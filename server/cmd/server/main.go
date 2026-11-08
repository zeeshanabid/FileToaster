// Command server is FileToaster's single embedded binary entrypoint. It resolves
// (and, if absent, creates) the config file, loads and validates it, applies
// GOMAXPROCS and the debug log level, then serves the embedded SvelteKit UI on
// the configured address until interrupted.
package main

import (
	"flag"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"filetoaster/internal/config"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(slog.LevelInfo)

	configFlag := flag.String("config-file", "", "path to the YAML config file (default: ./config.yaml, then ~/.filetoaster/config.yaml, or created there on first run)")
	flag.Parse()

	path, created, err := config.ResolveConfigPath(*configFlag)
	if err != nil {
		logger.Error("could not locate config file", "error", err.Error())
		return err
	}
	if created {
		logger.Info("no config file found; created default config", "path", path)
	} else {
		logger.Info("loaded config file", "path", path)
	}

	cfg, err := config.Load(path)
	if err != nil {
		logger.Error("invalid configuration", "path", path, "error", err.Error())
		return err
	}

	// Apply the configured debug level (default: info) to the logger.
	logLevel := slog.LevelInfo
	if cfg.Debug {
		logLevel = slog.LevelDebug
	}
	logger = newLogger(logLevel)

	if cfg.Debug {
		logger.Debug("debug logging enabled")
	} else {
		logger.Info("log level is info (set debug: true in the config to enable debug logging)")
	}

	if cfg.GOMAXPROCS > 0 {
		runtime.GOMAXPROCS(cfg.GOMAXPROCS)
		logger.Info("GOMAXPROCS set", "value", cfg.GOMAXPROCS)
	}

	logger.Info("configuration loaded",
		"addr", cfg.Addr,
		"allowed_paths", cfg.AllowedPaths,
		"max_parallel_tasks", cfg.MaxParallelTasks,
		"gomaxprocs", cfg.GOMAXPROCS,
		"tmp_dir", cfg.TMPDir,
		"session_secret", maskSecret(cfg.SessionSecret),
		"admin_password_hash_set", cfg.AdminPasswordHash != "",
	)

	return startServer(cfg, logger)
}

// newLogger returns an slog text handler writing to stderr at the given level.
func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// maskSecret hides a secret value for safe logging.
func maskSecret(s string) string {
	if s == "" {
		return "<empty>"
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", 12)
}
