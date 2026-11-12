package main

import (
	"context"
	"crypto/sha256"
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

type nodeOptions struct {
	port      int
	reachable bool // receiver: obtain relay reservations so NAT'd peers can be reached for hole punching
}

func startNode(ctx context.Context, key crypto.PrivKey, o nodeOptions) (*Node, error) {
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
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", o.port),
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", o.port),
			fmt.Sprintf("/ip6/::/tcp/%d", o.port),
			fmt.Sprintf("/ip6/::/udp/%d/quic-v1", o.port),
		),
		libp2p.NATPortMap(),
		libp2p.EnableHolePunching(),
	}
	if o.reachable {
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

// userKey is the DHT key under which a username is announced.
func userKey(username string) cid.Cid {
	sum := sha256.Sum256([]byte("gofer/user/v1/" + username))
	m, _ := mh.Encode(sum[:], mh.SHA2_256)
	return cid.NewCidV1(cid.Raw, m)
}

func hasRelayAddr(h host.Host) bool {
	for _, a := range h.Addrs() {
		if strings.Contains(a.String(), "/p2p-circuit") {
			return true
		}
	}
	return false
}

func shortID(id peer.ID) string {
	s := id.String()
	if len(s) > 12 {
		return "…" + s[len(s)-8:]
	}
	return s
}
