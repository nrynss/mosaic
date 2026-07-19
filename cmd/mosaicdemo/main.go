// Command mosaicdemo composes Mosaic's local, synthetic v0.1 demonstration.
// It deliberately wires only the frozen P07 fixture, deterministic replay, the
// P08/P17 public read surfaces, and the checked-in static dashboard. No live model client
// or operational-system client is present in this executable.
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
	"strings"
	"syscall"
	"time"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/dataset"
	"mosaic.local/mosaic/internal/simulator"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/stream"
)

const (
	defaultListenAddress = "127.0.0.1:8080"
	defaultUIDirectory   = "ui/dist"
	defaultAssetRoot     = "."
)

type config struct {
	ListenAddress string
	DatabasePath  string
	UIDirectory   string
	AssetRoot     string
}

type application struct {
	handler http.Handler
	close   func() error
}

func parseConfig(args []string, getenv func(string) string) (config, error) {
	if getenv == nil {
		return config{}, errors.New("environment reader is required")
	}
	flags := flag.NewFlagSet("mosaicdemo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen-addr", valueOrDefault(getenv("MOSAIC_LISTEN_ADDR"), defaultListenAddress), "HTTP listen address")
	database := flags.String("db", valueOrDefault(getenv("MOSAIC_DB_PATH"), simulator.DefaultDBPath()), "SQLite database path or DSN")
	ui := flags.String("ui-dir", valueOrDefault(getenv("MOSAIC_UI_DIR"), defaultUIDirectory), "prebuilt dashboard directory")
	assets := flags.String("asset-root", valueOrDefault(getenv("MOSAIC_ASSET_ROOT"), defaultAssetRoot), "directory containing ontology and datasets")
	if err := flags.Parse(args); err != nil {
		return config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return normalizeConfig(config{
		ListenAddress: *listen,
		DatabasePath:  *database,
		UIDirectory:   *ui,
		AssetRoot:     *assets,
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

func newApplication(ctx context.Context, configuration config) (*application, error) {
	configuration, err := normalizeConfig(configuration)
	if err != nil {
		return nil, err
	}
	if err := dataset.Validate(configuration.AssetRoot); err != nil {
		return nil, fmt.Errorf("validate frozen dataset from %q: %w", configuration.AssetRoot, err)
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

	scenario, err := simulator.New(simulator.Config{
		Store:      database,
		SchemaDir:  filepath.Join(configuration.AssetRoot, "ontology"),
		FixtureDir: filepath.Join(configuration.AssetRoot, "datasets", simulator.DomesticDisturbance),
	})
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose frozen scenario: %w", err)
	}
	// Run intentionally re-delivers the same frozen raw-event IDs on every
	// startup. P05's durable source idempotency returns existing results, so a
	// later start verifies and recovers the original scenario without appending
	// a second event, model-run, or checkpoint history.
	run, err := scenario.Run(ctx)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("seed frozen scenario: %w", err)
	}
	advisoryReplay, err := simulator.NewAdvisoryReplay(simulator.AdvisoryReplayConfig{
		Store:      database,
		SchemaDir:  filepath.Join(configuration.AssetRoot, "ontology"),
		FixtureDir: filepath.Join(configuration.AssetRoot, "datasets", simulator.DomesticDisturbance),
	})
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose fixture advisory replay: %w", err)
	}
	timeline, err := advisoryTimeline(ctx, configuration, run)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("prepare fixture advisory timeline: %w", err)
	}
	if _, err := advisoryReplay.Replay(ctx, timeline); err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose fixture advisory history: %w", err)
	}

	resolver, err := api.NewSQLiteEvidenceResolver(database)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose governed evidence resolver: %w", err)
	}
	operations, err := api.NewSQLiteOperationsReader(database)
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose SQLite operations reader: %w", err)
	}
	apiServer, err := api.New(api.Config{
		Recovery:        scenario,
		Records:         database,
		Evidence:        resolver,
		Operations:      operations,
		AdvisoryHistory: database,
		AdvisoryMode:    "fixture_composed",
		Stream:          stream.NewBroker(),
		Version:         "v0.1",
	})
	if err != nil {
		_ = closeDatabase()
		return nil, fmt.Errorf("compose demo API: %w", err)
	}

	return &application{
		handler: composeHandler(apiServer.Handler(), dashboard),
		close:   closeDatabase,
	}, nil
}

// advisoryTimeline preserves the frozen rev-7/rev-9 snapshots required by P24
// when a retained main database makes P05 deliveries idempotent. The fallback
// uses a transient local SQLite store and the same checked-in scenario; it
// never changes the retained database or invokes a model/network client.
func advisoryTimeline(ctx context.Context, configuration config, run simulator.RunResult) ([]simulator.TimelineEntry, error) {
	if timelineHasRevision(run.Timeline, 7) && timelineHasRevision(run.Timeline, 9) {
		return run.Timeline, nil
	}

	temporary, err := store.Open(ctx, ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open transient fixture timeline store: %w", err)
	}
	defer temporary.Close()

	shadow, err := simulator.New(simulator.Config{
		Store:      temporary,
		SchemaDir:  filepath.Join(configuration.AssetRoot, "ontology"),
		FixtureDir: filepath.Join(configuration.AssetRoot, "datasets", simulator.DomesticDisturbance),
	})
	if err != nil {
		return nil, fmt.Errorf("compose transient fixture timeline: %w", err)
	}
	result, err := shadow.Run(ctx)
	if err != nil {
		return nil, fmt.Errorf("run transient fixture timeline: %w", err)
	}
	if !timelineHasRevision(result.Timeline, 7) || !timelineHasRevision(result.Timeline, 9) {
		return nil, errors.New("transient fixture timeline is missing required revisions")
	}
	return result.Timeline, nil
}

func timelineHasRevision(timeline []simulator.TimelineEntry, revision int64) bool {
	for _, entry := range timeline {
		if entry.StateRevision == revision {
			return true
		}
	}
	return false
}
func normalizeConfig(configuration config) (config, error) {
	configuration.ListenAddress = strings.TrimSpace(configuration.ListenAddress)
	configuration.DatabasePath = strings.TrimSpace(configuration.DatabasePath)
	configuration.UIDirectory = strings.TrimSpace(configuration.UIDirectory)
	configuration.AssetRoot = strings.TrimSpace(configuration.AssetRoot)
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
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return "", errors.New("parent path segments are not allowed")
		}
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
