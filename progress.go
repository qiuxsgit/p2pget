package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode"

	"golang.org/x/term"
)

// Progress rows contain only our fixed Chinese labels and ASCII metrics.
func progressCells(text string) int {
	cells := 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			cells += 2
		} else {
			cells++
		}
	}
	return cells
}

func formatProgress(p progress, columns int) string {
	if columns <= 0 {
		columns = 80
	}
	budget := max(0, columns-1) // Avoid the terminal's pending-wrap state at the right edge.
	phase := p.Phase
	switch phase {
	case "下载 / 校验", "下载中":
		phase = "下载"
	case "寻找 / 排队":
		phase = "找源/排队"
	case "已完成":
		phase = "完成"
	}
	percent := float64(0)
	if p.Total > 0 {
		percent = min(100, max(0, 100*float64(p.Done)/float64(p.Total)))
	}
	speed := float64(max(0, p.Rate)) / (1 << 20)
	size := "大小未知"
	if p.Total > 0 {
		size = fmt.Sprintf("%.2f/%.2f GiB", float64(p.Done)/(1<<30), float64(p.Total)/(1<<30))
	}
	full := fmt.Sprintf("%s %.2f%% | %.2f MiB/s | 节点%d/%d | %s", phase, percent, speed, p.Peers, p.Known, size)
	detailed := full
	if p.Servers > 0 || p.Kad > 0 {
		detailed += fmt.Sprintf(" | 服务器%d Kad%d", p.Servers, p.Kad)
	}
	forms := []string{detailed, full, fmt.Sprintf("%s %.1f%%  %.2f MiB/s  节点%d  %s", phase, percent, speed, p.Peers, size), fmt.Sprintf("%s %.1f%% %.2f MiB/s", phase, percent, speed), fmt.Sprintf("%.1f%% %.2fMiB/s", percent, speed), fmt.Sprintf("%.1f%%", percent)}
	for _, line := range forms {
		if progressCells(line) <= budget {
			return line
		}
	}
	line := ""
	for _, r := range forms[len(forms)-1] {
		next := line + string(r)
		if progressCells(next) > budget {
			break
		}
		line = next
	}
	return line
}

// A wide character can wrap before the last column, leaving a padding cell.
func wrappedProgressRows(text string, columns int) int {
	if columns < 1 {
		return 1
	}
	rows, column := 1, 0
	for _, r := range text {
		cells := min(progressCells(string(r)), columns)
		if column+cells > columns {
			rows++
			column = 0
		}
		column += cells
	}
	return rows
}

type progressPrinter struct {
	mu       sync.Mutex
	out      io.Writer
	terminal bool
	columns  func() int
	active   bool
	lastText string
}

func newProgressPrinter(out io.Writer) *progressPrinter {
	printer := &progressPrinter{out: out, columns: func() int { return 120 }}
	if file, ok := out.(interface{ Fd() uintptr }); ok && os.Getenv("TERM") != "dumb" && term.IsTerminal(int(file.Fd())) && enableProgressANSI(file.Fd()) {
		printer.terminal = true
		printer.columns = func() int {
			width, _, err := term.GetSize(int(file.Fd()))
			if err != nil || width < 1 {
				return 80
			}
			return width
		}
	}
	return printer
}

func (p *progressPrinter) Render(status progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	width := p.columns()
	if width <= 0 {
		width = 80
	}
	line := formatProgress(status, width)
	if !p.terminal {
		fmt.Fprintln(p.out, line)
		return
	}
	fmt.Fprint(p.out, "\r\x1b[2K")
	if p.active {
		// A narrower window may have reflowed the previous frame onto several rows.
		rows := wrappedProgressRows(p.lastText, width)
		for i := 1; i < rows; i++ {
			fmt.Fprint(p.out, "\x1b[1A\r\x1b[2K")
		}
	}
	fmt.Fprint(p.out, line)
	p.active = true
	p.lastText = line
}

func (p *progressPrinter) finishLocked() {
	if p.active {
		fmt.Fprintln(p.out)
		p.active = false
		p.lastText = ""
	}
}
func (p *progressPrinter) Finish() { p.mu.Lock(); defer p.mu.Unlock(); p.finishLocked() }

// Engine diagnostics terminate the current progress row before writing their own record.
type progressLogHandler struct {
	printer *progressPrinter
	handler slog.Handler
}

func (h *progressLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}
func (h *progressLogHandler) Handle(ctx context.Context, record slog.Record) error {
	h.printer.mu.Lock()
	defer h.printer.mu.Unlock()
	h.printer.finishLocked()
	return h.handler.Handle(ctx, record)
}
func (h *progressLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &progressLogHandler{printer: h.printer, handler: h.handler.WithAttrs(attrs)}
}
func (h *progressLogHandler) WithGroup(name string) slog.Handler {
	return &progressLogHandler{printer: h.printer, handler: h.handler.WithGroup(name)}
}

type speedSample struct {
	at       time.Time
	received int64
}
type speedMeter struct{ samples []speedSample }

func (m *speedMeter) Observe(now time.Time, received int64) int64 {
	if len(m.samples) > 0 {
		last := m.samples[len(m.samples)-1]
		if now.Before(last.at) || received < last.received {
			m.samples = nil
		}
	}
	m.samples = append(m.samples, speedSample{now, received})
	cutoff := now.Add(-3 * time.Second)
	for len(m.samples) > 2 && !m.samples[1].at.After(cutoff) {
		m.samples = m.samples[1:]
	}
	first := m.samples[0]
	seconds := now.Sub(first.at).Seconds()
	if seconds <= 0 {
		return 0
	}
	return max(0, int64(float64(received-first.received)/seconds))
}

func defaultDownloadDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, "downloads"), nil
}
