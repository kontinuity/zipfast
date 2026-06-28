// Command zipfast is the Zipfast API + file server.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zipfast/internal/auth"
	"zipfast/internal/config"
	"zipfast/internal/datasource"
	"zipfast/internal/db"
	"zipfast/internal/logger"
	"zipfast/internal/server"
	"zipfast/internal/tasks"
	"zipfast/internal/thumbnails"
)

// version is overridable at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log := logger.Log("server")

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	store, err := db.New(ctx, cfg.Core.DatabaseURL)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		log.Error("failed to run migrations", "err", err)
		os.Exit(1)
	}
	log.Info("database ready")

	ds, err := buildDatasource(cfg)
	if err != nil {
		log.Error("failed to init datasource", "err", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(cfg.Core.TempDirectory, 0o755); err != nil {
		log.Error("failed to create temp directory", "err", err)
		os.Exit(1)
	}

	app := &server.App{
		Cfg:      cfg,
		Store:    store,
		DS:       ds,
		Log:      log,
		Version:  version,
		Sessions: auth.NewSessionManager(cfg.Core.Secret, cfg.Core.ReturnHTTPSURLs),
	}

	addr := fmt.Sprintf("%s:%d", cfg.Core.Hostname, cfg.Core.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Router(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	// Background tasks (deleteFiles, maxViews, clearInvites, metrics).
	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()
	tasks.Start(taskCtx, store, ds, cfg, log)
	thumbnails.Start(taskCtx, store, ds, cfg, log)

	go func() {
		log.Info("listening", "addr", addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// buildDatasource constructs the storage backend. S3 is wired in once the S3
// implementation lands; for now only local is supported here.
func buildDatasource(cfg *config.Config) (datasource.Datasource, error) {
	switch cfg.Datasource.Type {
	case "local", "":
		return datasource.NewLocal(cfg.Datasource.Local.Directory)
	case "s3", "b2":
		// Backblaze B2 is served through the S3-compatible datasource (see config.applyEnv).
		return datasource.NewS3(cfg.Datasource.S3)
	default:
		return nil, fmt.Errorf("unsupported datasource type %q", cfg.Datasource.Type)
	}
}
