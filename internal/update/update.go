// Package update implements self-updating from GitHub releases.
package update

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NZ-Linix/gofer/internal/ui"
)

const Repo = "NZ-Linix/gofer"

var client = &http.Client{Timeout: 5 * time.Minute}

// Run updates the running binary to the latest release if it differs from current.
func Run(current string) error {
	sp := ui.Spin("Checking for updates…")
	latest, err := latestVersion()
	sp.Stop()
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if latest == current {
		fmt.Printf("✓ gofer %s is already the latest version.\n", current)
		return nil
	}

	asset := fmt.Sprintf("gofer_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", Repo, latest)

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}

	expected, err := checksumFor(base+"checksums.txt", asset)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".gofer-update-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("no permission to write %s; try: sudo gofer update", filepath.Dir(exe))
		}
		return err
	}
	defer os.Remove(tmp.Name())

	fmt.Printf("Updating gofer %s → %s\n", current, latest)
	h := sha256.New()
	if err := download(base+asset, tmp, h); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("checksum verification failed; aborting")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}

	// Windows can't overwrite a running .exe, but it can rename it out of the way.
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return err
	}
	fmt.Printf("✓ Updated to %s\n", latest)
	return nil
}

// CleanupOldBinary removes the leftover from a previous Windows self-update.
func CleanupOldBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}

func latestVersion() (string, error) {
	resp, err := client.Get("https://api.github.com/repos/" + Repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	return rel.TagName, nil
}

func checksumFor(url, asset string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download checksums: %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("no build for %s/%s in this release", runtime.GOOS, runtime.GOARCH)
}

func download(url string, dst ...io.Writer) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	p := ui.NewProgress("Downloading", resp.ContentLength)
	if _, err := io.Copy(io.MultiWriter(append(dst, p)...), resp.Body); err != nil {
		p.Fail()
		return err
	}
	p.Done()
	return nil
}
