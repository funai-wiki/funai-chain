package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/cometbft/cometbft/crypto/secp256k1"

	funaiapp "github.com/funai-wiki/funai-chain/app"
	"github.com/funai-wiki/funai-chain/p2p"
	"github.com/funai-wiki/funai-chain/p2p/metrics"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	// Initialize bech32 address prefixes so pubkey-to-address conversions use "funai1..." format
	funaiapp.SetAddressPrefixes()

	cfg := p2p.DefaultConfig()

	// Override from environment variables
	if addr := os.Getenv("FUNAI_LISTEN_ADDR"); addr != "" {
		cfg.ListenAddr = addr
	}
	if rpc := os.Getenv("FUNAI_CHAIN_RPC"); rpc != "" {
		cfg.ChainRPC = rpc
	}
	if rest := os.Getenv("FUNAI_CHAIN_REST"); rest != "" {
		cfg.ChainREST = rest
	}
	if tgi := os.Getenv("FUNAI_TGI_ENDPOINT"); tgi != "" {
		cfg.TGIEndpoint = tgi
	}
	if token := os.Getenv("FUNAI_TGI_TOKEN"); token != "" {
		cfg.TGIToken = token
	}
	if worker := os.Getenv("FUNAI_WORKER_ADDR"); worker != "" {
		cfg.WorkerAddr = worker
	}
	if models := os.Getenv("FUNAI_MODELS"); models != "" {
		cfg.ModelIds = splitComma(models)
	}
	if peers := os.Getenv("FUNAI_BOOT_PEERS"); peers != "" {
		cfg.BootPeers = splitComma(peers)
	}

	// B2: Worker signing keys — required for signing InferReceipts, AcceptTask, VerifyResults, key exchange
	if privKeyHex := os.Getenv("FUNAI_WORKER_PRIVKEY"); privKeyHex != "" {
		privKeyBytes, err := hex.DecodeString(privKeyHex)
		if err != nil {
			log.Fatalf("Invalid FUNAI_WORKER_PRIVKEY (expected hex): %v", err)
		}
		cfg.WorkerPrivKey = privKeyBytes
		// Auto-derive pubkey from privkey if not explicitly provided
		if os.Getenv("FUNAI_WORKER_PUBKEY") == "" {
			privKey := secp256k1.PrivKey(privKeyBytes)
			cfg.WorkerPubkey = privKey.PubKey().Bytes()
		}
	}
	if pubKeyHex := os.Getenv("FUNAI_WORKER_PUBKEY"); pubKeyHex != "" {
		pubKeyBytes, err := hex.DecodeString(pubKeyHex)
		if err != nil {
			log.Fatalf("Invalid FUNAI_WORKER_PUBKEY (expected hex): %v", err)
		}
		cfg.WorkerPubkey = pubKeyBytes
	}
	if cfg.WorkerAddr != "" && len(cfg.WorkerPrivKey) == 0 {
		log.Printf("Warning: FUNAI_WORKER_ADDR set but FUNAI_WORKER_PRIVKEY missing — signing disabled")
	}
	if epsilonStr := os.Getenv("FUNAI_EPSILON"); epsilonStr != "" {
		if eps, err := strconv.ParseFloat(epsilonStr, 32); err == nil {
			cfg.Epsilon = float32(eps)
			log.Printf("Epsilon override: %f", cfg.Epsilon)
		}
	}

	if maxConcurrent := os.Getenv("FUNAI_MAX_CONCURRENT"); maxConcurrent != "" {
		if n, err := strconv.Atoi(maxConcurrent); err == nil && n > 0 {
			cfg.MaxConcurrentTasks = uint32(n)
			log.Printf("Max concurrent tasks: %d", n)
		}
	}

	if os.Getenv("FUNAI_TEST_CORRUPT_RECEIPT") == "1" {
		cfg.TestCorruptReceipt = true
		log.Printf("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
		log.Printf("!! TEST-ONLY: Worker will tamper receipt ResultHash on every task.")
		log.Printf("!! A production node with this flag is slashable on every inference.")
		log.Printf("!! This binary is only suitable for scripts/e2e-mock-fraud.sh.")
		log.Printf("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	}

	// Inference backend: "tgi" (default), "openai", "vllm", "sglang", "ollama"
	if backend := os.Getenv("FUNAI_INFERENCE_BACKEND"); backend != "" {
		cfg.InferenceBackend = backend
		log.Printf("Inference backend: %s", backend)
	}
	if model := os.Getenv("FUNAI_INFERENCE_MODEL"); model != "" {
		cfg.InferenceModel = model
		log.Printf("Inference model: %s", model)
	}

	if chainId := os.Getenv("FUNAI_CHAIN_ID"); chainId != "" {
		cfg.ChainID = chainId
	}
	if interval := os.Getenv("FUNAI_BATCH_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			cfg.BatchInterval = d
		}
	}

	if len(cfg.ModelIds) == 0 {
		cfg.ModelIds = []string{"test-model"}
	}

	// Start Prometheus metrics server
	metricsAddr := os.Getenv("FUNAI_METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":9091"
	}
	go func() {
		log.Printf("Metrics server listening on %s/metrics", metricsAddr)
		if err := metrics.StartServer(metricsAddr); err != nil {
			log.Printf("Metrics server error: %v", err)
		}
	}()

	node, err := p2p.NewNode(cfg)
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived %v, shutting down...\n", sig)
		cancel()
	}()

	// Periodically update connected-peers metric
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				metrics.SetConnectedPeers(node.Host.ConnectedPeers())
			}
		}
	}()

	if err := node.Start(ctx); err != nil {
		if ctx.Err() == nil {
			log.Fatalf("Node error: %v", err)
		}
	}

	if err := node.Stop(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	log.Println("FunAI P2P Node stopped.")
}

func splitComma(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == ',' {
			if current != "" {
				result = append(result, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
