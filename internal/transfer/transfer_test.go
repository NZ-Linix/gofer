package transfer

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"file.zip":            "file.zip",
		"../../etc/passwd":    "passwd",
		"/abs/path/photo.jpg": "photo.jpg",
		`..\..\windows\x.exe`: "x.exe",
		"..":                  "",
		".":                   "",
		"":                    "",
		"dir/":                "dir",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniquePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if got := uniquePath(p); got != p {
		t.Fatalf("got %q, want %q", got, p)
	}
	os.WriteFile(p, nil, 0o644)
	if got, want := uniquePath(p), filepath.Join(dir, "a (1).txt"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	os.WriteFile(filepath.Join(dir, "a (1).txt"), nil, 0o644)
	if got, want := uniquePath(p), filepath.Join(dir, "a (2).txt"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLineRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := offer{From: "alice", To: "bob", Name: "x.zip", Size: 42}
	if err := writeLine(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out offer
	if err := readLine(bufio.NewReader(&buf), &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

func TestReadLineTooLarge(t *testing.T) {
	big := strings.Repeat("x", maxLine+10) + "\n"
	var v any
	if err := readLine(bufio.NewReader(strings.NewReader(big)), &v); err == nil {
		t.Fatal("expected error for oversized line")
	}
}
