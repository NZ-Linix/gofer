package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
)

const (
	protoWhois = "/gofer/whois/1.0.0"
	protoSend  = "/gofer/send/1.0.0"
)

type offer struct {
	From string `json:"from"`
	To   string `json:"to"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type reply struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func readLine(r *bufio.Reader, v any) error {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return err
		}
		buf = append(buf, chunk...)
		if len(buf) > 64<<10 {
			return errors.New("message too large")
		}
		if !isPrefix {
			break
		}
	}
	return json.Unmarshal(buf, v)
}

// ---------------------------------------------------------------- receiver

type receiver struct {
	node     *Node
	username string
	dir      string
	autoYes  bool
	contacts Contacts

	promptMu sync.Mutex
	stdin    *bufio.Reader
}

func (r *receiver) register() {
	r.node.Host.SetStreamHandler(protoWhois, func(s network.Stream) {
		defer s.Close()
		_ = writeLine(s, map[string]string{"username": r.username})
	})
	r.node.Host.SetStreamHandler(protoSend, r.handleSend)
}

func (r *receiver) handleSend(s network.Stream) {
	defer s.Close()
	remote := s.Conn().RemotePeer()
	_ = s.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(s)

	var o offer
	if err := readLine(br, &o); err != nil {
		s.Reset()
		return
	}
	reject := func(reason string) {
		_ = writeLine(s, reply{Reason: reason})
	}
	name := filepath.Base(filepath.Clean("/" + o.Name))
	if o.To != r.username {
		reject("this device is not " + o.To)
		return
	}
	if name == "/" || name == "." || name == ".." || o.Size < 0 {
		reject("invalid file")
		return
	}

	who := fmt.Sprintf("%q (unverified, peer %s)", o.From, remote)
	if c := r.contacts.nameFor(remote); c != "" {
		who = fmt.Sprintf("%s (known contact)", c)
	}
	fmt.Printf("\n📨 %s wants to send %s (%s)\n", who, name, humanBytes(o.Size))

	_ = s.SetReadDeadline(time.Time{})
	if !r.confirm() {
		fmt.Println("   declined")
		reject("declined by recipient")
		return
	}
	if err := writeLine(s, reply{OK: true}); err != nil {
		return
	}

	dest, err := r.receiveFile(br, name, o.Size)
	if err != nil {
		fmt.Printf("\n   ✗ transfer failed: %v\n", err)
		_ = writeLine(s, reply{Reason: err.Error()})
		s.Reset()
		return
	}
	_ = writeLine(s, reply{OK: true})
	fmt.Printf("   ✓ saved to %s\n", dest)
	if o.From != "" && r.contacts.nameFor(remote) == "" {
		if _, taken := r.contacts[o.From]; !taken && usernameRe.MatchString(o.From) {
			r.contacts[o.From] = remote.String()
			_ = r.contacts.save()
		}
	}
}

func (r *receiver) confirm() bool {
	if r.autoYes {
		return true
	}
	r.promptMu.Lock()
	defer r.promptMu.Unlock()
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		fmt.Println("   (no terminal to confirm; run with --yes to auto-accept)")
		return false
	}
	fmt.Print("   Accept? [y/N] ")
	line, _ := r.stdin.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func (r *receiver) receiveFile(br *bufio.Reader, name string, size int64) (string, error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(r.dir, "."+name+".*.gofer-part")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()

	h := sha256.New()
	p := newProgress("   receiving", size)
	if _, err := io.CopyN(io.MultiWriter(f, h, p), br, size); err != nil {
		return "", fmt.Errorf("connection lost: %w", err)
	}
	p.done()
	sum := make([]byte, sha256.Size)
	if _, err := io.ReadFull(br, sum); err != nil {
		return "", fmt.Errorf("connection lost: %w", err)
	}
	if !bytes.Equal(sum, h.Sum(nil)) {
		return "", errors.New("checksum mismatch")
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	dest := uniquePath(filepath.Join(r.dir, name))
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	ok = true
	return dest, nil
}

func uniquePath(p string) string {
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(c); errors.Is(err, os.ErrNotExist) {
			return c
		}
	}
}

// ------------------------------------------------------------------ sender

// whois asks a peer which username it is logged in as. It may run over a relayed
// connection since only a few bytes are exchanged.
func whois(ctx context.Context, n *Node, ai peer.AddrInfo) (string, error) {
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
	var resp struct {
		Username string `json:"username"`
	}
	if err := readLine(bufio.NewReader(s), &resp); err != nil {
		return "", err
	}
	return resp.Username, nil
}

// resolve finds the peer currently announcing `username`. If pinned is set, only
// that peer is accepted.
func resolve(ctx context.Context, n *Node, username string, pinned peer.ID) (peer.ID, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	providers := n.DHT.FindProvidersAsync(ctx, userKey(username), 20)
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

	for {
		if providers == nil && pending == 0 {
			break
		}
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

// waitDirect waits until a direct (hole-punched or plain) connection exists.
func waitDirect(ctx context.Context, n *Node, id peer.ID) error {
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

func sendFile(ctx context.Context, n *Node, id peer.ID, from, to, path string) error {
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
	fmt.Printf("Waiting for %s to accept…\n", to)
	var rep reply
	if err := readLine(br, &rep); err != nil {
		return fmt.Errorf("no answer from %s: %w", to, err)
	}
	if !rep.OK {
		return fmt.Errorf("%s: %s", to, rep.Reason)
	}

	h := sha256.New()
	p := newProgress("Sending", fi.Size())
	if _, err := io.CopyN(io.MultiWriter(s, h, p), f, fi.Size()); err != nil {
		return err
	}
	p.done()
	if _, err := s.Write(h.Sum(nil)); err != nil {
		return err
	}
	_ = s.CloseWrite()
	_ = s.SetReadDeadline(time.Now().Add(2 * time.Minute))
	if err := readLine(br, &rep); err != nil {
		return fmt.Errorf("no confirmation from %s: %w", to, err)
	}
	if !rep.OK {
		return fmt.Errorf("%s: %s", to, rep.Reason)
	}
	return nil
}
