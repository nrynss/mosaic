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
	"strings"
	"syscall"
	"time"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/recurrence"
	"mosaic.local/mosaic/internal/reference/registry"
	"mosaic.local/mosaic/internal/simsession"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/stream"
)

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
}

type application struct {
	handler http.Handler
	close   func() error

	// Composed surfaces exposed for package tests (not HTTP).
	modelProviders contracts.AgentProviderSelection
	simulation     *simsession.Controller
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
	database := flags.String("db", valueOrDefault(getenv("MOSAIC_DB_PATH"), defaultDatabasePath()), "SQLite database path or DSN")
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
	})
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

	database, err := store.Open(ctx, configuration.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database %q: %w", configuration.DatabasePath, err)
	}
	closeDatabase := func() error { return database.Close() }

	// Compose builds the profile's deterministic scenario, fixture advisory
	// replay, and evidence resolver over the shared store. Run then seeds them:
	// P05's durable source idempotency means a later start verifies and recovers
	// the original history without appending duplicate events or advisory records.
	domainRuntime, err := selected.Compose(ctx, database, configuration.AssetRoot)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose domain profile %q: %w", selected.ID(), err)
	}
	if err := domainRuntime.Run(ctx); err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("seed domain profile %q: %w", selected.ID(), err)
	}

	schedule, ok := domainRuntime.(contracts.SimulationSchedule)
	if !ok {
		_ = closeDatabase()
		return nil, fmt.Errorf("domain profile %q does not expose a simulation beat schedule", selected.ID())
	}
	simController, err := simsession.New(simsession.Config{Schedule: schedule})
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose simulation controller: %w", err)
	}

	schemaDir := filepath.Join(configuration.AssetRoot, "ontology")
	fixtureDir := filepath.Join(configuration.AssetRoot, "datasets", selected.ID())
	identities := selected.Identities()
	models, err := composeModels(ctx, database, configuration.AssetRoot, fixtureDir, schemaDir, identities.Supervisor, configuration.ModelEnv)
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

	operations, err := api.NewSQLiteOperationsReader(database)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose SQLite operations reader: %w", err)
	}
	apiServer, err := api.New(api.Config{
		Recovery:          domainRuntime,
		Records:           database,
		Evidence:          domainRuntime,
		Operations:        operations,
		AdvisoryHistory:   database,
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
		return config{}, errors.New("SQLite database path is required")
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
