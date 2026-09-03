package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"llm-proxy/internal/app"
	"llm-proxy/webui"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := app.LoadConfig()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	databasePath := filepath.Join(cfg.DataDir, "llm-proxy.db")
	vault, err := app.LoadOrCreateVault(cfg.DataDir, databasePath)
	if err != nil {
		logger.Error("initialize credential vault", "error", err)
		os.Exit(1)
	}
	store, err := app.OpenStore(databasePath)
	if err != nil {
		logger.Error("initialize database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.MarkInterrupted(context.Background()); err != nil {
		logger.Error("mark interrupted requests", "error", err)
		os.Exit(1)
	}

	application := app.NewServer(cfg, store, vault, webui.FS(), logger)
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	application.StartBackground(ctx)
	defer application.StopBackground()

	httpServer := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	go func() {
		logger.Info("LLM Proxy is ready", "address", "http://"+cfg.ListenAddress, "data_dir", cfg.DataDir)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve HTTP", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	stopSignals()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown", "error", err)
		if closeErr := httpServer.Close(); closeErr != nil {
			logger.Error("force close HTTP server", "error", closeErr)
		}
	}
}
