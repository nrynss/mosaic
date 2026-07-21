// Command mosaicdemo composes Mosaic's local synthetic demonstration.
// It wires the frozen fixture seed, interactive simulation controller,
// optional live model transport (server-only key), recurrence detector,
// public API, and the checked-in static dashboard.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/eventlog"
	"mosaic.local/mosaic/internal/eventlog/memory"
	"mosaic.local/mosaic/internal/pgstore"
	"mosaic.local/mosaic/internal/recurrence"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance"
	"mosaic.local/mosaic/internal/reference/registry"
	"mosaic.local/mosaic/internal/simulation"
	"mosaic.local/mosaic/internal/simulation/session"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/stream"
)

// progressiveDomain is the optional interactive beat processor exposed by the
// domestic-disturbance runtime. Composition type-asserts profile.Runtime to it.
type progressiveDomain interface {
	ProcessBeat(ctx context.Context, beatID string) error
	RawEventPayload(rawEventID string) ([]byte, error)
}

const (
	defaultListenAddress    = "127.0.0.1:8080"
	defaultUIDirectory      = "ui/dist"
	defaultAssetRoot        = "."
	defaultRecurrenceArea   = "road"
	defaultRecurrenceWindow = 72 * time.Hour
)

type config struct {
	ListenAddress string
	DatabasePath  string
	UIDirectory   string
	AssetRoot     string
	// ModelEnv is server-only runtime model selection (never flags).
	ModelEnv modelEnv
	// RecurrenceArea is the configured area key for deterministic recurrence.
	RecurrenceArea string
	// RecurrenceWindow is how far back prior handoffs are considered.
	RecurrenceWindow time.Duration
	// DemoBudgetUSD is the optional operator-configured demo budget used to
	// compute an "estimated remaining" figure on /api/v1/model-usage. Unset
	// or invalid values leave this nil, which omits budget fields entirely.
	DemoBudgetUSD *float64
	// SeedOnStart bulk-runs the fixture at boot (legacy). Default false: the
	// board stays empty until Play drives progressive EventLog beats.
	// Env: MOSAIC_SEED_ON_START=1|true|yes|on
	SeedOnStart bool
	// BeatSpacing is equal inter-beat SSE pacing for the interactive controller.
	// Zero means use simulation.DefaultBeatSpacing via BeatSpacingFromEnv path;
	// composition always sets a positive spacing from env/defaults.
	BeatSpacing time.Duration
}

type application struct {
	handler http.Handler
	close   func() error

	// Composed surfaces exposed for package tests (not HTTP).
	modelProviders contracts.AgentProviderSelection
	simulation     *session.Controller
	recurrence     *recurrence.Detector
}

