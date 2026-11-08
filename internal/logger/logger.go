package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config defines options for initializing structured logging.
type Config struct {
	Level  string    // "debug", "info", "warn", "error" (defaults to "info")
	Format string    // "text", "json" (defaults to "text")
	Output io.Writer // Defaults to os.Stderr if nil
}

// ConfigFromEnv builds a Config from LOG_LEVEL and LOG_FORMAT environment variables.
func ConfigFromEnv() Config {
	return Config{
		Level:  os.Getenv("LOG_LEVEL"),
		Format: os.Getenv("LOG_FORMAT"),
	}
}

// Setup initializes the default slog logger with the given config and returns the instance.
func Setup(cfg Config) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}

	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(cfg.Level)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(cfg.Format), "json") {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}

	l := slog.New(handler)
	slog.SetDefault(l)
	return l
}
