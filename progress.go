package main

import (
	"fmt"
	"strings"
	"time"
)

type progress struct {
	label       string
	total, n    int64
	start, last time.Time
}

func newProgress(label string, total int64) *progress {
	return &progress{label: label, total: total, start: time.Now()}
}

func (p *progress) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if time.Since(p.last) > 200*time.Millisecond {
		p.last = time.Now()
		p.print()
	}
	return len(b), nil
}

func (p *progress) done() {
	p.print()
	fmt.Println()
}

func (p *progress) print() {
	pct := 100.0
	if p.total > 0 {
		pct = float64(p.n) / float64(p.total) * 100
	}
	const width = 30
	filled := int(pct / 100 * width)
	elapsed := time.Since(p.start).Seconds()
	speed := 0.0
	if elapsed > 0 {
		speed = float64(p.n) / elapsed
	}
	fmt.Printf("\r%s [%s%s] %5.1f%%  %s / %s  %s/s   ",
		p.label, strings.Repeat("█", filled), strings.Repeat("░", width-filled),
		pct, humanBytes(p.n), humanBytes(p.total), humanBytes(int64(speed)))
}

func humanBytes(n int64) string {
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
