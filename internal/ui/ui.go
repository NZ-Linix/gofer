// Package ui renders terminal progress bars and spinners.
package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

var tty = IsTerminal(os.Stdout)

const clearLine = "\r\033[K"

// HumanBytes formats n as a binary size, e.g. "1.5 MiB".
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// FormatDuration formats d as m:ss or h:mm:ss.
func FormatDuration(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// ---------------------------------------------------------------- Progress

// Progress is an io.Writer that renders a transfer progress bar.
type Progress struct {
	label string
	total int64
	n     int64
	start time.Time

	lastDraw time.Time
	lastN    int64
	lastT    time.Time
	rate     float64 // smoothed bytes/sec
}

func NewProgress(label string, total int64) *Progress {
	now := time.Now()
	return &Progress{label: label, total: total, start: now, lastT: now}
}

func (p *Progress) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if now := time.Now(); now.Sub(p.lastDraw) >= 100*time.Millisecond {
		p.sample(now)
		p.lastDraw = now
		p.draw()
	}
	return len(b), nil
}

func (p *Progress) sample(now time.Time) {
	dt := now.Sub(p.lastT).Seconds()
	if dt <= 0 {
		return
	}
	inst := float64(p.n-p.lastN) / dt
	if p.rate == 0 {
		p.rate = inst
	} else {
		p.rate = 0.8*p.rate + 0.2*inst
	}
	p.lastN, p.lastT = p.n, now
}

func (p *Progress) draw() {
	if !tty {
		return
	}
	frac := 1.0
	if p.total > 0 {
		frac = float64(p.n) / float64(p.total)
	}
	eta := "--:--"
	if p.rate > 0 && p.total > p.n {
		eta = FormatDuration(time.Duration(float64(p.total-p.n) / p.rate * float64(time.Second)))
	}
	fmt.Printf("%s%s %s %3.0f%%  %s/%s  %s/s  ETA %s",
		clearLine, p.label, bar(frac, 24), frac*100,
		HumanBytes(p.n), HumanBytes(p.total), HumanBytes(int64(p.rate)), eta)
}

// Done replaces the bar with a summary line.
func (p *Progress) Done() {
	elapsed := time.Since(p.start)
	avg := float64(p.n) / max(elapsed.Seconds(), 0.001)
	if tty {
		fmt.Print(clearLine)
	}
	fmt.Printf("%s %s %s in %s (%s/s)\n", p.label, bar(1, 24),
		HumanBytes(p.n), FormatDuration(elapsed), HumanBytes(int64(avg)))
}

// Fail clears the bar so an error can be printed cleanly.
func (p *Progress) Fail() {
	if tty {
		fmt.Print(clearLine)
	}
}

func bar(frac float64, width int) string {
	frac = min(max(frac, 0), 1)
	full := frac * float64(width)
	whole := int(full)
	parts := []string{" ", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}
	s := strings.Repeat("█", whole)
	if whole < width {
		s += parts[int((full-float64(whole))*8)]
		s += strings.Repeat(" ", width-whole-1)
	}
	return "▕" + s + "▏"
}

// ----------------------------------------------------------------- Spinner

// Spinner shows an animated status line while waiting.
type Spinner struct {
	msg  string
	stop chan struct{}
	wg   sync.WaitGroup
}

func Spin(msg string) *Spinner {
	s := &Spinner{msg: msg, stop: make(chan struct{})}
	if !tty {
		fmt.Println(msg)
		return s
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		start := time.Now()
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Printf("%s%s %s %s", clearLine, frames[i%len(frames)], s.msg,
				dim(FormatDuration(time.Since(start))))
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
		}
	}()
	return s
}

func (s *Spinner) halt() {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
	s.wg.Wait()
	if tty {
		fmt.Print(clearLine)
	}
}

// Success stops the spinner and prints a ✓ line.
func (s *Spinner) Success(msg string) {
	s.halt()
	fmt.Println("✓ " + msg)
}

// Stop stops the spinner without printing anything.
func (s *Spinner) Stop() { s.halt() }

func dim(s string) string {
	if !tty {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}