func parseConfig(args []string, getenv func(string) string) (config, error) {
	if getenv == nil {
		return config{}, errors.New("environment reader is required")
	}
	flags := flag.NewFlagSet("mosaicdemo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listenPort := getenv("MOSAIC_LISTEN_ADDR")
	if listenPort == "" {
		if p := getenv("PORT"); p != "" {
			listenPort = "0.0.0.0:" + p
		} else {
			listenPort = defaultListenAddress
		}
	}
	listen := flags.String("listen-addr", listenPort, "HTTP listen address")
	database := flags.String("db", valueOrDefault(getenv("MOSAIC_DB_PATH"), defaultDatabasePath()), "SQLite database path or postgres:// DSN")
	ui := flags.String("ui-dir", valueOrDefault(getenv("MOSAIC_UI_DIR"), defaultUIDirectory), "prebuilt dashboard directory")
	assets := flags.String("asset-root", valueOrDefault(getenv("MOSAIC_ASSET_ROOT"), defaultAssetRoot), "directory containing ontology and datasets")
	if err := flags.Parse(args); err != nil {
		return config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	// OPENAI_API_KEY and MOSAIC_*_PROVIDER are environment-only (no CLI flags)
	// so the secret never appears in process argument lists.
	area := valueOrDefault(getenv("MOSAIC_RECURRENCE_AREA"), defaultRecurrenceArea)
	window := defaultRecurrenceWindow
	if raw := strings.TrimSpace(getenv("MOSAIC_RECURRENCE_WINDOW")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse MOSAIC_RECURRENCE_WINDOW: %w", err)
		}
		window = parsed
	}
	return normalizeConfig(config{
		ListenAddress:    *listen,
		DatabasePath:     *database,
		UIDirectory:      *ui,
		AssetRoot:        *assets,
		ModelEnv:         parseModelEnv(getenv),
		RecurrenceArea:   area,
		RecurrenceWindow: window,
		DemoBudgetUSD:    parseDemoBudgetUSD(getenv("MOSAIC_DEMO_BUDGET_USD")),
		SeedOnStart:      parseSeedOnStart(getenv("MOSAIC_SEED_ON_START")),
		BeatSpacing:      simulation.ParseBeatSpacing(getenv(simulation.EnvBeatSpacing)),
	})
}

// parseSeedOnStart interprets MOSAIC_SEED_ON_START. Default is false (progressive).
func parseSeedOnStart(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// parseDemoBudgetUSD parses the optional MOSAIC_DEMO_BUDGET_USD env var as a
// float. An unset or unparsable value is ignored (returns nil) rather than
// failing startup; the model-usage endpoint simply omits budget fields.
func parseDemoBudgetUSD(raw string) *float64 {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mosaicdemo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	configuration, err := parseConfig(args, getenv)
	if err != nil {
		return err
	}
	app, err := newApplication(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { _ = app.close() }()

	listener, err := net.Listen("tcp", configuration.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", configuration.ListenAddress, err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           app.handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()

	fmt.Fprintf(stdout, "mosaicdemo listening on http://%s (synthetic fixture ready)\n", listener.Addr())
	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		if err := <-serveResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("stop HTTP server: %w", err)
		}
		return nil
	}
}

// newApplication composes exactly one internal domain profile and one
// separately configured UI asset directory. The selected profile owns
// deterministic startup, recovery, and state-fact evidence. This host owns
// generic persistence, interactive simulation, model selection, recurrence,
// and API plumbing. Live models are opt-in and require a server-only key.
func newApplication(ctx context.Context, configuration config) (*application, error) {
	configuration, err := normalizeConfig(configuration)
	if err != nil {
		return nil, err
	}
	selected, err := registry.Resolve(registry.DefaultID)
	if err != nil {
		return nil, fmt.Errorf("select domain profile: %w", err)
	}
	if err := selected.Validate(configuration.AssetRoot); err != nil {
		return nil, fmt.Errorf("validate frozen assets for profile %q from %q: %w", selected.ID(), configuration.AssetRoot, err)
	}
	dashboard, err := newDashboardHandler(configuration.UIDirectory)
	if err != nil {
		return nil, err
	}

	var (
		records         contracts.ImmutableRecordRepository
		advisoryHistory contracts.AdvisoryHistoryReader
		closeDatabase   func() error
		operations      api.OperationsReader
		// domainStore is the single durable backend used for seed, recovery,
		// models, records, and advisories. SQLite file path and Postgres DSN
		// never split across two stores.
		domainStore composeStore
		// recovery is domainRuntime by default; Postgres prefers the
		// materialized COP read model when present.
		preferMaterialized contracts.COPReadModelRepository
	)

	isPostgres := strings.HasPrefix(configuration.DatabasePath, "postgres://") || strings.HasPrefix(configuration.DatabasePath, "postgresql://")

	if isPostgres {
		pg, err := pgstore.Open(ctx, configuration.DatabasePath)
		if err != nil {
			return nil, fmt.Errorf("open Postgres database: %w", err)
		}
		records = pg
		advisoryHistory = pg
		domainStore = pg
		preferMaterialized = pg
		closeDatabase = func() error { return pg.Close() }

		operations, err = api.NewPostgresOperationsReader(pg.Pool())
		if err != nil {
			_ = closeDatabase()
			return nil, fmt.Errorf("compose Postgres operations reader: %w", err)
		}
	} else {
		db, err := store.Open(ctx, configuration.DatabasePath)
		if err != nil {
			return nil, fmt.Errorf("open SQLite database %q: %w", configuration.DatabasePath, err)
		}
		records = db
		advisoryHistory = db
		domainStore = db
		closeDatabase = func() error { return db.Close() }

		operations, err = api.NewSQLiteOperationsReader(db)
		if err != nil {
			_ = closeDatabase()
			return nil, fmt.Errorf("compose SQLite operations reader: %w", err)
		}
	}

	// Active session epoch (C3): empty board until Play; natural end leaves
	// Active set so the final progressive COP remains visible.
	activeSession := session.NewActiveSession()

	// Compose builds the profile's deterministic scenario, fixture advisory
	// continuum, and evidence resolver over the single durable store. Active is
	// passed so Postgres materialization writes session-scoped COP keys.
	composeCtx := domesticdisturbance.WithActiveSession(ctx, activeSession)
	domainRuntime, err := selected.Compose(composeCtx, records, configuration.AssetRoot)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose domain profile %q: %w", selected.ID(), err)
	}

	// Default: no bulk seed — board empty until Play. Optional MOSAIC_SEED_ON_START
	// restores bulk Run for non-progressive proofs (and skips ActiveSession
	// isolation so the seeded board is visible at boot).
	seedOnStart := configuration.SeedOnStart
	if seedOnStart {
		if err := domainRuntime.Run(ctx); err != nil {
			_ = closeDatabase()
			return nil, fmt.Errorf("seed domain profile %q: %w", selected.ID(), err)
		}
	}

	schedule, ok := domainRuntime.(contracts.SimulationSchedule)
	if !ok {
		_ = closeDatabase()
		return nil, fmt.Errorf("domain profile %q does not expose a simulation beat schedule", selected.ID())
	}

	// EventLog transport: Postgres store implements Append; SQLite demos use an
	// in-memory log. Domain data always stays on the single durable store (E1).
	var beatLog eventlog.EventLog
	if isPostgres {
		if pg, ok := domainStore.(*pgstore.Store); ok {
			beatLog = pg
		}
	}
	if beatLog == nil {
		beatLog = memory.New()
	}

	// Progressive OnBeat: EventLog.Append then sync domain ProcessBeat
	// (ingest+project+advisory continuum). Documented: EventLog is the append
	// seam; the sync handler is the interactive consumer (not multi-worker Run).
	var onBeat func(context.Context, contracts.ScheduledBeat) error
	if !seedOnStart {
		progressive, ok := domainRuntime.(progressiveDomain)
		if !ok {
			_ = closeDatabase()
			return nil, fmt.Errorf("domain profile %q does not support progressive ProcessBeat (required unless MOSAIC_SEED_ON_START)", selected.ID())
		}
		partitionKey := selected.ID()
		onBeat = func(beatCtx context.Context, beat contracts.ScheduledBeat) error {
			payload, err := progressive.RawEventPayload(beat.RawEventID)
			if err != nil {
				return err
			}
			if err := beatLog.Append(beatCtx, eventlog.EventEnvelope{
				PartitionKey:   partitionKey,
				IdempotencyKey: beat.RawEventID,
				Type:           simulation.EventTypeRawEvent,
				Payload:        payload,
			}); err != nil {
				return fmt.Errorf("event log append beat %q: %w", beat.BeatID, err)
			}
			if err := progressive.ProcessBeat(beatCtx, beat.BeatID); err != nil {
				return fmt.Errorf("process beat %q: %w", beat.BeatID, err)
			}
			return nil
		}
	}

	beatSpacing := configuration.BeatSpacing
	if beatSpacing <= 0 {
		beatSpacing = simulation.DefaultBeatSpacing
	}
	// Seed-on-start legacy path keeps relative schedule delays (no equal spacing
	// flood control required — board is already final). Progressive uses spacing.
	sessionCfg := session.Config{
		Schedule: schedule,
	}
	if !seedOnStart {
		sessionCfg.BeatSpacing = beatSpacing
		sessionCfg.Active = activeSession
		sessionCfg.OnBeat = onBeat
	}
	// else: Active nil + OnBeat nil so GET /cop shows the seeded board without Play.
	simController, err := session.New(sessionCfg)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose simulation controller: %w", err)
	}

	schemaDir := filepath.Join(configuration.AssetRoot, "ontology")
	fixtureDir := filepath.Join(configuration.AssetRoot, "datasets", selected.ID())
	identities := selected.Identities()
	models, err := composeModels(ctx, domainStore, configuration.AssetRoot, fixtureDir, schemaDir, identities.Supervisor, configuration.ModelEnv)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose model adapters: %w", err)
	}

	// Recurrence is deterministic and reviewable; composition constructs the
	// detector for later surfaces. It never contacts external parties.
	area := strings.TrimSpace(configuration.RecurrenceArea)
	if area == "" {
		area = defaultRecurrenceArea
	}
	window := configuration.RecurrenceWindow
	if window <= 0 {
		window = defaultRecurrenceWindow
	}
	detector := recurrence.NewDetector(area, window, time.Now)

	// Recovery: progressive path scopes on ActiveSession (empty until Play).
	// Postgres prefers session-scoped materialization; SQLite uses domain recover
	// gated by ActiveSession on the API Server.
	recovery := api.RecoveryReader(domainRuntime)
	var apiActive contracts.ActiveSessionSource
	if !seedOnStart {
		apiActive = activeSession
		if preferMaterialized != nil {
			scoped := pgstore.NewSessionScopedCOP(preferMaterialized, activeSession)
			recovery = api.PreferMaterializedRecovery{
				Materialized: scoped,
				Active:       activeSession,
				// No unscoped Fallback when Active is set (PreferMaterializedRecovery).
			}
		}
	} else if preferMaterialized != nil {
		recovery = api.PreferMaterializedRecovery{
			Materialized: preferMaterialized,
			Fallback:     domainRuntime,
		}
	}

	apiServer, err := api.New(api.Config{
		Recovery:          recovery,
		Records:           records,
		Evidence:          domainRuntime,
		Operations:        operations,
		AdvisoryHistory:   advisoryHistory,
		AdvisoryMode:      "fixture_composed",
		Stream:            stream.NewBroker(),
		Actors:            api.PublicActorResolver{ViewerIdentity: identities.Viewer, SupervisorIdentity: identities.Supervisor},
		Version:           "v0.1",
		Simulation:        simController,
		Terra:             models.Terra,
		Sol:               models.Sol,
		Luna:              models.Luna,
		ProviderSelection: models.ProviderSelection,
		BriefingRequester: models.BriefingRequester,
		APIKeyConfigured:  strings.TrimSpace(configuration.ModelEnv.APIKey) != "",
		DemoBudgetUSD:     configuration.DemoBudgetUSD,
		ActiveSession:     apiActive,
	})
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose demo API: %w", err)
	}

	return &application{
		handler:        composeHandler(apiServer.Handler(), dashboard),
		close:          closeDatabase,
		modelProviders: models.ProviderSelection,
		simulation:     simController,
		recurrence:     detector,
	}, nil
}

