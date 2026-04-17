package p2p

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/cometbft/cometbft/crypto/secp256k1"

	"github.com/funai-wiki/funai-chain/p2p/chain"
	p2phost "github.com/funai-wiki/funai-chain/p2p/host"
	"github.com/funai-wiki/funai-chain/p2p/inference"
	"github.com/funai-wiki/funai-chain/p2p/leader"
	"github.com/funai-wiki/funai-chain/p2p/proposer"
	p2pstore "github.com/funai-wiki/funai-chain/p2p/store"
	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	"github.com/funai-wiki/funai-chain/p2p/verifier"
	"github.com/funai-wiki/funai-chain/p2p/worker"
	"github.com/funai-wiki/funai-chain/sdk/privacy"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"
)

// Config holds the P2P node configuration.
type Config struct {
	ListenAddr         string        `json:"listen_addr" toml:"listen_addr"`
	BootPeers          []string      `json:"boot_peers" toml:"boot_peers"`
	ChainRPC           string        `json:"chain_rpc" toml:"chain_rpc"`
	ChainREST          string        `json:"chain_rest" toml:"chain_rest"`
	TGIEndpoint        string        `json:"tgi_endpoint" toml:"tgi_endpoint"`
	TGIToken           string        `json:"tgi_token" toml:"tgi_token"` // Bearer token for remote TGI auth
	WorkerAddr         string        `json:"worker_addr" toml:"worker_addr"`
	WorkerPubkey       []byte        `json:"worker_pubkey" toml:"worker_pubkey"`
	WorkerPrivKey      []byte        `json:"worker_privkey" toml:"worker_privkey"` // S6: for signing receipts
	ModelIds           []string      `json:"model_ids" toml:"model_ids"`
	Epsilon            float32       `json:"epsilon" toml:"epsilon"`
	AuditRate          uint32        `json:"audit_rate" toml:"audit_rate"`
	BatchSize          int           `json:"batch_size" toml:"batch_size"`
	EncryptionPubkey   []byte        `json:"encryption_pubkey" toml:"encryption_pubkey"`       // X25519 pubkey for TLS (§19)
	EncryptionPrivkey  []byte        `json:"encryption_privkey" toml:"encryption_privkey"`     // X25519 privkey for TLS (§19)
	ChainID            string        `json:"chain_id" toml:"chain_id"`                         // C1: chain ID for tx signing
	BatchInterval      time.Duration `json:"batch_interval" toml:"batch_interval"`             // C1: batch settlement interval
	MaxConcurrentTasks uint32        `json:"max_concurrent_tasks" toml:"max_concurrent_tasks"` // G3: inference concurrency limit
	InferenceBackend   string        `json:"inference_backend" toml:"inference_backend"`       // "tgi" (default), "openai", "ollama", "vllm", "sglang"
	InferenceModel     string        `json:"inference_model" toml:"inference_model"`           // Model name for OpenAI-compatible backends
}

func defaultChainID() string {
	if env := os.Getenv("FUNAI_CHAIN_ID"); env != "" {
		return env
	}
	return "funai_123123123-3"
}

// DefaultConfig returns a development configuration.
func DefaultConfig() Config {
	return Config{
		ListenAddr:    "/ip4/0.0.0.0/tcp/4001",
		ChainRPC:      "http://localhost:26657",
		ChainREST:     "http://localhost:1317",
		TGIEndpoint:   "http://localhost:8080",
		Epsilon:       0.01,
		AuditRate:     100, // 10%
		BatchSize:     100,
		ChainID:       defaultChainID(),
		BatchInterval: 5 * time.Second,
	}
}

// Node is the FunAI P2P node that runs all Layer 2 roles.
type Node struct {
	Config            Config
	Host              *p2phost.Host
	Chain             *chain.Client
	TGI               *inference.TGIClient      // kept for backward compat; prefer Engine
	Engine            inference.Engine          // unified inference engine
	Leaders           map[string]*leader.Leader // per model_id
	Worker            *worker.Worker
	Verifier          *verifier.Verifier
	Proposer          *proposer.Proposer
	Store             *p2pstore.Store // P2-5: 7-day data retention
	EncryptionPubkey  []byte
	EncryptionPrivkey []byte
	TLSTransport      privacy.Transport       // S6: decryption layer for incoming encrypted messages
	cachedWorkers     []vrftypes.RankedWorker // cached worker list for VRF dispatch
	cachedWorkersMu   sync.RWMutex
}

