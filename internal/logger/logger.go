package logger

import (
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
)

type Options struct {
	AppEnv        string
	Level         string
	FilePath      string
	RotateMaxMB   int
	RotateBackups int
	DedupeWindow  time.Duration
}

func New(opts Options) (zerolog.Logger, io.Closer, error) {
	appEnv := strings.TrimSpace(opts.AppEnv)
	if appEnv == "" {
		appEnv = "dev"
	}

	level := zerolog.InfoLevel
	if raw := strings.TrimSpace(opts.Level); raw != "" {
		parsed, err := zerolog.ParseLevel(strings.ToLower(raw))
		if err != nil {
			return zerolog.Logger{}, nil, fmt.Errorf("parse LOG_LEVEL: %w", err)
		}
		level = parsed
	}

	zerolog.TimeFieldFormat = time.RFC3339Nano

	var (
		writers []io.Writer
		closers []io.Closer
		fileErr error
	)
	writers = append(writers, os.Stdout)

	filePath := strings.TrimSpace(opts.FilePath)
	if filePath != "" {
		maxBytes := int64(opts.RotateMaxMB) * 1024 * 1024
		fileWriter, err := newRotatingFileWriter(filePath, maxBytes, opts.RotateBackups)
		if err != nil {
			fileErr = err
		} else {
			writers = append(writers, fileWriter)
			closers = append(closers, fileWriter)
		}
	}

	sink := io.MultiWriter(writers...)
	if opts.DedupeWindow > 0 {
		sink = newDedupeWriter(sink, opts.DedupeWindow)
	}
	sink = zerolog.SyncWriter(sink)

	base := zerolog.New(sink).
		With().
		Timestamp().
		Str("app_env", appEnv).
		Int("pid", os.Getpid()).
		Logger().
		Level(level)

	zerolog.SetGlobalLevel(level)
	zlog.Logger = base

	stdlog.SetFlags(0)
	stdlog.SetPrefix("")
	stdlog.SetOutput(&stdLogBridge{
		log: base.With().Str("source", "stdlib").Logger(),
	})

	if fileErr != nil {
		base.Warn().
			Err(fileErr).
			Str("log_file_path", filePath).
			Msg("file logging disabled; using stdout only")
	}

	var closer io.Closer
	if len(closers) > 0 {
		closer = multiCloser(closers)
	}
	return base, closer, nil
}

type stdLogBridge struct {
	log zerolog.Logger
}

func (b *stdLogBridge) Write(p []byte) (int, error) {
	n := len(p)
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return n, nil
	}
	b.log.Info().Msg(msg)
	return n, nil
}

type dedupeWriter struct {
	next      io.Writer
	window    time.Duration
	mu        sync.Mutex
	seen      map[string]time.Time
	lastClean time.Time
}

func newDedupeWriter(next io.Writer, window time.Duration) io.Writer {
	return &dedupeWriter{
		next:      next,
		window:    window,
		seen:      make(map[string]time.Time, 512),
		lastClean: time.Now(),
	}
}

func (w *dedupeWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	key := dedupeKey(p)
	if key == "" {
		return w.next.Write(p)
	}

	now := time.Now()

	w.mu.Lock()
	if ts, ok := w.seen[key]; ok && now.Sub(ts) < w.window {
		w.seen[key] = now
		w.mu.Unlock()
		return n, nil
	}
	w.seen[key] = now
	if len(w.seen) > 4096 || now.Sub(w.lastClean) >= w.window*8 {
		cutoff := now.Add(-w.window * 8)
		for k, t := range w.seen {
			if t.Before(cutoff) {
				delete(w.seen, k)
			}
		}
		w.lastClean = now
	}
	w.mu.Unlock()

	return w.next.Write(p)
}

func dedupeKey(p []byte) string {
	line := strings.TrimSpace(string(p))
	if line == "" {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return line
	}

	delete(payload, zerolog.TimestampFieldName)
	delete(payload, "timestamp")
	delete(payload, "ts")

	b, err := json.Marshal(payload)
	if err != nil {
		return line
	}
	return string(b)
}

type rotatingFileWriter struct {
	path       string
	maxBytes   int64
	maxBackups int

	mu   sync.Mutex
	file *os.File
	size int64
}

func newRotatingFileWriter(path string, maxBytes int64, maxBackups int) (*rotatingFileWriter, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return nil, fmt.Errorf("LOG_FILE_PATH cannot be empty when file logging is enabled")
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("LOG_ROTATE_MAX_MB must be >= 0")
	}
	if maxBackups < 0 {
		return nil, fmt.Errorf("LOG_ROTATE_BACKUPS must be >= 0")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir log dir: %w", err)
	}

	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat log file: %w", err)
	}

	return &rotatingFileWriter{
		path:       p,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
		file:       f,
		size:       info.Size(),
	}, nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, fmt.Errorf("log file is closed")
	}

	if w.maxBytes > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingFileWriter) rotateLocked() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	if w.maxBackups <= 0 {
		_ = os.Remove(w.path)
	} else {
		oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
		_ = os.Remove(oldest)
		for i := w.maxBackups - 1; i >= 1; i-- {
			src := fmt.Sprintf("%s.%d", w.path, i)
			dst := fmt.Sprintf("%s.%d", w.path, i+1)
			_ = os.Rename(src, dst)
		}
		_ = os.Rename(w.path, w.path+".1")
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open rotated log file: %w", err)
	}
	w.file = f
	w.size = 0
	return nil
}

func (w *rotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var firstErr error
	for _, c := range m {
		if c == nil {
			continue
		}
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