// defaultDatabasePath keeps the interactive demo database outside the
// repository. It mirrors the generic local default without importing a domain
// package into this composition root.
func defaultDatabasePath() string {
	return filepath.Join(os.TempDir(), "mosaic-v0.1-demo.db")
}

func normalizeConfig(configuration config) (config, error) {
	configuration.ListenAddress = strings.TrimSpace(configuration.ListenAddress)
	configuration.DatabasePath = strings.TrimSpace(configuration.DatabasePath)
	configuration.UIDirectory = strings.TrimSpace(configuration.UIDirectory)
	configuration.AssetRoot = strings.TrimSpace(configuration.AssetRoot)
	configuration.RecurrenceArea = strings.TrimSpace(configuration.RecurrenceArea)
	if configuration.ListenAddress == "" {
		return config{}, errors.New("listen address is required")
	}
	if configuration.DatabasePath == "" {
		return config{}, errors.New("database path or DSN is required")
	}
	if configuration.UIDirectory == "" {
		return config{}, errors.New("prebuilt UI directory is required")
	}
	if configuration.AssetRoot == "" {
		return config{}, errors.New("asset root containing ontology and datasets is required")
	}
	if configuration.RecurrenceArea == "" {
		configuration.RecurrenceArea = defaultRecurrenceArea
	}
	if configuration.RecurrenceWindow <= 0 {
		configuration.RecurrenceWindow = defaultRecurrenceWindow
	}
	// Default unset agent providers to fixture without requiring parseConfig.
	if configuration.ModelEnv.Luna == "" {
		configuration.ModelEnv.Luna = contracts.ProviderFixture
	}
	if configuration.ModelEnv.Terra == "" {
		configuration.ModelEnv.Terra = contracts.ProviderFixture
	}
	if configuration.ModelEnv.Sol == "" {
		configuration.ModelEnv.Sol = contracts.ProviderFixture
	}

	assetRoot, err := filepath.Abs(configuration.AssetRoot)
	if err != nil {
		return config{}, fmt.Errorf("resolve asset root %q: %w", configuration.AssetRoot, err)
	}
	uiDirectory, err := filepath.Abs(configuration.UIDirectory)
	if err != nil {
		return config{}, fmt.Errorf("resolve UI directory %q: %w", configuration.UIDirectory, err)
	}
	configuration.AssetRoot = assetRoot
	configuration.UIDirectory = uiDirectory
	return configuration, nil
}

