// Command server is the YuHaul application entry point. It wires the storage,
// processing, scheduling, and HTTP layers into a single binary that serves the
// admin SPA and REST API on one port.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/bateau84/YuHaul/internal/advisory"
	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/scheduler"
	"github.com/bateau84/YuHaul/internal/server"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/stats"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/youtube"
)

// appConfig holds runtime configuration sourced from flags and environment
// variables. Flags take precedence over environment variables.
type appConfig struct {
	DBPath      string
	HTTPAddr    string
	WebRoot     string
	LogLevel    string
	DailyQuota  int

	OAuthClientID     string
	OAuthClientSecret string
	OAuthRedirectURL  string
	EncryptionKey     string
}

func main() {
	cfg := loadConfig()
	logger := newLogger(cfg.LogLevel)

	ctx := context.Background()

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		logger.ErrorContext(ctx, "failed to open database", "error", err, "path", cfg.DBPath)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	encKey, err := decodeKey(cfg.EncryptionKey)
	if err != nil {
		logger.ErrorContext(ctx, "invalid encryption key", "error", err)
		os.Exit(1)
	}

	tokenStore, err := auth.NewSQLiteTokenStore(db, encKey)
	if err != nil {
		logger.ErrorContext(ctx, "failed to open token store", "error", err)
		os.Exit(1)
	}

	authSvc, err := auth.NewService(&auth.ServiceConfig{
		ClientID:      cfg.OAuthClientID,
		ClientSecret:  cfg.OAuthClientSecret,
		RedirectURL:   cfg.OAuthRedirectURL,
		EncryptionKey: cfg.EncryptionKey,
	}, tokenStore, auth.NewInMemoryStateStore())
	if err != nil {
		logger.ErrorContext(ctx, "failed to configure auth", "error", err)
		os.Exit(1)
	}

	stateStore, err := state.NewStore(db)
	if err != nil {
		logger.ErrorContext(ctx, "failed to open state store", "error", err)
		os.Exit(1)
	}
	machine := state.NewMachine(stateStore)

	ledger := quota.New(db, quota.WithLogger(logger), quota.WithDefaultLimit(cfg.DailyQuota))
	if err := ledger.EnsureSchema(ctx); err != nil {
		logger.ErrorContext(ctx, "failed to ensure quota schema", "error", err)
		os.Exit(1)
	}

	routerStore := router.NewStore(db)
	configStore := config.NewStore(db)
	configSvc := config.NewService(routerStore, configStore)
	routerStore.WithOnChange(configSvc.Snapshot)

	metadataStore := metadata.NewStore(db)
	meta := metadata.NewManager(
		metadataStore,
		&metadataFetcher{auth: authSvc},
		ledger,
		metadata.WithLogger(logger),
	)

	detector := duplicate.NewDetector(db, duplicate.WithLogger(logger))
	discoveryEngine := discovery.NewEngine(authSvc, machine, ledger, db, discovery.WithLogger(logger))
	executor := pipeline.NewExecutor(db, ledger, authSvc, pipeline.WithExecutorLogger(logger))
	broadcast := pipeline.NewBroadcaster()

	queueStore, err := queue.NewStore(db)
	if err != nil {
		logger.ErrorContext(ctx, "failed to open queue store", "error", err)
		os.Exit(1)
	}
	queueSvc := queue.NewService(queueStore, nil).WithLogger(logger)

	statsStore := stats.NewStore(db)
	statsSvc := stats.NewService(statsStore, configStore, ledger)
	advisoryStore := advisory.NewStore(db)
	advisorySvc := advisory.NewService(advisoryStore, statsSvc, configStore)

	pl := pipeline.NewPipeline(
		db,
		machine,
		stateStore,
		discoveryEngine,
		detector,
		router.NewEngine(routerStore),
		executor,
		ledger,
		meta,
		authSvc,
		broadcast,
		pipeline.WithLogger(logger),
		pipeline.WithVersioner(configSvc),
		pipeline.WithQueueStore(queueStore),
		pipeline.WithConfigStore(configStore),
		pipeline.WithAdvisoryGenerator(advisorySvc),
	)

	configHandler := config.NewHandler(configSvc, logger)
	statsHandler := stats.NewHandler(statsSvc, logger)
	advisoryHandler := advisory.NewHandler(advisorySvc, logger)
	apiServer := server.NewServer(
		pl,
		broadcast,
		server.WithServerLogger(logger),
		server.WithConfigHandler(configHandler),
		server.WithAnalyticsHandler(statsHandler),
		server.WithAdvisoryHandler(advisoryHandler),
	)

	schedulerStore, err := scheduler.NewStore(db)
	if err != nil {
		logger.ErrorContext(ctx, "failed to open scheduler store", "error", err)
		os.Exit(1)
	}
	sch := scheduler.NewScheduler(schedulerStore, pl, stateStore, scheduler.WithLogger(logger))
	if err := sch.Start(ctx); err != nil {
		logger.ErrorContext(ctx, "failed to start scheduler", "error", err)
		os.Exit(1)
	}
	defer sch.Stop()

	root := http.NewServeMux()
	root.Handle("/api/", apiServer.Handler())
	root.Handle("/api/auth/", http.StripPrefix("/api/auth", authSvc.Handler()))
	root.Handle("/api/account/", http.StripPrefix("/api/account", authSvc.AccountHandler()))
	root.Handle("/api/errors/", http.StripPrefix("/api/errors", queueSvc.ErrorHandler()))
	root.Handle("/api/deferred/", http.StripPrefix("/api/deferred", queueSvc.DeferredHandler()))
	root.Handle("/", spaHandler(cfg.WebRoot))

	httpServer := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: root,
	}

	go func() {
		logger.InfoContext(ctx, "starting http server", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "http server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	logger.InfoContext(ctx, "shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(ctx, "graceful shutdown failed", "error", err)
	}
}

