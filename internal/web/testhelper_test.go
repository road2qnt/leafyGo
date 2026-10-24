package web

import (
	"io"
	"log/slog"
	"os"
)

func testLogger() *slog.Logger {
	return testLoggerTo(os.Stderr, slog.LevelError)
}

func testLoggerTo(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
