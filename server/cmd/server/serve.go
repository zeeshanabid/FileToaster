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
	"filetoaster/internal/download"
	"filetoaster/internal/fs"
	"filetoaster/internal/protocol"
	"filetoaster/internal/service"
	"filetoaster/internal/upload"
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
//   - POST /api/fs/rename : authenticated synchronous rename endpoint (wrapped
//     in auth.Middleware); the selected paths and a new name are read from the
//     JSON request body. The first path is renamed to the exact name and the
//     rest to "name (N)"; a clash aborts with 409 without touching anything.
//   - POST /api/copy, POST /api/move, POST /api/delete, POST /api/compress, POST /api/decompress : authenticated task
//     endpoints (wrapped in auth.Middleware); copy/move read `paths` and a
//     `dest` from the body and move overwrites any same-named item at the
//     destination; delete reads only `paths`; compress reads `paths` and a
//     `format` (zip, targz, tar) and writes a single archive in place; decompress
//     reads `paths` and an `into` (extract here or into a sibling folder) and
//     extracts a single zip/tar/tar.gz archive read-only.
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
	mux.Handle("POST /api/fs/rename", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFsRename(w, r, fsCfg)
	})))
	mux.Handle("POST /api/fs/info", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFsInfo(w, r, fsCfg)
	})))
	mux.Handle("POST /api/fs/validate-path", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFsValidatePath(w, r, fsCfg)
	})))

	// Single-file download (tokenized): POST /api/downloads confines the selected
	// path to the allowed roots, issues a one-time fixed-length token, and returns
	// it; GET /downloads/{token} streams the file and consumes the token. Both are
	// authenticated like the fs endpoints, and the streaming URL carries only the
	// token, so no client path can reach it (traversal is impossible).
	downloadCfg := newDownloadConfig(func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }, download.NewRegistry(), cfg.DownloadBufferSize)
	mux.Handle("POST /api/downloads", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveDownloads(w, r, downloadCfg)
	})))
	mux.Handle("GET /downloads/{token}", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveDownloadToken(w, r, downloadCfg)
	})))
	downloadCleanup := startDownloadCleanup(downloadCfg.registry, downloadStaleAfter, downloadSweepInterval)

	// Task engine. The pool runs at most cfg.MaxParallelTasks tasks at once;
	// overflow queues. Copy is the first operation; more register the same way.
	// The task routes (copy, task snapshot/list, cancel, SSE progress) are all
	// authenticated like the fs endpoints.
	roots := func() *fs.Roots { return fs.NewRoots(pathsOf(fsCfg)...) }
	// Generate a unique instance ID for this server run to ensure task IDs are
	// globally unique across restarts (prevents cleared-task localStorage collision).
	instanceID := time.Now().UTC().Format("20060102T150405Z")
	taskService := service.NewService(cfg.MaxParallelTasks, logger, instanceID)
	registerCopyOp(taskService, roots, cfg)
	registerMoveOp(taskService, roots, cfg)
	registerDeleteOp(taskService, roots)
	registerCompressOp(taskService, roots, cfg)
	registerDecompressOp(taskService, roots, cfg)
	registerUploadOp(taskService, roots, cfg)
	// In-memory upload session registry: chunks stage here (under tmp_dir) while
	// a client uploads, then a tracked finalize op copies staging into the target.
	sessions := upload.NewRegistry()
	taskSrv := protocol.NewServer(taskService, logger,
		protocol.WithUploadStaging(cfg.TMPDir, sessions, roots),
		protocol.WithUploadSizing(cfg.UploadChunkSize, cfg.UploadMaxConcurrentChunks),
	)
	mux.Handle("POST /api/copy", guard.Middleware(http.HandlerFunc(taskSrv.ServeCopy)))
	mux.Handle("POST /api/move", guard.Middleware(http.HandlerFunc(taskSrv.ServeMove)))
	mux.Handle("POST /api/delete", guard.Middleware(http.HandlerFunc(taskSrv.ServeDelete)))
	mux.Handle("POST /api/compress", guard.Middleware(http.HandlerFunc(taskSrv.ServeCompress)))
	mux.Handle("POST /api/decompress", guard.Middleware(http.HandlerFunc(taskSrv.ServeDecompress)))
	// Upload (chunked + resumable): create a session, ingest per-chunk PUTs, read
	// resume status, and cancel (keeping chunks). The finalize is a tracked op.
	mux.Handle("POST /api/uploads", guard.Middleware(http.HandlerFunc(taskSrv.ServeCreateUpload)))
	mux.Handle("PUT /api/uploads/{id}/chunk", guard.Middleware(http.HandlerFunc(taskSrv.ServeUploadChunk)))
	mux.Handle("GET /api/uploads/{id}", guard.Middleware(http.HandlerFunc(taskSrv.ServeUploadStatus)))
	mux.Handle("DELETE /api/uploads/{id}", guard.Middleware(http.HandlerFunc(taskSrv.ServeUploadCancel)))
	mux.Handle("POST /api/uploads/{id}/resume", guard.Middleware(http.HandlerFunc(taskSrv.ServeUploadResume)))
	// Upload sizing the client should use (chunk size + max concurrent chunks).
	// Public so the SPA reads it before any session cookie exists.
	mux.HandleFunc("GET /api/config", taskSrv.ServeConfig)
	mux.Handle("GET /api/tasks", guard.Middleware(http.HandlerFunc(taskSrv.ServeTasksList)))
	mux.Handle("POST /api/tasks/clear", guard.Middleware(http.HandlerFunc(taskSrv.ServeClearTasks)))
	mux.Handle("GET /api/tasks/{id}", guard.Middleware(http.HandlerFunc(taskSrv.ServeTask)))
	mux.Handle("POST /api/tasks/{id}/cancel", guard.Middleware(http.HandlerFunc(taskSrv.ServeCancel)))
	mux.Handle("GET /api/events", guard.Middleware(http.HandlerFunc(taskSrv.ServeEvents)))

	files := http.FileServer(http.FS(filetoaster.Assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		files.ServeHTTP(w, r)
	})

	// Startup reclaim: remove staging dirs left behind by a previous run. Sessions
	// are in-memory and gone after a restart, so anything still on disk would be an
	// orphan; a fresh process must never conflict with a now-invalid task id.
	if removed, rerr := upload.ReclaimAll(cfg.TMPDir, sessions.Delete); rerr != nil {
		logger.Error("startup upload reclaim failed", "error", rerr.Error())
	} else if removed > 0 {
		logger.Info("reclaimed stale upload staging on startup", "dirs", removed)
	}
	// Background age sweep for abandoned/cancelled uploads. It is a standalone
	// goroutine (never a tracked task) and is stopped on shutdown below.
	cleanup := startUploadCleanup(cfg.TMPDir, cfg.UploadStaleAfterDuration(), cfg.UploadSweepIntervalDuration(), sessions.Delete, logger)

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
	// Stop the upload age sweep and wait for its scan to finish (never abrupt),
	// then stop the HTTP server once the task pool has drained.
	cleanup.cancel()
	<-cleanup.done
	// Stop the download token sweep and wait for its final scan to finish.
	downloadCleanup.cancel()
	<-downloadCleanup.done
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
