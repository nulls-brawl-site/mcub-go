// Package logger provides a simple structured logger for MCUB.
// It wraps the standard log package with level-based filtering and
// optional coloured output for terminals.
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// Level represents a log verbosity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the human-readable level name.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel converts a string such as "debug", "info", "warn", "error"
// to the corresponding Level. Returns LevelInfo for unknown strings.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Logger is a levelled logger that wraps log.Logger.
type Logger struct {
	mu      sync.Mutex
	inner   *log.Logger
	level   Level
	prefix  string
}

// New creates a new Logger writing to w with the given minimum level.
func New(w io.Writer, level Level) *Logger {
	return &Logger{
		inner: log.New(w, "", log.LstdFlags),
		level: level,
	}
}

// Default returns a Logger writing to os.Stderr at LevelInfo.
func Default() *Logger {
	return New(os.Stderr, LevelInfo)
}

// WithPrefix returns a shallow copy of the logger with a prefix prepended to
// each log line's tag.
func (l *Logger) WithPrefix(prefix string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &Logger{
		inner:  l.inner,
		level:  l.level,
		prefix: prefix,
	}
}

// SetLevel changes the minimum log level.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// log is the internal emit helper.
func (l *Logger) log(lvl Level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lvl < l.level {
		return
	}
	tag := lvl.String()
	if l.prefix != "" {
		tag = l.prefix + "/" + tag
	}
	msg := fmt.Sprintf(format, args...)
	l.inner.Printf("[%s] %s", tag, msg)
}

// Debug emits a debug-level log line.
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Info emits an info-level log line.
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Warn emits a warning-level log line.
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Error emits an error-level log line.
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// Printf implements a Printf-compatible interface so the logger can be passed
// to packages that accept a Printf logger (e.g. the telegram-mcub-go client).
func (l *Logger) Printf(format string, args ...interface{}) {
	l.Info(format, args...)
}
