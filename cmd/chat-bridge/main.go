// Command chat-bridge runs the unified chat HTTP API with the registered platform adapters.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gimhq/chat-bridge/internal/adapters/matrix"
	"gimhq/chat-bridge/internal/adapters/remote"
	"gimhq/chat-bridge/internal/adapters/telegram"
	"gimhq/chat-bridge/internal/adapters/whatsapp"
	"gimhq/chat-bridge/internal/config"
	"gimhq/chat-bridge/internal/core"
	"gimhq/chat-bridge/internal/media"
	"gimhq/chat-bridge/internal/server"
	"gimhq/chat-bridge/internal/store"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("bridge exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "config file (default: $CHATBRIDGE_CONFIG, then ./chat-bridge.yaml if present)")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Log.Level)
	slog.SetDefault(log)
	log.Info("starting chat-bridge", "version", version, "config", cfg.Source, "data_dir", cfg.Storage.DataDir)

	if err := os.MkdirAll(cfg.Storage.DataDir, 0o750); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(cfg.Storage.DataDir, "chatbridge.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	blobs, err := media.Open(filepath.Join(cfg.Storage.DataDir, "media"))
	if err != nil {
		return err
	}

	c := core.New(core.Options{
		Store: st, Blobs: blobs, DataDir: cfg.Storage.DataDir, Logger: log,
		Media:          core.MediaPolicy{AutoDownload: cfg.Media.AutoDownload, MaxBytes: cfg.Media.AutoDownloadMaxMB << 20},
		EventRetention: time.Duration(cfg.Events.RetentionDays) * 24 * time.Hour,
	})
	c.Register(whatsapp.New(log.With("adapter", "whatsapp")))
	c.Register(telegram.New(log.With("adapter", "telegram")))
	c.Register(matrix.New(log.With("adapter", "matrix")))
	if err := c.Start(ctx); err != nil {
		return err
	}
	defer c.Stop(context.Background())

	var hub http.Handler
	if cfg.Server.AdapterToken != "" {
		hub = remote.NewHub(c, cfg.Server.AdapterToken, cfg.Server.PublicURL, log.With("component", "adapter-hub"))
		log.Info("remote adapters enabled", "path", "/adapter/v1")
	}
	srv := &http.Server{
		Addr: cfg.Server.Addr,
		Handler: server.New(server.Options{
			Core: c, Token: cfg.Server.Token, Version: version, MaxUploadBytes: cfg.Media.MaxUploadMB << 20, Logger: log, AdapterHub: hub,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
