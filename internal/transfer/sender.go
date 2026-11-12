package transfer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NZ-Linix/gofer/internal/p2p"
	"github.com/NZ-Linix/gofer/internal/ui"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
)

// whois asks a peer which username it is logged in as. It may run over a relayed
// connection since only a few bytes are exchanged.
func whois(ctx context.Context, n *p2p.Node, ai peer.AddrInfo) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if len(ai.Addrs) == 0 {
		found, err := n.DHT.FindPeer(ctx, ai.ID)
		if err != nil {
			return "", err
		}
		ai = found
	}
	n.Host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.TempAddrTTL)
	if err := n.Host.Connect(ctx, ai); err != nil {
		return "", err
	}
	s, err := n.Host.NewStream(network.WithAllowLimitedConn(ctx, "whois"), ai.ID, protoWhois)
	if err != nil {
		return "", err
	}
	defer s.Close()
	_ = s.SetReadDeadline(time.Now().Add(20 * time.Second))
	var resp whoisReply
	if err := readLine(bufio.NewReader(s), &resp); err != nil {
		return "", err
	}
	return resp.Username, nil
}

// Resolve finds the peer currently announcing username. If pinned is set, only that
// peer is accepted.
func Resolve(ctx context.Context, n *p2p.Node, username string, pinned peer.ID) (peer.ID, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	providers := n.DHT.FindProvidersAsync(ctx, p2p.UserKey(username), 20)
	type result struct {
		id  peer.ID
		err error
	}
	results := make(chan result, 32)
	pending := 0
	seen := map[peer.ID]bool{}
	var matches []peer.ID
	var lastErr error
	var grace <-chan time.Time

	for providers != nil || pending > 0 {
		select {
		case ai, ok := <-providers:
			if !ok {
				providers = nil
				continue
			}
			if ai.ID == n.Host.ID() || seen[ai.ID] || (pinned != "" && ai.ID != pinned) {
				continue
			}
			seen[ai.ID] = true
			pending++
			go func(ai peer.AddrInfo) {
				name, err := whois(ctx, n, ai)
				if err == nil && name != username {
					err = fmt.Errorf("peer is now logged in as %q", name)
				}
				results <- result{ai.ID, err}
			}(ai)
		case r := <-results:
			pending--
			if r.err != nil {
				lastErr = r.err
				continue
			}
			matches = append(matches, r.id)
			if pinned != "" {
				return r.id, nil
			}
			if grace == nil {
				grace = time.After(5 * time.Second)
			}
		case <-grace:
			providers, pending = nil, 0
		case <-ctx.Done():
			providers, pending = nil, 0
		}
	}

	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		var b strings.Builder
		fmt.Fprintf(&b, "several devices claim the username %q:\n", username)
		for _, m := range matches {
			fmt.Fprintf(&b, "  %s\n", m)
		}
		fmt.Fprintf(&b, "ask your friend for theirs (`gofer whoami`) and pin it with `gofer contacts add %s <peer-id>`", username)
		return "", errors.New(b.String())
	case lastErr != nil:
		return "", fmt.Errorf("%q was found but could not be reached: %v", username, lastErr)
	default:
		return "", fmt.Errorf("%q is not online (they need to run `gofer receive`)", username)
	}
}

// WaitDirect waits until a direct (hole-punched or plain) connection to id exists.
func WaitDirect(ctx context.Context, n *p2p.Node, id peer.ID) error {
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range n.Host.Network().ConnsToPeer(id) {
			if !c.Stat().Limited {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("could not open a direct connection (both networks may have strict/symmetric NATs; try forwarding a port with `gofer receive --port N`)")
}

// Send offers the file at path to peer id and streams it once accepted.
func Send(ctx context.Context, n *p2p.Node, id peer.ID, from, to, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory; zip it first", path)
	}

	s, err := n.Host.NewStream(ctx, id, protoSend)
	if err != nil {
		return err
	}
	defer s.Close()
	br := bufio.NewReader(s)

	if err := writeLine(s, offer{From: from, To: to, Name: filepath.Base(path), Size: fi.Size()}); err != nil {
		return err
	}

	sp := ui.Spin(fmt.Sprintf("Waiting for %s to accept…", to))
	var rep reply
	err = readLine(br, &rep)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("no answer from %s: %w", to, err)
	}
	if !rep.OK {
		return fmt.Errorf("%s: %s", to, rep.Reason)
	}

	h := sha256.New()
	p := ui.NewProgress("Sending  ", fi.Size())
	if _, err := io.CopyN(io.MultiWriter(s, h, p), f, fi.Size()); err != nil {
		p.Fail()
		return err
	}
	p.Done()
	if _, err := s.Write(h.Sum(nil)); err != nil {
		return err
	}
	_ = s.CloseWrite()

	sp = ui.Spin("Waiting for confirmation…")
	_ = s.SetReadDeadline(time.Now().Add(2 * time.Minute))
	err = readLine(br, &rep)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("no confirmation from %s: %w", to, err)
	}
	if !rep.OK {
		return fmt.Errorf("%s: %s", to, rep.Reason)
	}
	return nil
}
