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
	"filetoaster/internal/fs"
	"filetoaster/internal/protocol"
	"filetoaster/internal/service"
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
//   - POST /api/copy, POST /api/move, POST /api/delete : authenticated task
//     endpoints (wrapped in auth.Middleware); copy/move read `paths` and a
//     `dest` from the body and move overwrites any same-named item at the
//     destination; delete reads only `paths`.
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

	// Task engine. The pool runs at most cfg.MaxParallelTasks tasks at once;
	// overflow queues. Copy is the first operation; more register the same way.
	// The task routes (copy, task snapshot/list, cancel, SSE progress) are all
	// authenticated like the fs endpoints.
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	taskService := service.NewService(cfg.MaxParallelTasks, logger)
	registerCopyOp(taskService, roots)
	registerMoveOp(taskService, roots)
	registerDeleteOp(taskService, roots)
	taskSrv := protocol.NewServer(taskService, logger)
	mux.Handle("POST /api/copy", guard.Middleware(http.HandlerFunc(taskSrv.ServeCopy)))
	mux.Handle("POST /api/move", guard.Middleware(http.HandlerFunc(taskSrv.ServeMove)))
	mux.Handle("POST /api/delete", guard.Middleware(http.HandlerFunc(taskSrv.ServeDelete)))
	mux.Handle("GET /api/tasks", guard.Middleware(http.HandlerFunc(taskSrv.ServeTasksList)))
	mux.Handle("GET /api/tasks/{id}", guard.Middleware(http.HandlerFunc(taskSrv.ServeTask)))
	mux.Handle("POST /api/tasks/{id}/cancel", guard.Middleware(http.HandlerFunc(taskSrv.ServeCancel)))
	mux.Handle("GET /api/events", guard.Middleware(http.HandlerFunc(taskSrv.ServeEvents)))

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
	// Drain running tasks first so their final progress reaches subscribed
	// clients, then stop accepting HTTP connections (which closes the SSE
	// streams). The pool keeps existing operations running for up to the
	// configured shutdown timeout before cancelling them and returning.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer shutdownCancel()
	taskService.Shutdown(shutdownCtx)
	// Stop the HTTP server once the task pool has drained (or been force-cancelled).
	httpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer httpCancel()
	if err := srv.Shutdown(httpCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err.Error())
		return err
	}
	logger.Info("server stopped")
	return nil
}
