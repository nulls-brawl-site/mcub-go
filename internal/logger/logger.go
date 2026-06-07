// Package logger provides a structured logger for MCUB.
// It wraps the standard log package with level-based filtering and
// optional coloured output for terminals.
// Enhanced with KernelLogger, ErrorFormatter, and log-file helpers ported
// from core/lib/utils/logger.py.
// SPDX-License-Identifier: MIT
package logger

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---- Level ------------------------------------------------------------------

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

// ParseLevel converts "debug", "info", "warn", "error" to Level.
// Returns LevelInfo for unknown strings.
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

// ---- Logger -----------------------------------------------------------------

// Logger is a levelled logger that wraps log.Logger.
type Logger struct {
	mu     sync.Mutex
	inner  *log.Logger
	level  Level
	prefix string
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

// emit is the internal helper.
func (l *Logger) emit(lvl Level, format string, args ...interface{}) {
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
func (l *Logger) Debug(format string, args ...interface{}) { l.emit(LevelDebug, format, args...) }

// Info emits an info-level log line.
func (l *Logger) Info(format string, args ...interface{}) { l.emit(LevelInfo, format, args...) }

// Warn emits a warning-level log line.
func (l *Logger) Warn(format string, args ...interface{}) { l.emit(LevelWarn, format, args...) }

// Error emits an error-level log line.
func (l *Logger) Error(format string, args ...interface{}) { l.emit(LevelError, format, args...) }

// Printf implements a Printf-compatible interface.
func (l *Logger) Printf(format string, args ...interface{}) { l.Info(format, args...) }

// ---- SetupLogging -----------------------------------------------------------

const (
	_logDir  = "logs"
	_logFile = "logs/kernel.log"
)

// SetupLogging creates the logs/ directory if needed and returns a Logger that
// writes to both stderr (WARN+) and the rotating log file.
func SetupLogging() (*Logger, error) {
	if err := os.MkdirAll(_logDir, 0o755); err != nil {
		return nil, fmt.Errorf("logger: create log dir: %w", err)
	}
	f, err := os.OpenFile(_logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logger: open log file: %w", err)
	}
	w := io.MultiWriter(os.Stderr, f)
	return New(w, LevelWarn), nil
}

// SaveToFile appends text to logs/kernel.log, creating the file/directory as
// needed.
func SaveToFile(text string) error {
	if err := os.MkdirAll(_logDir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(_logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), text)
	return err
}

// ---- MaskSensitiveData ------------------------------------------------------

var sensitivePatterns = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)token(?:['"\s:=])+([A-Za-z0-9_\-:,.]+)`), `token="***"`},
	{regexp.MustCompile(`(?i)api[_-]?id(?:['"\s:=])+(\d+)`), `api_id=***`},
	{regexp.MustCompile(`(?i)api[_-]?hash(?:['"\s:=])+([A-Za-z0-9_-]+)`), `api_hash=***`},
	{regexp.MustCompile(`(?i)api[_-]?key(?:['"\s:=])+([^,}\])"'\s]+)`), `api_key=***`},
	{regexp.MustCompile(`(?i)password(?:['"\s:=])+([^\s"'&]+)`), `password=***`},
	{regexp.MustCompile(`(?i)session(?:['"\s:=])+([A-Za-z0-9_-]+)`), `session=***`},
	{regexp.MustCompile(`(?i)Authorization:\s*.+`), `Authorization: ***`},
}

var longNumberRE = regexp.MustCompile(`\b\d{10,}\b`)

// MaskSensitiveData replaces API keys, tokens, passwords, and similar secrets
// in text with placeholder strings.
func MaskSensitiveData(text string) string {
	if text == "" {
		return text
	}
	for _, p := range sensitivePatterns {
		text = p.re.ReplaceAllString(text, p.replacement)
	}
	text = longNumberRE.ReplaceAllStringFunc(text, func(s string) string {
		return strings.Repeat("X", len(s))
	})
	return text
}

// ---- ErrorFormatter ---------------------------------------------------------

// ErrorFormatter formats Go errors and stack traces for human-readable output.
// Ported from the Python ErrorFormatter class in core/lib/utils/logger.py.
type ErrorFormatter struct{}

// Short returns a concise single-line description of err.
func (ef ErrorFormatter) Short(err error) string {
	if err == nil {
		return "<nil>"
	}
	return MaskSensitiveData(err.Error())
}

// FormatTraceback formats a raw Go stack-trace line.
// It converts absolute file paths to shorter module-relative paths where possible.
func (ef ErrorFormatter) FormatTraceback(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	// Highlight goroutine headers.
	if strings.HasPrefix(line, "goroutine ") {
		return ">> " + line
	}
	// Highlight file references.
	if strings.HasPrefix(line, "\t") || strings.Contains(line, ".go:") {
		return "  " + strings.TrimPrefix(line, "\t")
	}
	return line
}

// FormatFullTraceback formats a full multi-line stack trace.
func (ef ErrorFormatter) FormatFullTraceback(tb string) string {
	lines := strings.Split(tb, "\n")
	var out []string
	for _, l := range lines {
		formatted := ef.FormatTraceback(l)
		if formatted != "" {
			out = append(out, formatted)
		}
	}
	return strings.Join(out, "\n")
}

// CaptureStack returns the current goroutine call stack as a formatted string,
// skipping the top skip frames.
func (ef ErrorFormatter) CaptureStack(skip int) string {
	buf := make([]byte, 8192)
	n := runtime.Stack(buf, false)
	return ef.FormatFullTraceback(string(buf[:n]))
}

// ---- KernelLogger -----------------------------------------------------------

// TelegramSender is implemented by any object that can send Telegram messages.
// It is satisfied by the MCUB kernel's client wrapper.
type TelegramSender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// KernelLogger sends structured log messages to a Telegram log chat and also
// writes them to the rotating log file.
type KernelLogger struct {
	mu      sync.Mutex
	kernel  interface{}
	chatID  int64
	enabled bool
	sender  TelegramSender
	logger  *Logger
}

// NewKernelLogger creates a KernelLogger. Pass a non-zero chatID to enable
// Telegram forwarding; pass a TelegramSender implementation to provide the
// underlying transport.
func NewKernelLogger(kernel interface{}, chatID int64, sender TelegramSender) *KernelLogger {
	l, _ := SetupLogging()
	if l == nil {
		l = Default()
	}
	return &KernelLogger{
		kernel:  kernel,
		chatID:  chatID,
		enabled: chatID != 0 && sender != nil,
		sender:  sender,
		logger:  l,
	}
}

// SendLogMessage sends text to the configured Telegram log chat.
// The text is masked for sensitive data before sending.
func (kl *KernelLogger) SendLogMessage(ctx context.Context, text string) error {
	safe := MaskSensitiveData(text)
	_ = SaveToFile(safe)

	kl.mu.Lock()
	enabled := kl.enabled
	sender := kl.sender
	chatID := kl.chatID
	kl.mu.Unlock()

	if !enabled || sender == nil {
		return nil
	}
	return sender.SendMessage(ctx, chatID, safe)
}

// SendError formats err and sends it to the log chat.
// source is a short identifier of where the error originated (e.g. module name
// or function). msgInfo is additional context such as the triggering message text.
func (kl *KernelLogger) SendError(ctx context.Context, err error, source, msgInfo string) error {
	if err == nil {
		return nil
	}
	ef := ErrorFormatter{}
	text := fmt.Sprintf("ERROR in %s: %s", source, ef.Short(err))
	if msgInfo != "" {
		text += "\nMessage: " + MaskSensitiveData(msgInfo)
	}
	return kl.SendLogMessage(ctx, text)
}

// Log writes a plain info message to the log chat and file.
func (kl *KernelLogger) Log(ctx context.Context, message string) error {
	return kl.SendLogMessage(ctx, message)
}

// LogError writes an error-level message prefixed with a marker.
func (kl *KernelLogger) LogError(ctx context.Context, message string) error {
	return kl.SendLogMessage(ctx, "[ERROR] "+message)
}

// SetEnabled toggles Telegram forwarding at runtime.
func (kl *KernelLogger) SetEnabled(enabled bool) {
	kl.mu.Lock()
	defer kl.mu.Unlock()
	kl.enabled = enabled && kl.sender != nil && kl.chatID != 0
}

// Underlying returns the inner *Logger for standard levelled logging.
func (kl *KernelLogger) Underlying() *Logger {
	return kl.logger
}
