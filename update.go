package main

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
)

const repo = "NZ-Linix/gofer"

var httpClient = &http.Client{Timeout: 5 * time.Minute}

func latestVersion() (string, error) {
	resp, err := httpClient.Get("https://api.github.com/repos/" + repo + "/releases/latest")
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

func download(url string, w io.Writer) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func cmdUpdate() error {
	latest, err := latestVersion()
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if latest == version {
		fmt.Printf("gofer %s is already the latest version.\n", version)
		return nil
	}

	asset := fmt.Sprintf("gofer_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", repo, latest)

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	dir := filepath.Dir(exe)

	var sums strings.Builder
	if err := download(base+"checksums.txt", &sums); err != nil {
		return err
	}
	expected := ""
	sc := bufio.NewScanner(strings.NewReader(sums.String()))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == asset {
			expected = f[0]
		}
	}
	if expected == "" {
		return fmt.Errorf("no build for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, latest)
	}

	fmt.Printf("Updating gofer %s → %s…\n", version, latest)
	tmp, err := os.CreateTemp(dir, ".gofer-update-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("no permission to write %s; try: sudo gofer update", dir)
		}
		return err
	}
	defer os.Remove(tmp.Name())

	h := sha256.New()
	if err := download(base+asset, io.MultiWriter(tmp, h)); err != nil {
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

// cleanupOldBinary removes the leftover from a previous Windows self-update.
func cleanupOldBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