func composeHandler(apiHandler, dashboard http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keep the entire versioned API namespace out of the SPA fallback. This
		// includes unknown API paths so an API typo cannot become an HTML 200.
		if r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		dashboard.ServeHTTP(w, r)
	})
}

type dashboardHandler struct {
	root  string
	index string
}

func newDashboardHandler(directory string) (http.Handler, error) {
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve prebuilt UI directory %q: %w", directory, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect prebuilt UI directory %q: %w", directory, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("prebuilt UI directory %q is not a directory", directory)
	}
	index, exists, err := resolveUIFile(root, "index.html")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("prebuilt UI directory %q does not contain index.html; run the dashboard build first", directory)
	}
	return dashboardHandler{root: root, index: index}, nil
}

func (h dashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "dashboard is read-only", http.StatusMethodNotAllowed)
		return
	}
	relative, err := safeUIRelativePath(r)
	if err != nil {
		http.Error(w, "invalid dashboard path", http.StatusBadRequest)
		return
	}
	file, exists, err := resolveUIFile(h.root, relative)
	if err != nil {
		http.Error(w, "dashboard asset is unavailable", http.StatusNotFound)
		return
	}
	if !exists {
		// A missing non-API path is a client-side dashboard route. The fallback
		// remains contained to the verified index file under h.root.
		file = h.index
	}
	opened, err := os.Open(file)
	if err != nil {
		http.Error(w, "dashboard asset is unavailable", http.StatusNotFound)
		return
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil {
		http.Error(w, "dashboard asset is unavailable", http.StatusNotFound)
		return
	}
	http.ServeContent(w, r, filepath.Base(file), info.ModTime(), opened)
}

