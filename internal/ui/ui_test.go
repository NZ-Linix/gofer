package ui

import (
	"testing"
	"time"
	"unicode/utf8"
)

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:               "0 B",
		1023:            "1023 B",
		1024:            "1.0 KiB",
		1536:            "1.5 KiB",
		5 * 1024 * 1024: "5.0 MiB",
		3 << 30:         "3.0 GiB",
	}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                "0:00",
		59 * time.Second: "0:59",
		61 * time.Second: "1:01",
		time.Hour + 2*time.Minute + 3*time.Second: "1:02:03",
	}
	for in, want := range cases {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBarWidth(t *testing.T) {
	for _, f := range []float64{-1, 0, 0.01, 0.5, 0.999, 1, 2} {
		if n := utf8.RuneCountInString(bar(f, 24)); n != 26 {
			t.Errorf("bar(%v) has %d runes, want 26", f, n)
		}
	}
}
