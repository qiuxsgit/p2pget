package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/qiuxsgit/p2pget/internal/engine"
)

// The progress vocabulary contains ASCII and Chinese, so this independent
// screen-cell check catches byte/rune-count based wrapping regressions.
func testCells(value string) int {
	n := 0
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			n += 2
		} else {
			n++
		}
	}
	return n
}
func sampleProgress() engine.Progress {
	return engine.Progress{Phase: "下载 / 校验", Done: 1234567890, Total: 9264506880, Peers: 142, Known: 184, Servers: 4, Kad: 175, Rate: 1234567}
}

func TestProgressFitsNarrowTerminal(t *testing.T) {
	for _, width := range []int{12, 20, 40, 60, 80, 120} {
		line := formatProgress(sampleProgress(), width)
		if strings.ContainsAny(line, "\r\n\x1b") {
			t.Fatalf("control character in formatted line: %q", line)
		}
		if got := testCells(line); got > width-1 {
			t.Fatalf("width %d: progress needs %d screen cells: %q", width, got, line)
		}
	}
}

func TestProgressTerminalClearsLineAndEndsOnce(t *testing.T) {
	var out bytes.Buffer
	printer := &progressPrinter{out: &out, terminal: true, columns: func() int { return 80 }}
	printer.Render(sampleProgress())
	p := sampleProgress()
	p.Peers = 1
	printer.Render(p)
	printer.Finish()
	printer.Finish()
	if got := strings.Count(out.String(), "\r\x1b[2K"); got != 2 {
		t.Fatalf("expected one clear per update, got %d: %q", got, out.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("terminal updates should share one line: %q", out.String())
	}
}

func TestProgressResizeRemovesWrappedPreviousFrame(t *testing.T) {
	var out bytes.Buffer
	width := 120
	printer := &progressPrinter{out: &out, terminal: true, columns: func() int { return width }}
	printer.Render(sampleProgress())
	out.Reset()
	width = 20
	printer.Render(sampleProgress())
	if !strings.Contains(out.String(), "\x1b[1A") {
		t.Fatalf("previous frame's wrapped rows were not cleared: %q", out.String())
	}
	last := strings.LastIndex(out.String(), "\x1b[2K")
	if testCells(out.String()[last+4:]) >= width {
		t.Fatalf("new frame still wraps: %q", out.String())
	}
}

func TestProgressRedirectedOutputUsesPlainLines(t *testing.T) {
	var out bytes.Buffer
	printer := newProgressPrinter(&out)
	printer.Render(sampleProgress())
	printer.Render(sampleProgress())
	printer.Finish()
	if strings.ContainsAny(out.String(), "\r\x1b") {
		t.Fatalf("redirected log contains terminal controls: %q", out.String())
	}
	if strings.Count(out.String(), "\n") != 2 {
		t.Fatalf("expected two log records: %q", out.String())
	}
}

func TestProgressDiagnosticsStartOnNewLine(t *testing.T) {
	var out bytes.Buffer
	printer := &progressPrinter{out: &out, terminal: true, columns: func() int { return 80 }}
	logger := slog.New(&progressLogHandler{printer: printer, handler: slog.NewTextHandler(&out, nil)})
	printer.Render(sampleProgress())
	logger.ErrorContext(context.Background(), "diagnostic")
	if !strings.Contains(out.String(), "\ntime=") {
		t.Fatalf("diagnostic overwrote the progress line: %q", out.String())
	}
}

func TestRateUsesWindowRatherThanLastPollingTick(t *testing.T) {
	meter := speedMeter{}
	start := time.Unix(1, 0)
	meter.Observe(start, 0)
	meter.Observe(start.Add(100*time.Millisecond), 1<<20)
	got := meter.Observe(start.Add(time.Second), 1<<20)
	if got != 1<<20 {
		t.Fatalf("expected 1 MiB averaged over first second, got %d", got)
	}
	if got = meter.Observe(start.Add(1100*time.Millisecond), 1<<20); got <= 0 {
		t.Fatal("a quiet polling tick incorrectly reset download speed to zero")
	}
	if got = meter.Observe(start.Add(5*time.Second), 1<<20); got != 0 {
		t.Fatalf("speed did not fall to zero after the window went idle: %d", got)
	}
	if got = meter.Observe(start.Add(6*time.Second), 10); got != 0 {
		t.Fatalf("counter reset produced a false rate: %d", got)
	}
}

func TestDefaultDownloadDirSurvivesGoRun(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := defaultDownloadDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(cwd, "downloads") {
		t.Fatalf("default directory is not relative to the working directory: %s", got)
	}
}

func TestProgressResizeAccountsForWideCharacterPadding(t *testing.T) {
	var out bytes.Buffer
	width := 120
	printer := &progressPrinter{out: &out, terminal: true, columns: func() int { return width }}
	p := sampleProgress()
	p.Phase = "寻找 / 排队"
	p.Rate = 100 << 20
	p.Peers = 1
	p.Known = 1
	printer.Render(p)
	out.Reset()
	width = 37
	printer.Render(p)
	// At 37 columns this Chinese/ASCII frame occupies three rows: a wide
	// character wraps with one cell unused. Dividing total cells gives only two.
	if got := strings.Count(out.String(), "\x1b[1A"); got != 2 {
		t.Fatalf("expected to clear two prior rows, got %d: %q", got, out.String())
	}
}
