package main

import (
	"context"
	"errors"
	"filetoaster"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"filetoaster/internal/config"
)

// startServer serves the embedded frontend on cfg.Addr and blocks until the
// process receives SIGINT or SIGTERM, then shuts down gracefully. It performs
// no file operations or task logic — it only serves the static SvelteKit UI.
func startServer(cfg *config.Config, logger *slog.Logger) error {
	handler := http.FileServer(http.FS(filetoaster.Assets))
	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
	}

	go func() {
		logger.Info("serving FileToaster UI", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err.Error())
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("shutdown signal received; stopping server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err.Error())
		return err
	}
	logger.Info("server stopped")
	return nil
}