// loadConfig reads configuration from flags and environment variables.
func loadConfig() appConfig {
	var cfg appConfig
	flag.StringVar(&cfg.DBPath, "db-path", envDefault("YUHAUL_DB_PATH", "/data/yuhaul.db"), "path to the SQLite database file")
	flag.StringVar(&cfg.HTTPAddr, "http-addr", envDefault("YUHAUL_HTTP_ADDR", ":8080"), "HTTP listen address")
	flag.StringVar(&cfg.WebRoot, "web-root", envDefault("YUHAUL_WEB_ROOT", "/app/web"), "directory containing the built SPA")
	flag.StringVar(&cfg.LogLevel, "log-level", envDefault("YUHAUL_LOG_LEVEL", "info"), "log level (debug, info, warn, error)")
	flag.IntVar(&cfg.DailyQuota, "daily-quota", intEnvDefault("YUHAUL_DAILY_QUOTA", quota.DefaultDailyLimit), "YouTube Data API daily quota limit")
	flag.Parse()

	cfg.OAuthClientID = envDefault("YUHAUL_OAUTH_CLIENT_ID", "")
	cfg.OAuthClientSecret = envDefault("YUHAUL_OAUTH_CLIENT_SECRET", "")
	cfg.OAuthRedirectURL = envDefault("YUHAUL_OAUTH_REDIRECT_URL", "")
	cfg.EncryptionKey = envDefault("YUHAUL_ENCRYPTION_KEY", "")

	return cfg
}

// newLogger builds a structured JSON logger at the requested level.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// decodeKey accepts a raw 32-byte string or a base64-encoded 32-byte key.
func decodeKey(key string) ([]byte, error) {
	if len(key) == 32 {
		return []byte(key), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("encryption key must be 32 raw bytes or base64: %w", err)
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(decoded))
	}
	return decoded, nil
}

// envDefault returns the value of envVar if set, otherwise fallback.
func envDefault(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// intEnvDefault parses an integer environment variable, falling back to the
// provided default on parse failure.
func intEnvDefault(envVar string, fallback int) int {
	v := os.Getenv(envVar)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}

// spaHandler returns an http.Handler that serves a single-page application.
// Requests for existing static files are served directly; all other paths fall
// back to index.html so client-side routing can take over.
func spaHandler(root string) http.Handler {
	fsys := spaFS{root: http.Dir(root)}
	return http.FileServer(fsys)
}

// spaFS wraps a http.FileSystem so that missing files fall back to index.html.
type spaFS struct {
	root http.FileSystem
}

func (s spaFS) Open(name string) (http.File, error) {
	// Clean the path to avoid traversal outside the root.
	name = path.Clean("/" + name)
	f, err := s.root.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s.root.Open("/index.html")
		}
		return nil, err
	}
	return f, nil
}

// advisoryChannelAdapter adapts the stats store to the advisory service's
// ChannelReader interface. The two packages use separate nominal types for the
// same conceptual shape, so this thin wrapper converts between them.
type advisoryChannelAdapter struct {
	store *stats.Store
}

func (a *advisoryChannelAdapter) ListChannelStats(ctx context.Context) ([]advisory.ChannelStats, error) {
	rows, err := a.store.ListChannelStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]advisory.ChannelStats, len(rows))
	for i, r := range rows {
		out[i] = advisory.ChannelStats{
			ChannelID:        r.ChannelID,
			ChannelName:      r.ChannelName,
			TotalDiscovered:  r.TotalDiscovered,
			TotalRouted:      r.TotalRouted,
			TotalRetained:    r.TotalRetained,
			LastDiscoveredAt: r.LastDiscoveredAt,
		}
	}
	return out, nil
}

func (a *advisoryChannelAdapter) ListDuplicateProvenance(ctx context.Context, channelIDText string) ([]advisory.DuplicateProvenance, error) {
	rows, err := a.store.ListDuplicateProvenance(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	out := make([]advisory.DuplicateProvenance, len(rows))
	for i, r := range rows {
		out[i] = advisory.DuplicateProvenance{
			VideoID: r.VideoID,
		}
	}
	return out, nil
}

// metadataFetcher adapts the YouTube API client to the metadata.Fetcher
// interface used by the metadata lifecycle manager.
type metadataFetcher struct {
	auth *auth.Service
}

func (f *metadataFetcher) FetchMetadata(ctx context.Context, videoIDs []string) ([]metadata.TransientMetadata, error) {
	if len(videoIDs) == 0 {
		return nil, nil
	}
	token, err := f.auth.ActiveToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadata fetcher: no active token: %w", err)
	}
	client := youtube.NewClient(ctx, token)
	details, err := client.GetVideos(ctx, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("metadata fetcher: get videos: %w", err)
	}
	out := make([]metadata.TransientMetadata, 0, len(details))
	for _, d := range details {
		title := d.Title
		channel := d.ChannelTitle
		desc := d.Description
		dur := d.DurationSeconds
		thumb := d.ThumbnailURL
		pub := d.PublishedAt
		out = append(out, metadata.TransientMetadata{
			VideoID:         d.ID,
			Title:           &title,
			ChannelName:     &channel,
			Description:     &desc,
			DurationSeconds: &dur,
			ThumbnailURL:    &thumb,
			PublishedAt:     &pub,
		})
	}
	return out, nil
}