// NewNode creates and initializes a P2P node with all role modules.
func NewNode(cfg Config) (*Node, error) {
	// Create P2P host
	host, err := p2phost.New(cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("create P2P host: %w", err)
	}

	// Create chain client
	chainClient := chain.NewClient(cfg.ChainRPC, cfg.ChainREST)

	// Create inference engine based on backend config
	var engine inference.Engine
	var tgiClient *inference.TGIClient

	switch cfg.InferenceBackend {
	case "openai", "vllm", "sglang", "ollama":
		backend := cfg.InferenceBackend
		if backend == "openai" {
			backend = "vllm" // generic OpenAI-compatible
		}
		engine = inference.NewOpenAIClient(cfg.TGIEndpoint, cfg.InferenceModel, backend)
		engine.DetectVersion()
		log.Printf("Inference engine: %s (model=%s, endpoint=%s)", backend, cfg.InferenceModel, cfg.TGIEndpoint)
	default:
		// Default: TGI
		tgiClient = inference.NewTGIClient(cfg.TGIEndpoint)
		tgiClient.SetAuthToken(cfg.TGIToken)
		engine = tgiClient
		log.Printf("Inference engine: tgi (endpoint=%s)", cfg.TGIEndpoint)
	}

	node := &Node{
		Config: cfg,
		Host:   host,
		Chain:  chainClient,
		TGI:    tgiClient,
		Engine: engine,
	}

	// Initialize role modules — one Leader per model_id (P1-4: pass privKey for signing)
	node.Leaders = make(map[string]*leader.Leader, len(cfg.ModelIds))
	for _, modelId := range cfg.ModelIds {
		node.Leaders[modelId] = leader.New(modelId, cfg.WorkerPrivKey, cfg.WorkerAddr, cfg.WorkerPubkey, host, chainClient)
	}

	// L15: query actual stake from chain instead of hardcoded zero
	workerStake := sdkmath.ZeroInt()
	if stakeInfo, err := chainClient.GetWorkerStake(cfg.WorkerAddr); err == nil {
		workerStake = stakeInfo
	}

	node.Proposer = proposer.New(cfg.WorkerAddr, cfg.WorkerPrivKey, chainClient, cfg.AuditRate, cfg.BatchSize)
	node.Worker = worker.New(cfg.WorkerAddr, cfg.WorkerPubkey, cfg.WorkerPrivKey, cfg.ModelIds, host, engine, chainClient)
	// G3: apply configured concurrency limit
	if cfg.MaxConcurrentTasks > 0 {
		node.Worker.SetMaxConcurrentTasks(cfg.MaxConcurrentTasks)
	}
	node.Worker.OutputObserver = node.Proposer
	// P2-2: wire Worker as RebroadcastStopper so Proposer can signal stop on 3 results
	node.Proposer.Rebroadcaster = node.Worker
	node.Verifier = verifier.New(cfg.WorkerAddr, cfg.WorkerPubkey, cfg.WorkerPrivKey, workerStake, host, engine, cfg.Epsilon)

	// P2-1 + P2-5: initialize 7-day data retention store and wire to Proposer
	if dataStore, err := p2pstore.New("/tmp/funai-store-" + cfg.WorkerAddr); err == nil {
		node.Store = dataStore
		node.Proposer.Store = dataStore
	} else {
		log.Printf("Warning: failed to create data store: %v", err)
	}

	// §19 + P7: initialize X25519 encryption keypair for TLS key exchange.
	// Always generate a fresh keypair on startup for forward secrecy (P7 key rotation).
	// Config keys are only used as fallback if generation fails.
	priv, pub, err := privacy.GenerateX25519Keypair()
	if err != nil {
		log.Printf("Warning: failed to generate X25519 keypair: %v", err)
		// P7 fallback: use persisted keys if available
		if len(cfg.EncryptionPubkey) == 32 && len(cfg.EncryptionPrivkey) == 32 {
			node.EncryptionPubkey = cfg.EncryptionPubkey
			node.EncryptionPrivkey = cfg.EncryptionPrivkey
			log.Printf("P7: using persisted encryption keys (generation failed)")
		}
	} else {
		node.EncryptionPubkey = pub[:]
		node.EncryptionPrivkey = priv[:]
		log.Printf("P7: fresh X25519 keypair generated (key rotation on restart)")
	}

	// S6: initialize TLS transport for decrypting incoming messages from SDK clients.
	// Uses the node's X25519 private key. Backward compatible: Unwrap fails on plaintext → treated as unencrypted.
	if len(node.EncryptionPrivkey) == 32 && len(node.EncryptionPubkey) == 32 {
		tlsTransport, err := privacy.NewTransport(privacy.ModeTLS,
			privacy.WithLocalKeys(node.EncryptionPrivkey, node.EncryptionPubkey))
		if err != nil {
			log.Printf("Warning: failed to create TLS transport: %v", err)
		} else {
			node.TLSTransport = tlsTransport
		}
	}

	return node, nil
}

