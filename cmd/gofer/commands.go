package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NZ-Linix/gofer/internal/config"
	"github.com/NZ-Linix/gofer/internal/p2p"
	"github.com/NZ-Linix/gofer/internal/transfer"
	"github.com/NZ-Linix/gofer/internal/ui"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/peer"
)

// step runs fn behind a spinner and prints done on success.
func step(msg, done string, fn func() error) error {
	sp := ui.Spin(msg)
	if err := fn(); err != nil {
		sp.Stop()
		return err
	}
	sp.Success(done)
	return nil
}

func cmdLogin(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gofer login <username>")
	}
	name := strings.ToLower(args[0])
	if !config.ValidUsername(name) {
		return fmt.Errorf("username must be 3–32 chars: a-z, 0-9, '.', '_', '-'")
	}
	key, err := config.LoadOrCreateKey()
	if err != nil {
		return err
	}
	if err := config.Save(config.Config{Username: name}); err != nil {
		return err
	}
	fmt.Printf("✓ Logged in as %s\n  Peer ID: %s\n", name, config.PeerID(key))
	fmt.Printf("  Friends can pin you with: gofer contacts add %s <peer-id>\n", name)
	return nil
}

func cmdWhoami() error {
	cfg, key, err := config.RequireLogin()
	if err != nil {
		return err
	}
	fmt.Printf("Username: %s\nPeer ID:  %s\n", cfg.Username, config.PeerID(key))
	return nil
}

func cmdContacts(args []string) error {
	c, err := config.LoadContacts()
	if err != nil {
		return err
	}
	switch {
	case len(args) == 0:
		if len(c) == 0 {
			fmt.Println("No contacts yet. They are pinned automatically on first transfer.")
			return nil
		}
		names := make([]string, 0, len(c))
		for n := range c {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Printf("%-20s %s\n", n, c[n])
		}
		return nil
	case len(args) == 3 && args[0] == "add":
		if _, err := peer.Decode(args[2]); err != nil {
			return fmt.Errorf("invalid peer ID: %w", err)
		}
		c[strings.ToLower(args[1])] = args[2]
		return c.Save()
	case len(args) == 2 && (args[0] == "rm" || args[0] == "remove"):
		delete(c, strings.ToLower(args[1]))
		return c.Save()
	}
	return fmt.Errorf("usage: gofer contacts [add <name> <peer-id> | rm <name>]")
}

func cmdReceive(ctx context.Context, args []string) error {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("receive", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join(home, "Downloads"), "where to save received files")
	yes := fs.Bool("yes", false, "accept all incoming files without asking")
	port := fs.Int("port", 0, "fixed TCP/UDP listen port")
	verbose := fs.Bool("v", false, "print network addresses")
	fs.Parse(args)

	cfg, key, err := config.RequireLogin()
	if err != nil {
		return err
	}
	contacts, err := config.LoadContacts()
	if err != nil {
		return err
	}

	n, err := p2p.Start(key, p2p.Options{Port: *port, Reachable: true})
	if err != nil {
		return err
	}
	defer n.Close()

	err = step("Connecting to the peer network…", "Connected", func() error { return n.Bootstrap(ctx) })
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return err
	}

	err = step(fmt.Sprintf("Announcing %q…", cfg.Username), "Announced", func() error {
		// Give the relay reservation a moment so NAT'd senders get a usable address.
		n.WaitForRelay(ctx, 15*time.Second)
		return n.Announce(ctx, cfg.Username)
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "! announce incomplete, will retry:", err)
	}

	r := &transfer.Receiver{Node: n, Username: cfg.Username, Dir: *dir, AutoYes: *yes, Contacts: contacts}
	r.Register()

	if *verbose {
		for _, a := range n.Host.Addrs() {
			fmt.Println("  ", a)
		}
	}
	fmt.Printf("\nReady as %s — others can run: gofer send <file> %s\n", cfg.Username, cfg.Username)
	fmt.Printf("Saving to %s · Ctrl+C to stop\n", *dir)

	sub, err := n.Host.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		return err
	}
	defer sub.Close()

	reannounce := func() { _ = n.Announce(ctx, cfg.Username) }

	// A fresh node's first announce often reaches suboptimal DHT peers; repeat it soon.
	go func() {
		for _, d := range []time.Duration{20 * time.Second, time.Minute, 3 * time.Minute} {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
				reannounce()
			}
		}
	}()

	// Re-announce when our addresses change (new relay, new network) and periodically.
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nOffline.")
			return nil
		case <-sub.Out():
			debounce = time.After(5 * time.Second)
		case <-debounce:
			debounce = nil
			go reannounce()
		case <-ticker.C:
			go reannounce()
		}
	}
}

func cmdSend(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gofer send <file> <username>")
	}
	path, to := args[0], strings.ToLower(args[1])
	if fi, err := os.Stat(path); err != nil {
		return err
	} else if fi.IsDir() {
		return fmt.Errorf("%s is a directory; zip it first", path)
	}

	cfg, key, err := config.RequireLogin()
	if err != nil {
		return err
	}
	contacts, err := config.LoadContacts()
	if err != nil {
		return err
	}
	var pinned peer.ID
	if s, ok := contacts[to]; ok {
		if pinned, err = peer.Decode(s); err != nil {
			return fmt.Errorf("contact %s has invalid peer ID: %w", to, err)
		}
	}

	n, err := p2p.Start(key, p2p.Options{})
	if err != nil {
		return err
	}
	defer n.Close()

	if err := step("Connecting to the peer network…", "Connected", func() error {
		return n.Bootstrap(ctx)
	}); err != nil {
		return err
	}

	var id peer.ID
	if err := step(fmt.Sprintf("Looking for %s…", to), "Found "+to, func() (err error) {
		id, err = transfer.Resolve(ctx, n, to, pinned)
		return err
	}); err != nil {
		return err
	}
	if pinned == "" {
		contacts[to] = id.String()
		if err := contacts.Save(); err == nil {
			fmt.Printf("  Pinned %s → %s\n  (first contact — compare with `gofer whoami` on their side)\n", to, id)
		}
	}

	if err := step("Establishing direct connection…", "Direct connection", func() error {
		return transfer.WaitDirect(ctx, n, id)
	}); err != nil {
		return err
	}
	if err := transfer.Send(ctx, n, id, cfg.Username, to, path); err != nil {
		return err
	}
	fmt.Printf("✓ Delivered %s to %s\n", filepath.Base(path), to)
	return nil
}