func safeUIRelativePath(r *http.Request) (string, error) {
	escaped := r.URL.EscapedPath()
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", err
	}
	if strings.Contains(decoded, "\\") {
		return "", errors.New("backslash path separators are not allowed")
	}
	if slices.Contains(strings.Split(decoded, "/"), "..") {
		return "", errors.New("parent path segments are not allowed")
	}
	cleaned := path.Clean("/" + decoded)
	relative := strings.TrimPrefix(cleaned, "/")
	if relative == "" || relative == "." {
		return "index.html", nil
	}
	return relative, nil
}

func resolveUIFile(root, relative string) (string, bool, error) {
	if relative == "" || strings.Contains(relative, "\\") {
		return "", false, errors.New("invalid dashboard asset path")
	}
	candidate := filepath.Join(root, filepath.FromSlash(relative))
	inside, err := withinRoot(root, candidate)
	if err != nil || !inside {
		return "", false, errors.New("dashboard asset escapes prebuilt UI directory")
	}
	info, err := os.Stat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		return "", false, nil
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false, err
	}
	inside, err = withinRoot(root, resolved)
	if err != nil || !inside {
		return "", false, errors.New("dashboard asset symlink escapes prebuilt UI directory")
	}
	return resolved, true, nil
}

func withinRoot(root, candidate string) (bool, error) {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, err
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative), nil
}