// Start begins the P2P node: connects to bootstrap peers, joins topics, starts listening.
func (n *Node) Start(ctx context.Context) error {
	log.Printf("FunAI P2P Node starting: peer_id=%s", n.Host.ID())
	for _, addr := range n.Host.Addrs() {
		log.Printf("  Listening: %s/p2p/%s", addr, n.Host.ID())
	}

	// Connect to bootstrap peers
	for _, peer := range n.Config.BootPeers {
		if err := n.Host.ConnectPeer(ctx, peer); err != nil {
			log.Printf("  Warning: failed to connect to peer %s: %v", peer, err)
		} else {
			log.Printf("  Connected to peer: %s", peer)
		}
	}

	// Start mDNS for local network discovery
	if err := n.Host.StartMDNS("funai-p2p"); err != nil {
		log.Printf("  Warning: mDNS failed: %v", err)
	}

	// Join model topics
	for _, modelId := range n.Config.ModelIds {
		topic := p2phost.ModelTopic(modelId)
		if _, err := n.Host.Subscribe(topic); err != nil {
			return fmt.Errorf("subscribe to %s: %w", topic, err)
		}
		log.Printf("  Subscribed to topic: %s", topic)
	}

	// Join settlement topic
	if _, err := n.Host.Subscribe(p2phost.SettlementTopic); err != nil {
		return fmt.Errorf("subscribe to settlement: %w", err)
	}
	log.Printf("  Subscribed to topic: %s", p2phost.SettlementTopic)

	// §19: start key exchange publisher — advertise encryption pubkey for TLS
	if len(n.EncryptionPubkey) == 32 {
		for _, modelId := range n.Config.ModelIds {
			go n.publishEncryptionKey(ctx, modelId)
		}
		log.Printf("  Encryption key exchange enabled for %d models", len(n.Config.ModelIds))
	}

	// B1: Start message dispatch loops — route pubsub messages to Leader/Worker/Verifier/Proposer
	if err := n.startDispatchLoops(ctx); err != nil {
		return fmt.Errorf("start dispatch: %w", err)
	}

	// Periodically refresh worker list from chain for VRF ranking
	go n.refreshWorkerList(ctx)

	// C1: Periodically process pending tasks and submit BatchSettlement
	go n.startBatchLoop(ctx)

	log.Printf("FunAI P2P Node ready. Models: %v", n.Config.ModelIds)

	<-ctx.Done()
	return nil
}

// ValidateVerifyResult performs VRF eligibility check on an incoming VerifyResult
// before passing it to the Proposer. P1-7: nodes must verify the submitter's VRF
// eligibility at the P2P layer before relaying, preventing spam from non-eligible nodes.
func (n *Node) ValidateVerifyResult(result *p2ptypes.VerifyResult, receipt *p2ptypes.InferReceipt, activeWorkers []vrftypes.RankedWorker) error {
	if receipt == nil || len(activeWorkers) < 3 {
		// Not enough context for VRF validation — let Proposer handle it
		return n.Proposer.AddVerifyResult(result)
	}

	verifSeed := append(append([]byte{}, result.TaskId...), receipt.ResultHash...)

	candidates := make([]vrftypes.RankedWorker, 0, len(activeWorkers))
	for _, w := range activeWorkers {
		if !bytes.Equal([]byte(w.Address), receipt.WorkerPubkey) {
			candidates = append(candidates, w)
		}
	}

	if len(candidates) < 3 {
		return n.Proposer.AddVerifyResult(result)
	}

	ranked := vrftypes.RankWorkers(verifSeed, candidates, vrftypes.AlphaVerification)

	top := 10 // §9.1: 8-10 candidates
	if top > len(ranked) {
		top = len(ranked)
	}
	for i := 0; i < top; i++ {
		if bytes.Equal(ranked[i].Pubkey, result.VerifierAddr) {
			return n.Proposer.AddVerifyResult(result)
		}
	}

	return fmt.Errorf("P2P VRF check: verifier %x not in top 10 candidates, dropping message", result.VerifierAddr)
}

