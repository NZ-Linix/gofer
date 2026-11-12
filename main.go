package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/peer"
)

var version = "dev"

const usage = `gofer — send files directly to another device, peer to peer.

Usage:
  gofer login <username>          set your username on this device
  gofer whoami                    show your username and peer ID
  gofer receive [flags]           stay online and receive files
      --dir <path>                  where to save files (default ~/Downloads)
      --yes                         accept every incoming file without asking
      --port <n>                    fixed TCP/UDP port (helps with port forwarding)
  gofer send <file> <username>    send a file to someone running 'gofer receive'
  gofer contacts                  list pinned contacts
  gofer contacts add <name> <id>  pin a username to a peer ID
  gofer contacts rm <name>        remove a contact
  gofer update                    update gofer to the latest release
  gofer version                   show the installed version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cleanupOldBinary()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "login":
		err = cmdLogin(args)
	case "whoami":
		err = cmdWhoami()
	case "receive", "recv":
		err = cmdReceive(ctx, args)
	case "send":
		err = cmdSend(ctx, args)
	case "contacts":
		err = cmdContacts(args)
	case "update", "upgrade":
		err = cmdUpdate()
	case "version", "--version":
		fmt.Println("gofer", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdLogin(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gofer login <username>")
	}
	name := strings.ToLower(args[0])
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("username must be 3–32 chars: a-z, 0-9, '.', '_', '-'")
	}
	key, err := loadOrCreateKey()
	if err != nil {
		return err
	}
	if err := writeJSON("config.json", Config{Username: name}); err != nil {
		return err
	}
	fmt.Printf("Logged in as %s\nPeer ID: %s\n", name, peerIDFromKey(key))
	fmt.Println("Share your peer ID with friends so they can pin you: gofer contacts add", name, "<peer-id>")
	return nil
}

func cmdWhoami() error {
	cfg, key, err := requireLogin()
	if err != nil {
		return err
	}
	fmt.Printf("Username: %s\nPeer ID:  %s\n", cfg.Username, peerIDFromKey(key))
	return nil
}

func cmdContacts(args []string) error {
	c, err := loadContacts()
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
		return c.save()
	case len(args) == 2 && (args[0] == "rm" || args[0] == "remove"):
		delete(c, strings.ToLower(args[1]))
		return c.save()
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

	cfg, key, err := requireLogin()
	if err != nil {
		return err
	}
	contacts, err := loadContacts()
	if err != nil {
		return err
	}

	n, err := startNode(ctx, key, nodeOptions{port: *port, reachable: true})
	if err != nil {
		return err
	}
	defer n.Close()

	r := &receiver{node: n, username: cfg.Username, dir: *dir, autoYes: *yes,
		contacts: contacts, stdin: bufio.NewReader(os.Stdin)}
	r.register()

	fmt.Println("Connecting to the peer network…")
	if err := n.Bootstrap(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	fmt.Printf("✓ Online as %s (peer %s)\n  Saving to %s — Ctrl+C to stop.\n", cfg.Username, n.Host.ID(), *dir)

	sub, err := n.Host.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		return err
	}
	defer sub.Close()

	announce := func() error {
		// The DHT walk rarely "completes" (many public peers are unreachable); on deadline it
		// still stores the record with the closest peers found so far, which is what we want.
		c, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		err := n.DHT.Provide(c, userKey(cfg.Username), true)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil
		}
		return err
	}

	go func() {
		fmt.Printf("  Announcing %q so others can find you…\n", cfg.Username)
		// Give the relay reservation a moment so NAT'd senders get a usable address.
		for i := 0; i < 60 && !hasRelayAddr(n.Host) && ctx.Err() == nil; i++ {
			time.Sleep(250 * time.Millisecond)
		}
		err := announce()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "  warning: announce incomplete, retrying shortly:", err)
		} else {
			fmt.Println("  ✓ Findable — others can now `gofer send <file>", cfg.Username+"`")
		}
		if *verbose {
			for _, a := range n.Host.Addrs() {
				fmt.Println("    ", a)
			}
		}
		// A fresh node's first announce often reaches suboptimal DHT peers; repeat it soon.
		for _, d := range []time.Duration{20 * time.Second, time.Minute, 3 * time.Minute} {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
				announce()
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
			go announce()
		case <-ticker.C:
			go announce()
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

	cfg, key, err := requireLogin()
	if err != nil {
		return err
	}
	contacts, err := loadContacts()
	if err != nil {
		return err
	}
	var pinned peer.ID
	if s, ok := contacts[to]; ok {
		if pinned, err = peer.Decode(s); err != nil {
			return fmt.Errorf("contact %s has invalid peer ID: %w", to, err)
		}
	}

	n, err := startNode(ctx, key, nodeOptions{})
	if err != nil {
		return err
	}
	defer n.Close()

	fmt.Println("Connecting to the peer network…")
	if err := n.Bootstrap(ctx); err != nil {
		return err
	}
	fmt.Printf("Looking for %s…\n", to)
	id, err := resolve(ctx, n, to, pinned)
	if err != nil {
		return err
	}
	if pinned == "" {
		contacts[to] = id.String()
		if err := contacts.save(); err == nil {
			fmt.Printf("Pinned %s → %s (first contact; verify with `gofer whoami` on their side)\n", to, id)
		}
	}

	fmt.Println("Establishing direct connection…")
	if err := waitDirect(ctx, n, id); err != nil {
		return err
	}
	if err := sendFile(ctx, n, id, cfg.Username, to, path); err != nil {
		return err
	}
	fmt.Printf("✓ Delivered %s to %s\n", filepath.Base(path), to)
	return nil
}
