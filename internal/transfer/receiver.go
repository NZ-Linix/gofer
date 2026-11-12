package transfer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NZ-Linix/gofer/internal/config"
	"github.com/NZ-Linix/gofer/internal/p2p"
	"github.com/NZ-Linix/gofer/internal/ui"
	"github.com/libp2p/go-libp2p/core/network"
)

// Receiver accepts incoming files for one username.
type Receiver struct {
	Node     *p2p.Node
	Username string
	Dir      string
	AutoYes  bool
	Contacts config.Contacts

	mu    sync.Mutex // serializes prompts and progress output
	stdin *bufio.Reader
}

// Register installs the stream handlers on the node.
func (r *Receiver) Register() {
	r.stdin = bufio.NewReader(os.Stdin)
	r.Node.Host.SetStreamHandler(protoWhois, func(s network.Stream) {
		defer s.Close()
		_ = writeLine(s, whoisReply{Username: r.Username})
	})
	r.Node.Host.SetStreamHandler(protoSend, r.handleSend)
}

func (r *Receiver) handleSend(s network.Stream) {
	defer s.Close()
	remote := s.Conn().RemotePeer()
	_ = s.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(s)

	var o offer
	if err := readLine(br, &o); err != nil {
		s.Reset()
		return
	}
	reject := func(reason string) { _ = writeLine(s, reply{Reason: reason}) }

	name := safeName(o.Name)
	switch {
	case o.To != r.Username:
		reject("this device is not " + o.To)
		return
	case name == "" || o.Size < 0:
		reject("invalid file")
		return
	}
	_ = s.SetReadDeadline(time.Time{})

	r.mu.Lock()
	defer r.mu.Unlock()

	who := fmt.Sprintf("%q (unverified, peer %s)", o.From, remote)
	if c := r.Contacts.NameFor(remote); c != "" {
		who = c + " (known contact)"
	}
	fmt.Printf("\n📨 %s wants to send %s (%s)\n", who, name, ui.HumanBytes(o.Size))

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
		fmt.Printf("✗ transfer failed: %v\n", err)
		_ = writeLine(s, reply{Reason: err.Error()})
		s.Reset()
		return
	}
	_ = writeLine(s, reply{OK: true})
	fmt.Printf("✓ Saved to %s\n", dest)

	if o.From != "" && r.Contacts.NameFor(remote) == "" {
		if _, taken := r.Contacts[o.From]; !taken && config.ValidUsername(o.From) {
			r.Contacts[o.From] = remote.String()
			_ = r.Contacts.Save()
		}
	}
}

func (r *Receiver) confirm() bool {
	if r.AutoYes {
		return true
	}
	if !ui.IsTerminal(os.Stdin) {
		fmt.Println("   (no terminal to confirm; run with --yes to auto-accept)")
		return false
	}
	fmt.Print("   Accept? [y/N] ")
	line, _ := r.stdin.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func (r *Receiver) receiveFile(br *bufio.Reader, name string, size int64) (string, error) {
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(r.Dir, "."+name+".*.gofer-part")
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
	p := ui.NewProgress("Receiving", size)
	if _, err := io.CopyN(io.MultiWriter(f, h, p), br, size); err != nil {
		p.Fail()
		return "", fmt.Errorf("connection lost: %w", err)
	}
	p.Done()
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
	dest := uniquePath(filepath.Join(r.Dir, name))
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	ok = true
	return dest, nil
}

// uniquePath returns p, or "name (n).ext" if p already exists.
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