// SelectAuditCandidates selects 15-20 audit candidates using VRF α=0.0 (P3-8).
// Called at the P2P layer before dispatching audit tasks.
// Returns the top candidates ranked by pure random VRF score (equal probability).
func (n *Node) SelectAuditCandidates(taskId []byte, resultHash []byte, allWorkers []vrftypes.RankedWorker, excludeWorker string, excludeVerifiers []string) []vrftypes.RankedWorker {
	// V5.2 §6.1/§13.4: audit_vrf_seed = task_id || post_verification_block_hash
	// Use current block hash for unpredictability (not resultHash which is known pre-verification)
	var postVerifBlockHash []byte
	if n.Chain != nil {
		blockHash, _, err := n.Chain.GetLatestBlockHash(context.Background())
		if err == nil {
			postVerifBlockHash = blockHash
		}
	}
	if postVerifBlockHash == nil {
		postVerifBlockHash = resultHash // fallback if chain unavailable
	}
	auditSeed := append(append([]byte{}, taskId...), postVerifBlockHash...)

	// Build exclusion set
	excluded := make(map[string]bool)
	excluded[excludeWorker] = true
	for _, v := range excludeVerifiers {
		excluded[v] = true
	}

	// Filter candidates: exclude the worker and original verifiers
	var candidates []vrftypes.RankedWorker
	for _, w := range allWorkers {
		if excluded[w.Address] {
			continue
		}
		candidates = append(candidates, w)
	}

	if len(candidates) == 0 {
		return nil
	}

	// Rank with α=0.0 (pure random — all workers have equal probability regardless of stake)
	ranked := vrftypes.RankWorkers(auditSeed, candidates, vrftypes.AlphaSecondThirdVerification)

	// Audit KT §1: unified rank window 21 (same as verifier)
	maxCandidates := 21
	if maxCandidates > len(ranked) {
		maxCandidates = len(ranked)
	}

	return ranked[:maxCandidates]
}

// DecryptMessage attempts to decrypt an incoming P2P message using the node's TLS transport (S6).
// If decryption succeeds, returns the plaintext. If decryption fails (message is already plaintext
// from a ModePlain client), returns the original data unchanged.
// This should be called on raw pubsub message data before json.Unmarshal.
func (n *Node) DecryptMessage(ctx context.Context, data []byte) []byte {
	if n.TLSTransport == nil {
		return data
	}
	decrypted, err := n.TLSTransport.Unwrap(ctx, data)
	if err != nil {
		// Decryption failed → treat as plaintext (backward compatible with ModePlain clients)
		return data
	}
	return decrypted
}

// publishEncryptionKey periodically publishes this node's X25519 encryption pubkey
// on the key exchange topic so SDK clients can discover it for TLS encryption (§19).
func (n *Node) publishEncryptionKey(ctx context.Context, modelId string) {
	keyTopic := fmt.Sprintf("/funai/keyexchange/%s", modelId)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// P0-6: Sign the pubkey with node's secp256k1 key to prevent MITM.
	// Message format: JSON { pubkey: <X25519 bytes>, sig: <secp256k1 sig over pubkey> }
	signedMsg := n.signKeyExchangeMessage()

	// Publish immediately on start
	_ = n.Host.Publish(ctx, keyTopic, signedMsg)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = n.Host.Publish(ctx, keyTopic, signedMsg)
		}
	}
}

// signKeyExchangeMessage creates a signed key exchange payload.
// P0-6: pubkey is signed with the node's secp256k1 key to prevent MITM attacks.
func (n *Node) signKeyExchangeMessage() []byte {
	type signedKeyExchange struct {
		Pubkey    []byte `json:"pubkey"`
		NodeAddr  string `json:"node_addr"`
		Signature []byte `json:"signature"`
	}
	msg := signedKeyExchange{
		Pubkey:   n.EncryptionPubkey,
		NodeAddr: n.Config.WorkerAddr,
	}
	if len(n.Config.WorkerPrivKey) == 32 {
		h := sha256.Sum256(n.EncryptionPubkey)
		privKey := secp256k1.PrivKey(n.Config.WorkerPrivKey)
		sig, err := privKey.Sign(h[:])
		if err == nil {
			msg.Signature = sig
		}
	}
	data, _ := json.Marshal(msg)
	return data
}

// Stop gracefully shuts down the P2P node.
func (n *Node) Stop() error {
	log.Printf("FunAI P2P Node shutting down...")
	for _, l := range n.Leaders {
		l.Stop()
	}
	return n.Host.Close()
}
