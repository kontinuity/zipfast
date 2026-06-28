// Command zipfast is the Zipfast API + file server.
package main

import (
	"context"
	"encoding/json"
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

	// Overlay DB-persisted settings (saved via the admin Settings UI) onto the
	// env-based config, so runtime settings take effect — exactly like Zipline
	// (defaults -> DB -> env, env wins).
	if data, _, lerr := store.LoadSettings(ctx); lerr == nil && len(data) > 0 {
		var blob map[string]any
		if json.Unmarshal(data, &blob) == nil {
			if eff, berr := config.BuildEffective(blob); berr == nil {
				cfg = eff
			} else {
				log.Warn("failed to apply db settings to config", "err", berr)
			}
		}
	}

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
		Tampered: config.EnvTamperedKeys(),
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
	case "s3":
		// S3-compatible storage (AWS S3, MinIO, Backblaze B2, etc.). For Backblaze,
		// set DATASOURCE_S3_ENDPOINT to the bucket's S3 endpoint (e.g.
		// s3.us-west-004.backblazeb2.com), as in upstream Zipline.
		return datasource.NewS3(cfg.Datasource.S3)
	default:
		return nil, fmt.Errorf("unsupported datasource type %q", cfg.Datasource.Type)
	}
}
