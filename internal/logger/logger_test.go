package logger

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestNewFailsWhenFilePathInvalid(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("prepare file: %v", err)
	}

	_, closer, err := New(Options{
		AppEnv:       "test",
		Level:        "info",
		FilePath:     filepath.Join(notDir, "bot.log"),
		DedupeWindow: 0,
	})
	if err == nil {
		t.Fatal("expected logger init error")
	}
	if closer != nil {
		_ = closer.Close()
	}
	if !strings.Contains(err.Error(), "init file logging") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewAllowsStdoutOnlyWhenFilePathEmpty(t *testing.T) {
	logg, closer, err := New(Options{
		AppEnv:       "test",
		Level:        "info",
		FilePath:     "",
		DedupeWindow: 0,
	})
	if err != nil {
		t.Fatalf("logger init must allow empty file path: %v", err)
	}
	if closer != nil {
		_ = closer.Close()
	}
	if logg.GetLevel() != zerolog.InfoLevel {
		t.Fatalf("unexpected level=%s", logg.GetLevel())
	}
}

func TestDedupeWriterSuppressesExactEventIgnoringTimestamp(t *testing.T) {
	var out bytes.Buffer
	w := newDedupeWriter(&out, 2*time.Second)

	first := `{"level":"info","time":"2026-04-12T11:52:04Z","message":"generation done","job_id":38}` + "\n"
	second := `{"level":"info","time":"2026-04-12T11:52:05Z","message":"generation done","job_id":38}` + "\n"
	third := `{"level":"info","time":"2026-04-12T11:52:06Z","message":"generation done","job_id":39}` + "\n"

	if _, err := w.Write([]byte(first)); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if _, err := w.Write([]byte(second)); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if _, err := w.Write([]byte(third)); err != nil {
		t.Fatalf("write third: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d want=2 output=%q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"job_id":38`) {
		t.Fatalf("first line mismatch: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"job_id":39`) {
		t.Fatalf("second line mismatch: %q", lines[1])
	}
}

func TestRotatingFileWriterBySize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bot.log")

	w, err := newRotatingFileWriter(path, 32, 2)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	first := strings.Repeat("a", 24) + "\n"
	second := strings.Repeat("b", 24) + "\n"

	if _, err := w.Write([]byte(first)); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if _, err := w.Write([]byte(second)); err != nil {
		t.Fatalf("write second: %v", err)
	}

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read rotated file: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current file: %v", err)
	}
	if strings.TrimSpace(string(rotated)) != strings.TrimSpace(first) {
		t.Fatalf("rotated mismatch got=%q want=%q", strings.TrimSpace(string(rotated)), strings.TrimSpace(first))
	}
	if strings.TrimSpace(string(current)) != strings.TrimSpace(second) {
		t.Fatalf("current mismatch got=%q want=%q", strings.TrimSpace(string(current)), strings.TrimSpace(second))
	}
}
