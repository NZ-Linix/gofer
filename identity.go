package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)

type Config struct {
	Username string `json:"username"`
}

// goferDir returns the state directory (~/.gofer, or $GOFER_HOME).
func goferDir() (string, error) {
	if d := os.Getenv("GOFER_HOME"); d != "" {
		return d, os.MkdirAll(d, 0o700)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".gofer")
	return d, os.MkdirAll(d, 0o700)
}

// loadOrCreateKey returns this device's persistent Ed25519 identity.
func loadOrCreateKey() (crypto.PrivKey, error) {
	dir, err := goferDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "identity.key")
	data, err := os.ReadFile(path)
	if err == nil {
		return crypto.UnmarshalPrivateKey(data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return nil, err
	}
	data, err = crypto.MarshalPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return key, os.WriteFile(path, data, 0o600)
}

func peerIDFromKey(key crypto.PrivKey) peer.ID {
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		panic(err)
	}
	return id
}

func readJSON(name string, v any) error {
	dir, err := goferDir()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(name string, v any) error {
	dir, err := goferDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), data, 0o600)
}

func loadConfig() (Config, error) {
	var c Config
	err := readJSON("config.json", &c)
	return c, err
}

func requireLogin() (Config, crypto.PrivKey, error) {
	cfg, err := loadConfig()
	if err != nil {
		return cfg, nil, err
	}
	if cfg.Username == "" {
		return cfg, nil, fmt.Errorf("not logged in: run `gofer login <username>` first")
	}
	key, err := loadOrCreateKey()
	return cfg, key, err
}

// Contacts pin usernames to peer IDs (trust on first use).
type Contacts map[string]string

func loadContacts() (Contacts, error) {
	c := Contacts{}
	err := readJSON("contacts.json", &c)
	return c, err
}

func (c Contacts) save() error { return writeJSON("contacts.json", c) }

func (c Contacts) nameFor(id peer.ID) string {
	for name, pid := range c {
		if pid == id.String() {
			return name
		}
	}
	return ""
}
