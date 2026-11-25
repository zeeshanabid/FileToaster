// Command server is FileToaster's single embedded binary entrypoint. It resolves
// (and, if absent, creates) the config file, loads and validates it, applies
// GOMAXPROCS and the debug log level, then serves the embedded SvelteKit UI on
// the configured address until interrupted.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"filetoaster/internal/auth"
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

	// Resolve the admin credentials: precedence is FILETOASTER_ADMIN_PASSWORD
	// (env) over the config's admin_password_hash over the built-in default admin
	// password. The result is always non-blank, so the server never starts with a
	// blank admin password.
	adminUser := cfg.AdminUser
	adminHash, err := resolveAdminHash(cfg.AdminPasswordHash)
	if err != nil {
		logger.Error("could not initialise admin password", "error", err.Error())
		return err
	}
	guard, err := auth.NewGuard(cfg.SessionSecret, adminUser, adminHash)
	if err != nil {
		logger.Error("could not initialise authentication", "error", err.Error())
		return err
	}

	// Persist a rotated admin password back to the config file so the change
	// survives restarts. When FILETOASTER_ADMIN_PASSWORD is set, the resolver
	// prefers the env var, so this persisted hash is only used once the env var
	// is cleared (see the client-side warning on the change-password dialog).
	guard.ConfigSaver = func(newHash string) error {
		return config.ChangeAdminPassword(path, newHash)
	}

	logger.Info("configuration loaded",
		"addr", cfg.Addr,
		"allowed_paths", cfg.AllowedPaths,
		"max_parallel_tasks", cfg.MaxParallelTasks,
		"shutdown_timeout", cfg.ShutdownTimeout,
		"gomaxprocs", cfg.GOMAXPROCS,
		"tmp_dir", cfg.TMPDir,
		"session_secret", maskSecret(cfg.SessionSecret),
		"admin_user", guard.User,
		"admin_password_set", guard.Hash != "",
	)

	return startServer(cfg, guard, logger)
}

// resolveAdminHash returns the Argon2id hash to authenticate admins with,
// applying precedence: FILETOASTER_ADMIN_PASSWORD over configHash over the
// default admin password. It never returns an empty value.
func resolveAdminHash(configHash string) (string, error) {
	if envPwd := os.Getenv("FILETOASTER_ADMIN_PASSWORD"); envPwd != "" {
		hashed, err := auth.HashPassword(envPwd)
		if err != nil {
			return "", fmt.Errorf("hash admin password from environment: %w", err)
		}
		return hashed, nil
	}
	if configHash != "" {
		return configHash, nil
	}
	hashed, err := auth.HashPassword(auth.DefaultPassword)
	if err != nil {
		return "", fmt.Errorf("hash default admin password: %w", err)
	}
	return hashed, nil
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
