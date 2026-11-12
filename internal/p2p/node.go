// Package p2p wraps the libp2p host and the public DHT used for discovery.
package p2p

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	mh "github.com/multiformats/go-multihash"
)

type Node struct {
	Host host.Host
	DHT  *dht.IpfsDHT
}

type Options struct {
	Port int
	// Reachable makes the node obtain relay reservations so peers behind NAT can
	// reach it for hole punching. Used by receivers.
	Reachable bool
}

func Start(key crypto.PrivKey, o Options) (*Node, error) {
	var (
		mu  sync.Mutex
		kad *dht.IpfsDHT
	)
	// Relay candidates come from the public DHT routing table. Relays are only used to
	// coordinate the hole punch; file data always goes over a direct connection.
	peerSource := func(ctx context.Context, num int) <-chan peer.AddrInfo {
		out := make(chan peer.AddrInfo, num)
		go func() {
			defer close(out)
			mu.Lock()
			d := kad
			mu.Unlock()
			if d == nil {
				return
			}
			for _, p := range d.RoutingTable().ListPeers() {
				if num <= 0 {
					return
				}
				select {
				case out <- d.Host().Peerstore().PeerInfo(p):
					num--
				case <-ctx.Done():
					return
				}
			}
		}()
		return out
	}

	opts := []libp2p.Option{
		libp2p.Identity(key),
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", o.Port),
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", o.Port),
			fmt.Sprintf("/ip6/::/tcp/%d", o.Port),
			fmt.Sprintf("/ip6/::/udp/%d/quic-v1", o.Port),
		),
		libp2p.NATPortMap(),
		libp2p.EnableHolePunching(),
	}
	if o.Reachable {
		opts = append(opts,
			libp2p.ForceReachabilityPrivate(),
			libp2p.EnableAutoRelayWithPeerSource(peerSource,
				autorelay.WithBootDelay(0),
				autorelay.WithMinInterval(5*time.Second)),
		)
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}
	d, err := dht.New(h,
		dht.Mode(dht.ModeClient),
		dht.EnableOptimisticProvide(),
		dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
	if err != nil {
		h.Close()
		return nil, err
	}
	mu.Lock()
	kad = d
	mu.Unlock()
	return &Node{Host: h, DHT: d}, nil
}

func (n *Node) Close() {
	n.DHT.Close()
	n.Host.Close()
}

// Bootstrap connects to the public DHT and waits until the routing table is usable.
func (n *Node) Bootstrap(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, ai := range dht.GetDefaultBootstrapPeerAddrInfos() {
		wg.Add(1)
		go func(ai peer.AddrInfo) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_ = n.Host.Connect(c, ai)
		}(ai)
	}
	wg.Wait()
	if err := n.DHT.Bootstrap(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for n.DHT.RoutingTable().Size() < 5 {
		if time.Now().After(deadline) {
			if n.DHT.RoutingTable().Size() > 0 {
				return nil
			}
			return fmt.Errorf("could not join the peer network (no internet connection?)")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

// Announce publishes that this node answers for username.
func (n *Node) Announce(ctx context.Context, username string) error {
	// The DHT walk rarely "completes" (many public peers are unreachable); on deadline it
	// still stores the record with the closest peers found so far, which is what we want.
	c, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	err := n.DHT.Provide(c, UserKey(username), true)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil
	}
	return err
}

// WaitForRelay waits up to max for a relay address to appear.
func (n *Node) WaitForRelay(ctx context.Context, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) && !n.hasRelayAddr() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (n *Node) hasRelayAddr() bool {
	for _, a := range n.Host.Addrs() {
		if strings.Contains(a.String(), "/p2p-circuit") {
			return true
		}
	}
	return false
}

// UserKey is the DHT key under which a username is announced.
func UserKey(username string) cid.Cid {
	sum := sha256.Sum256([]byte("gofer/user/v1/" + username))
	m, _ := mh.Encode(sum[:], mh.SHA2_256)
	return cid.NewCidV1(cid.Raw, m)
}
