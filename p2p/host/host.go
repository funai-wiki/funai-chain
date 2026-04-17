package host

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	// P3-4: IP rate limit — 100 messages per second per peer
	peerRateLimit   = 100
	peerRateResetMs = 1000
)

// Host wraps a libp2p host with FunAI-specific topic management.
type Host struct {
	host   host.Host
	ps     *pubsub.PubSub
	topics map[string]*pubsub.Topic
	subs   map[string]*pubsub.Subscription
	mu     sync.RWMutex

	// P3-4: per-peer message rate limiting
	peerRates  map[peer.ID]*peerRateEntry
	peerRateMu sync.Mutex
}

// peerRateEntry tracks message counts per peer for rate limiting.
type peerRateEntry struct {
	count   int
	resetAt time.Time
}

// New creates a new P2P host listening on the given address.
func New(listenAddr string) (*Host, error) {
	addr, err := ma.NewMultiaddr(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("parse listen addr: %w", err)
	}

	h, err := libp2p.New(libp2p.ListenAddrs(addr))
	if err != nil {
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	// N5: configure peer scoring to defend against message floods.
	// Malicious peers sending excessive messages get penalized and eventually disconnected.
	peerScoreParams := &pubsub.PeerScoreParams{
		AppSpecificScore:            func(p peer.ID) float64 { return 0 },
		DecayInterval:               time.Minute,
		DecayToZero:                 0.01,
		IPColocationFactorWeight:    -10,
		IPColocationFactorThreshold: 3,
		BehaviourPenaltyWeight:      -1,
		BehaviourPenaltyDecay:       0.99,
	}
	peerScoreThresholds := &pubsub.PeerScoreThresholds{
		GossipThreshold:             -100,
		PublishThreshold:            -500,
		GraylistThreshold:           -1000,
		OpportunisticGraftThreshold: 5,
	}
	ps, err := pubsub.NewGossipSub(context.Background(), h,
		pubsub.WithPeerScore(peerScoreParams, peerScoreThresholds),
		pubsub.WithFloodPublish(true),
	)
	if err != nil {
		h.Close()
		return nil, fmt.Errorf("create gossipsub: %w", err)
	}

	return &Host{
		host:      h,
		ps:        ps,
		topics:    make(map[string]*pubsub.Topic),
		subs:      make(map[string]*pubsub.Subscription),
		peerRates: make(map[peer.ID]*peerRateEntry),
	}, nil
}

// ID returns the peer ID of this host.
func (h *Host) ID() peer.ID {
	return h.host.ID()
}

// Addrs returns the listen addresses.
func (h *Host) Addrs() []ma.Multiaddr {
	return h.host.Addrs()
}

// ModelTopic returns the topic name for a model.
func ModelTopic(modelId string) string {
	return fmt.Sprintf("/funai/model/%s", modelId)
}

// SettlementTopic is the global settlement evidence topic.
const SettlementTopic = "/funai/settlement"

// JoinTopic joins (or returns existing) a pubsub topic.
func (h *Host) JoinTopic(topicName string) (*pubsub.Topic, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if t, ok := h.topics[topicName]; ok {
		return t, nil
	}

	// P2-2: register per-topic validator for peer rate limiting before joining.
	// CheckPeerRate is called on every inbound message, dropping from flooding peers.
	_ = h.ps.RegisterTopicValidator(topicName,
		func(_ context.Context, pid peer.ID, _ *pubsub.Message) bool {
			return h.CheckPeerRate(pid)
		},
	)

	t, err := h.ps.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("join topic %s: %w", topicName, err)
	}

	h.topics[topicName] = t
	return t, nil
}

// Subscribe subscribes to a topic and returns the subscription.
func (h *Host) Subscribe(topicName string) (*pubsub.Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if s, ok := h.subs[topicName]; ok {
		return s, nil
	}

	t, ok := h.topics[topicName]
	if !ok {
		// P2-1: register rate limit validator before joining (same as JoinTopic)
		_ = h.ps.RegisterTopicValidator(topicName,
			func(_ context.Context, pid peer.ID, _ *pubsub.Message) bool {
				return h.CheckPeerRate(pid)
			},
		)
		var err error
		t, err = h.ps.Join(topicName)
		if err != nil {
			return nil, err
		}
		h.topics[topicName] = t
	}

	sub, err := t.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", topicName, err)
	}

	h.subs[topicName] = sub
	return sub, nil
}

// Publish publishes data to a topic.
func (h *Host) Publish(ctx context.Context, topicName string, data []byte) error {
	h.mu.RLock()
	t, ok := h.topics[topicName]
	h.mu.RUnlock()

	if !ok {
		var err error
		t, err = h.JoinTopic(topicName)
		if err != nil {
			return err
		}
	}

	return t.Publish(ctx, data)
}

// ConnectPeer connects to a bootstrap peer.
func (h *Host) ConnectPeer(ctx context.Context, addr string) error {
	maddr, err := ma.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("parse peer addr: %w", err)
	}

	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("parse peer info: %w", err)
	}

	return h.host.Connect(ctx, *peerInfo)
}

// StartMDNS starts mDNS peer discovery for local network testing.
func (h *Host) StartMDNS(serviceTag string) error {
	notifee := &mdnsNotifee{host: h.host}
	service := mdns.NewMdnsService(h.host, serviceTag, notifee)
	return service.Start()
}

type mdnsNotifee struct {
	host host.Host
}

func (n *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	_ = n.host.Connect(context.Background(), pi)
}

// CheckPeerRate checks whether a peer has exceeded the per-IP rate limit (P3-4).
// Returns true if the message should be allowed.
func (h *Host) CheckPeerRate(pid peer.ID) bool {
	h.peerRateMu.Lock()
	defer h.peerRateMu.Unlock()

	now := time.Now()
	entry, ok := h.peerRates[pid]
	if !ok || now.After(entry.resetAt) {
		h.peerRates[pid] = &peerRateEntry{
			count:   1,
			resetAt: now.Add(peerRateResetMs * time.Millisecond),
		}
		return true
	}

	entry.count++
	return entry.count <= peerRateLimit
}

// ConnectedPeers returns the number of currently connected libp2p peers.
func (h *Host) ConnectedPeers() int {
	return len(h.host.Network().Peers())
}

// Close shuts down the host and all topics.
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, sub := range h.subs {
		sub.Cancel()
	}
	for _, t := range h.topics {
		t.Close()
	}

	return h.host.Close()
}
