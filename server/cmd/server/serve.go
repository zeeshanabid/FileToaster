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

	"filetoaster/internal/auth"
	"filetoaster/internal/config"
)

// startServer serves the embedded frontend on cfg.Addr and blocks until the
// process receives SIGINT or SIGTERM, then shuts down gracefully.
//
// Routing:
//   - POST /api/auth/login, POST /api/auth/logout, POST /api/auth/change-password:
//     public auth endpoints reachable without a cookie.
//   - GET  /api/auth/status : requires a valid session cookie (self-guarded).
//   - POST /api/fs/roots, POST /api/fs : authenticated directory-listing
//     endpoints (wrapped in auth.Middleware); the listing path is read from the
//     JSON request body.
//   - POST /api/fs/mkdir : authenticated directory-creation endpoint (wrapped in
//     auth.Middleware); the parent directory and new name are read from the JSON
//     request body.
//   - POST /api/fs/info : authenticated information endpoint (wrapped in
//     auth.Middleware); the selected paths are read from the JSON request body
//     and their recursive size/file/dir counts and permissions are returned.
//   - everything else : the embedded SvelteKit SPA, served publicly so the login
//     screen can load before any cookie exists. auth.Middleware is available to
//     protect future data endpoints; the SPA shell itself must stay public.
func startServer(cfg *config.Config, guard *auth.Guard, logger *slog.Logger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", guard.ServeLogin)
	mux.HandleFunc("POST /api/auth/logout", guard.ServeLogout)
	mux.HandleFunc("POST /api/auth/change-password", guard.ServeChangePassword)
	mux.HandleFunc("GET /api/auth/status", guard.ServeStatus)

	// Directory listing endpoints. They require a valid session cookie (the SPA
	// shell and the auth routes stay public so the login screen can load before
	// any cookie exists); the fs package confines every request to the configured
	// allowed roots. The listing path is read from the JSON request body (POST),
	// not the URL, so it is not subject to URL length limits.
	fsCfg := newFSConfig(cfg)
	mux.Handle("POST /api/fs/roots", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRoots(w, r, fsCfg)
	})))
	mux.Handle("POST /api/fs", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFsList(w, r, fsCfg)
	})))
	mux.Handle("POST /api/fs/mkdir", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFsMkdir(w, r, fsCfg)
	})))
	mux.Handle("POST /api/fs/info", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFsInfo(w, r, fsCfg)
	})))

	files := http.FileServer(http.FS(filetoaster.Assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		files.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
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
