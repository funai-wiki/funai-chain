package sdk

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/cometbft/cometbft/crypto/secp256k1"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/funai-wiki/funai-chain/p2p/chain"
	p2phost "github.com/funai-wiki/funai-chain/p2p/host"
	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	"github.com/funai-wiki/funai-chain/sdk/privacy"
	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"
)

// defaultFraudProofChainID is the default Cosmos chain ID used to sign
// MsgFraudProof transactions when Config.ChainID is not set. Matches
// p2p/node.go's default so an SDK instance works out of the box against
// the reference mainnet-readiness testnet.
const defaultFraudProofChainID = "funai_123123123-3"

// Config holds SDK client configuration.
type Config struct {
	ListenAddr  string   // libp2p listen address
	BootPeers   []string // bootstrap peer multiaddrs
	KeyName     string   // user key name
	UserPubkey  []byte   // user's secp256k1 public key
	UserPrivKey []byte   // P0-1: user's secp256k1 private key for signing requests
	ChainRPC    string   // chain RPC URL for FraudProof submission
	ChainREST   string   // chain REST URL
	ChainID     string   // Cosmos chain ID for FraudProof tx signing; falls back to funai_123123123-3

	// P3-3: Privacy options (§19: sanitization defaults ON; set DisableSanitization=true to opt out)
	DisableSanitization    bool          // set true to skip PII sanitization (default: sanitization ON)
	EnableTLS              bool          // enforce TLS for P2P connections (deprecated, use PrivacyMode)
	PrivacyMode            string        // "plain", "tls", "tor", "full"
	TorSocksAddr           string        // Tor SOCKS5 proxy address (default "127.0.0.1:9050")
	EncryptionPubkey       []byte        // X25519 public key for message encryption (auto-generated if empty)
	EncryptionPrivkey      []byte        // X25519 private key (auto-generated if empty)
	RecipientEncryptionKey []byte        // X25519 public key of recipient (Leader) for TLS encryption
	InferTimeout           time.Duration // KT: inference retry timeout (default 30s, range [5s, 120s])
}

// ModelSize categorizes model sizes for tiered expire (P3-5).
type ModelSize int

const (
	ModelSizeSmall  ModelSize = iota // < 7B params
	ModelSizeMedium                  // 7B-30B params
	ModelSizeLarge                   // > 30B params
)

// KT: SDK inference retry timeout bounds.
const (
	DefaultInferTimeout = 30 * time.Second
	MinInferTimeout     = 5 * time.Second
	MaxInferTimeout     = 120 * time.Second
)

// Client sends inference requests and receives streaming results.
type Client struct {
	config              Config
	host                *p2phost.Host
	chainClient         *chain.Client
	transport           privacy.Transport
	privacyMode         privacy.Mode
	recipientEncryptKey []byte        // cached Leader X25519 pubkey for TLS encryption
	InferTimeout        time.Duration // KT: configurable retry timeout (default 30s)
}

// NewClient creates a new SDK client with its own libp2p host.
func NewClient(cfg Config) (*Client, error) {
	listenAddr := cfg.ListenAddr
	if listenAddr == "" {
		listenAddr = "/ip4/0.0.0.0/tcp/0"
	}

	// Backward compatibility: EnableTLS maps to "tls" mode
	privacyModeStr := cfg.PrivacyMode
	if privacyModeStr == "" && cfg.EnableTLS {
		privacyModeStr = "tls"
	}

	privMode, err := privacy.ParseMode(privacyModeStr)
	if err != nil {
		return nil, fmt.Errorf("parse privacy mode: %w", err)
	}

	// Build privacy transport options
	var privOpts []privacy.Option
	if len(cfg.EncryptionPrivkey) == 32 && len(cfg.EncryptionPubkey) == 32 {
		privOpts = append(privOpts, privacy.WithLocalKeys(cfg.EncryptionPrivkey, cfg.EncryptionPubkey))
	}
	if cfg.TorSocksAddr != "" {
		privOpts = append(privOpts, privacy.WithTorAddr(cfg.TorSocksAddr))
	}

	transport, err := privacy.NewTransport(privMode, privOpts...)
	if err != nil {
		return nil, fmt.Errorf("create privacy transport: %w", err)
	}

	if privMode == privacy.ModeTor || privMode == privacy.ModeFull {
		log.Printf("SDK: privacy mode=%s (Tor SOCKS5 configured)", privMode)
	}
	if privMode == privacy.ModeTLS || privMode == privacy.ModeFull {
		log.Printf("SDK: privacy mode=%s (E2E encryption enabled)", privMode)
	}

	host, err := p2phost.New(listenAddr)
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("create SDK host: %w", err)
	}

	for _, peer := range cfg.BootPeers {
		if err := host.ConnectPeer(context.Background(), peer); err != nil {
			log.Printf("SDK: warning: failed to connect to %s: %v", peer, err)
		}
	}

	var cc *chain.Client
	if cfg.ChainRPC != "" {
		cc = chain.NewClient(cfg.ChainRPC, cfg.ChainREST)
	}

	c := &Client{
		config:              cfg,
		host:                host,
		chainClient:         cc,
		transport:           transport,
		privacyMode:         privMode,
		recipientEncryptKey: cfg.RecipientEncryptionKey,
		InferTimeout:        DefaultInferTimeout,
	}
	// KT: clamp user-configured timeout
	if cfg.InferTimeout > 0 {
		c.InferTimeout = cfg.InferTimeout
	}
	if c.InferTimeout < MinInferTimeout {
		c.InferTimeout = MinInferTimeout
	}
	if c.InferTimeout > MaxInferTimeout {
		c.InferTimeout = MaxInferTimeout
	}
	return c, nil
}

// InferParams holds parameters for an inference request.
type InferParams struct {
	ModelId      string
	Prompt       string
	Fee          uint64 // in ufai
	Temperature  uint16 // 0=argmax, 10000=1.0
	TopP         uint16 // 0 or 10000=disabled, 1-9999=nucleus sampling (10000=1.0)
	MaxExpire    uint64 // max blocks for signature validity
	MaxTokens    uint32 // expected max output tokens (for tiered expire)
	MaxLatencyMs uint32 // max first-token latency in ms (0=no constraint)
	StreamMode   bool   // whether to request streaming response
}

// InferResult holds the final inference result.
type InferResult struct {
	TaskId     []byte
	Output     string
	ResultHash []byte
	Tokens     []string
	Verified   bool // M7: true if result_hash matches worker's receipt
}

// Infer sends an inference request and returns the complete result.
// Implements 5-second auto-retry with the same task_id.
// M7: verifies result_hash against Worker's InferReceipt after completion.
func (c *Client) Infer(ctx context.Context, params InferParams) (*InferResult, error) {
	prompt := params.Prompt
	var piiMapping []PIIMapping

	// P3-3 + §19: sanitize PII from prompt by default (reversible); opt out with DisableSanitization
	if !c.config.DisableSanitization {
		sr := SanitizePromptReversible(prompt)
		prompt = sr.Sanitized
		piiMapping = sr.Mapping
	}

	// P2-2: client-side temperature validation (saves network roundtrip)
	if params.Temperature > 20000 {
		return nil, fmt.Errorf("temperature %d exceeds maximum 20000", params.Temperature)
	}

	// M3: auto-set expire by token count (§3.6), with 17280 block hard cap
	if params.MaxExpire == 0 {
		params.MaxExpire = TieredExpireByTokens(params.MaxTokens)
	}
	if params.MaxExpire > MaxExpireBlocks {
		params.MaxExpire = MaxExpireBlocks
	}

	promptHash := sha256.Sum256([]byte(prompt))
	timestamp := uint64(time.Now().UnixNano())

	var userSeed []byte
	if params.Temperature > 0 {
		userSeed = make([]byte, 32)
		if _, err := rand.Read(userSeed); err != nil {
			return nil, fmt.Errorf("generate user seed: %w", err)
		}
	}

	// P0-8: expire_block must be absolute block height, not relative offset.
	// Query current chain height and add MaxExpire as offset.
	expireBlock := params.MaxExpire // fallback if chain query fails
	if c.chainClient != nil {
		_, currentHeight, err := c.chainClient.GetLatestBlockHash(ctx)
		if err == nil && currentHeight > 0 {
			expireBlock = uint64(currentHeight) + params.MaxExpire
		}
	}

	req := &p2ptypes.InferRequest{
		ModelId:      []byte(params.ModelId),
		PromptHash:   promptHash[:],
		MaxFee:       params.Fee,
		ExpireBlock:  expireBlock,
		Temperature:  params.Temperature,
		TopP:         params.TopP,
		Timestamp:    timestamp,
		UserPubkey:   c.config.UserPubkey,
		Prompt:       prompt,
		UserSeed:     userSeed,
		MaxTokens:    params.MaxTokens,
		MaxLatencyMs: params.MaxLatencyMs,
		StreamMode:   params.StreamMode,
	}

	// P0-1: Sign the InferRequest with user's secp256k1 private key.
	// Without this signature, Leader rejects all requests.
	if len(c.config.UserPrivKey) == 32 {
		signBytes := req.SignBytes()
		msgHash := sha256.Sum256(signBytes)
		privKey := secp256k1.PrivKey(c.config.UserPrivKey)
		sig, err := privKey.Sign(msgHash[:])
		if err != nil {
			return nil, fmt.Errorf("sign InferRequest: %w", err)
		}
		req.UserSignature = sig
	}

	taskId := req.TaskId()

	responseTopic := fmt.Sprintf("/funai/response/%x", taskId)
	sub, err := c.host.Subscribe(responseTopic)
	if err != nil {
		return nil, fmt.Errorf("subscribe response: %w", err)
	}

	// Subscribe to receipt topic for M7 verification
	receiptTopic := fmt.Sprintf("/funai/receipt/%x", taskId)
	receiptSub, _ := c.host.Subscribe(receiptTopic)

	reqData, _ := json.Marshal(req)

	// Apply privacy transport encryption if enabled.
	// P2-3: refuse to send plaintext when privacy mode requires encryption.
	publishData := reqData
	if c.privacyMode == privacy.ModeTLS || c.privacyMode == privacy.ModeFull {
		if len(c.recipientEncryptKey) != 32 {
			c.resolveRecipientKey(ctx, params.ModelId)
		}
		if len(c.recipientEncryptKey) != 32 {
			return nil, fmt.Errorf("privacy mode %s requires encryption key but key exchange failed", c.privacyMode)
		}
		wrapped, wrapErr := c.transport.Wrap(ctx, reqData, c.recipientEncryptKey)
		if wrapErr != nil {
			return nil, fmt.Errorf("privacy wrap failed: %w", wrapErr)
		}
		publishData = wrapped
	}

	modelTopic := p2phost.ModelTopic(params.ModelId)
	if err := c.host.Publish(ctx, modelTopic, publishData); err != nil {
		return nil, fmt.Errorf("publish request: %w", err)
	}

	log.Printf("SDK: sent InferRequest task_id=%x model=%s fee=%d privacy=%s", taskId[:8], params.ModelId, params.Fee, c.privacyMode)

	var tokens []string
	var workerReceipt *p2ptypes.InferReceipt
	retryTimer := time.NewTimer(c.InferTimeout)
	defer retryTimer.Stop()

	// P2-9: use channel to safely receive receipt from goroutine (no data race)
	receiptCh := make(chan *p2ptypes.InferReceipt, 1)
	go func() {
		if receiptSub == nil {
			return
		}
		msg, err := receiptSub.Next(ctx)
		if err != nil {
			return
		}
		var receipt p2ptypes.InferReceipt
		if err := json.Unmarshal(msg.Data, &receipt); err != nil {
			return
		}
		receiptCh <- &receipt
	}()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-retryTimer.C:
			log.Printf("SDK: timeout, retrying task_id=%x", taskId[:8])
			// P2-1: re-resolve encryption key on retry if initial resolution failed
			if (c.privacyMode == privacy.ModeTLS || c.privacyMode == privacy.ModeFull) && len(c.recipientEncryptKey) != 32 {
				c.resolveRecipientKey(ctx, params.ModelId)
				if len(c.recipientEncryptKey) == 32 {
					if wrapped, err := c.transport.Wrap(ctx, reqData, c.recipientEncryptKey); err == nil {
						publishData = wrapped
					}
				}
			}
			_ = c.host.Publish(ctx, modelTopic, publishData)
			retryTimer.Reset(c.InferTimeout)
		default:
			msg, err := sub.Next(ctx)
			if err != nil {
				continue
			}

			var streamToken p2ptypes.StreamToken
			if err := json.Unmarshal(msg.Data, &streamToken); err != nil {
				continue
			}

			tokens = append(tokens, streamToken.Token)

			if streamToken.IsFinal {
				output := ""
				for _, t := range tokens {
					output += t
				}

				resultHash := sha256.Sum256([]byte(output))

				// P3-7: Worker signs sha256(complete_output) on the final StreamToken.
				// Capture it here so a downstream FraudProof can carry a valid
				// WorkerContentSig through the on-chain H3 check
				// (x/settlement/keeper/keeper.go:1279-1288). Without this, FraudProof
				// would ship empty WorkerContentSig and the H3 sig verification would
				// be silently skipped — anyone could spam forged fraud reports.
				workerContentSig := streamToken.ContentSig

				// Restore PII in output if sanitization was applied
				restoredOutput := output
				if len(piiMapping) > 0 {
					restoredOutput = RestoreOutput(output, piiMapping)
				}

				result := &InferResult{
					TaskId:     taskId,
					Output:     restoredOutput,
					ResultHash: resultHash[:],
					Tokens:     tokens,
					Verified:   true,
				}

				// P2-9: safely receive receipt from channel (no data race)
				select {
				case r := <-receiptCh:
					workerReceipt = r
				default:
				}

				// M7: verify result_hash against Worker's InferReceipt
				if workerReceipt != nil {
					// P3-4: verify Worker's signature on the receipt before trusting it
					receiptSigValid := verifyWorkerReceiptSig(workerReceipt)
					if !receiptSigValid {
						log.Printf("SDK: Worker receipt signature invalid for task %x, ignoring", taskId[:8])
					} else if !bytes.Equal(resultHash[:], workerReceipt.ResultHash) {
						result.Verified = false
						log.Printf("SDK: FRAUD DETECTED! result_hash mismatch for task %x", taskId[:8])
						c.submitFraudProof(ctx, taskId, workerReceipt, []byte(output), resultHash[:], workerContentSig)
					}
				}

				return result, nil
			}

			retryTimer.Reset(c.InferTimeout)
		}
	}
}

// verifyWorkerReceiptSig checks a Worker's secp256k1 signature on an InferReceipt.
//
// Must stay byte-for-byte identical to the signer in p2p/worker.signReceipt and
// the verifier in p2p/verifier.verifyWorkerPayloadSignature, both of which pass
// receipt.SignBytes() directly to the cometbft Sign/VerifySignature primitives.
// Those primitives internally sha256 their input once (cometbft/crypto/secp256k1
// Sign:L131 and VerifySignature:L214), so adding an explicit sha256.Sum256 on
// top of SignBytes() here would produce a 3-layer digest while Worker/Verifier
// use a 2-layer digest — every receipt would fail verification. The previous
// version of this function had exactly that bug.
func verifyWorkerReceiptSig(receipt *p2ptypes.InferReceipt) bool {
	if receipt == nil {
		return false
	}
	if len(receipt.WorkerPubkey) != 33 || len(receipt.WorkerSig) == 0 {
		return false
	}
	pk := secp256k1.PubKey(receipt.WorkerPubkey)
	return pk.VerifySignature(receipt.SignBytes(), receipt.WorkerSig)
}

// submitFraudProof submits a MsgFraudProof to the chain when Worker sends
// content that doesn't match the signed InferReceipt.
//
// M7: last line of defense against Workers sending fake content.
//
// Prior to this implementation the SDK marshalled a custom struct to raw JSON
// and handed it to BroadcastTx, which CometBFT rejects as "tx parse error"
// — the on-chain keeper is complete but the submission channel was broken
// (same pattern as D2 / issue #9). This rewrite builds a proper
// MsgFraudProof, derives bech32 addresses from pubkeys, includes the
// Worker's content signature (captured from the final StreamToken's
// ContentSig), and hands the message to BroadcastFraudProof which uses the
// Cosmos SDK tx builder + signer + TxEncoder pipeline.
//
// actualContent: the bytes the SDK actually received from streaming.
// contentHash: sha256(actualContent) — matches what Worker signed.
// workerContentSig: the ContentSig field on the final StreamToken. If empty,
//   the on-chain H3 check skips sig verification — the fraud proof still
//   lands but can be spammed. The SDK logs a warning in that case.
func (c *Client) submitFraudProof(ctx context.Context, taskId []byte, receipt *p2ptypes.InferReceipt, actualContent, contentHash, workerContentSig []byte) {
	if c.chainClient == nil {
		log.Printf("SDK: FraudProof: no chain client configured, cannot submit")
		return
	}
	if receipt == nil || len(receipt.WorkerPubkey) != 33 {
		log.Printf("SDK: FraudProof: missing or malformed Worker receipt, cannot submit for task %x", taskId[:8])
		return
	}
	if len(c.config.UserPrivKey) != 32 || len(c.config.UserPubkey) != 33 {
		log.Printf("SDK: FraudProof: user key not configured, cannot sign fraud-proof tx for task %x", taskId[:8])
		return
	}
	if len(workerContentSig) == 0 {
		log.Printf("SDK: FraudProof WARNING: Worker's ContentSig was not captured on the final StreamToken; submitting unsigned fraud proof for task %x — on-chain H3 sig check will be skipped", taskId[:8])
	}

	reporterAddr, err := bech32FromPubkey(c.config.UserPubkey)
	if err != nil {
		log.Printf("SDK: FraudProof: derive reporter bech32 failed: %v", err)
		return
	}
	workerAddr, err := bech32FromPubkey(receipt.WorkerPubkey)
	if err != nil {
		log.Printf("SDK: FraudProof: derive worker bech32 failed: %v", err)
		return
	}

	msg := settlementtypes.NewMsgFraudProof(
		reporterAddr,
		taskId,
		workerAddr,
		contentHash,
		workerContentSig,
		actualContent,
	)

	chainID := c.config.ChainID
	if chainID == "" {
		chainID = defaultFraudProofChainID
	}

	txHash, err := c.chainClient.BroadcastFraudProof(ctx, msg, c.config.UserPrivKey, reporterAddr, chainID)
	if err != nil {
		log.Printf("SDK: FraudProof: broadcast error for task %x: %v", taskId[:8], err)
		return
	}

	log.Printf("SDK: FraudProof submitted tx=%s for task %x worker=%s", txHash, taskId[:8], workerAddr)
}

// bech32FromPubkey returns the funai-prefixed bech32 address of a compressed
// secp256k1 pubkey. Uses explicit-prefix bech32 encoding so the SDK does not
// depend on the caller having called app.SetAddressPrefixes() on the global
// sdk.Config — important because the SDK is designed to be importable by
// third-party tooling that does not link the full app package.
func bech32FromPubkey(pubkey []byte) (string, error) {
	if len(pubkey) != 33 {
		return "", fmt.Errorf("expected 33-byte compressed secp256k1 pubkey, got %d", len(pubkey))
	}
	pk := secp256k1.PubKey(pubkey)
	addrBytes := pk.Address().Bytes()
	return bech32.ConvertAndEncode("funai", addrBytes)
}

// ---- P3-3: Privacy Protection ----

// piiPatterns defines regex patterns for common PII types to sanitize.
// NOTE: Go's regexp/syntax does not support Perl lookaheads (?!...).
// Patterns use broader matches; false positives are acceptable for privacy-first sanitization.
var piiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),                            // email
	regexp.MustCompile(`\b\d{3}[-.\s]?\d{2}[-.\s]?\d{4}\b`),                                               // SSN (US): 3-2-4 digit pattern
	regexp.MustCompile(`\b(?:4\d{3}|5[1-5]\d{2}|3[47]\d{2}|6011)\d{4,}\b`),                                // credit card: major issuer prefixes
	regexp.MustCompile(`\b1[3-9]\d{9}\b`),                                                                 // China mobile phone
	regexp.MustCompile(`\b[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]\b`), // China ID card
	regexp.MustCompile(`\b[2-9]\d{2}[-.\s]?[2-9]\d{2}[-.\s]?\d{4}\b`),                                     // US phone: NANP
}

// SanitizeResult holds a sanitized prompt and the mapping needed to restore PII in outputs.
type SanitizeResult struct {
	Sanitized string
	Mapping   []PIIMapping // ordered list of replacements
}

// PIIMapping records a single PII substitution for reversible sanitization.
type PIIMapping struct {
	Placeholder string
	Original    string
}

// SanitizePrompt removes common PII patterns from the prompt (P3-3).
// Replaces detected PII with indexed placeholders like [PII_0], [PII_1], etc.
func SanitizePrompt(prompt string) string {
	r := SanitizePromptReversible(prompt)
	return r.Sanitized
}

// SanitizePromptReversible removes PII and returns a mapping for restoration.
func SanitizePromptReversible(prompt string) SanitizeResult {
	result := prompt
	var mapping []PIIMapping
	idx := 0

	for _, pattern := range piiPatterns {
		matches := pattern.FindAllString(result, -1)
		for _, m := range matches {
			placeholder := fmt.Sprintf("[PII_%d]", idx)
			mapping = append(mapping, PIIMapping{Placeholder: placeholder, Original: m})
			result = strings.Replace(result, m, placeholder, 1)
			idx++
		}
	}

	return SanitizeResult{Sanitized: result, Mapping: mapping}
}

// RestoreOutput replaces PII placeholders in the model output with original values.
// §19: "auto-restore PII after result is returned"
func RestoreOutput(output string, mapping []PIIMapping) string {
	result := output
	for _, m := range mapping {
		result = strings.ReplaceAll(result, m.Placeholder, m.Original)
	}
	return result
}

// ---- M3: Tiered Expire by Token Count (§3.6) ----

const (
	// MaxExpireBlocks is the chain hard cap: 24h = 17280 blocks at 5s/block (§3.6)
	MaxExpireBlocks uint64 = 17280
)

// TieredExpireByTokens returns expire blocks based on expected output token count (§3.6).
// Small (<1000 tokens): 30min = 360 blocks
// Medium (1000-10000): 2hr = 1440 blocks
// Large (>10000): 6hr = 4320 blocks
func TieredExpireByTokens(maxTokens uint32) uint64 {
	switch {
	case maxTokens > 0 && maxTokens < 1000:
		return 360 // 30 minutes
	case maxTokens > 10000:
		return 4320 // 6 hours
	default:
		return 1440 // 2 hours (default for medium or unspecified)
	}
}

// TieredExpireBlocks returns the recommended expire_block based on model size (legacy).
// Kept for backwards compatibility; prefer TieredExpireByTokens.
func TieredExpireBlocks(size ModelSize) uint64 {
	switch size {
	case ModelSizeSmall:
		return 360
	case ModelSizeMedium:
		return 1440
	case ModelSizeLarge:
		return 4320
	default:
		return 1440
	}
}

// InferModelSize estimates the model size category from the model name (P3-5).
func InferModelSize(modelName string) ModelSize {
	lower := strings.ToLower(modelName)
	// Check for size indicators in model name
	if strings.Contains(lower, "70b") || strings.Contains(lower, "65b") ||
		strings.Contains(lower, "40b") || strings.Contains(lower, "34b") ||
		strings.Contains(lower, "180b") || strings.Contains(lower, "405b") {
		return ModelSizeLarge
	}
	if strings.Contains(lower, "13b") || strings.Contains(lower, "7b") ||
		strings.Contains(lower, "8b") || strings.Contains(lower, "14b") ||
		strings.Contains(lower, "22b") || strings.Contains(lower, "27b") {
		return ModelSizeMedium
	}
	// Default small for names like 1b, 3b, or no size indicator
	return ModelSizeSmall
}

// resolveRecipientKey attempts to discover the Leader's X25519 encryption public key
// via P2P key exchange topic. The Leader publishes its encryption pubkey on
// /funai/keyexchange/<model_id> so SDK clients can encrypt requests.
func (c *Client) resolveRecipientKey(ctx context.Context, modelId string) {
	keyTopic := fmt.Sprintf("/funai/keyexchange/%s", modelId)
	sub, err := c.host.Subscribe(keyTopic)
	if err != nil {
		log.Printf("SDK: keyexchange subscribe error: %v", err)
		return
	}

	// Wait up to 2s for a key exchange response
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	msg, err := sub.Next(timeoutCtx)
	if err != nil {
		log.Printf("SDK: keyexchange timeout for model %s: %v", modelId, err)
		return
	}

	// H4: parse signed key exchange JSON and verify secp256k1 signature to prevent MITM (§19)
	type signedKeyExchange struct {
		Pubkey    []byte `json:"pubkey"`
		NodeAddr  string `json:"node_addr"`
		Signature []byte `json:"signature"`
	}
	var keyMsg signedKeyExchange
	if err := json.Unmarshal(msg.Data, &keyMsg); err != nil {
		log.Printf("SDK: keyexchange invalid JSON for model %s — rejecting", modelId)
		return
	}
	if len(keyMsg.Pubkey) != 32 {
		log.Printf("SDK: keyexchange invalid pubkey length %d for model %s", len(keyMsg.Pubkey), modelId)
		return
	}
	// P0-3: verify secp256k1 signature over the X25519 pubkey using on-chain Worker pubkey
	if len(keyMsg.Signature) > 0 && keyMsg.NodeAddr != "" {
		h := sha256.Sum256(keyMsg.Pubkey)
		verified := false
		if c.chainClient != nil {
			workerPubkey, err := c.chainClient.GetWorkerPubkey(keyMsg.NodeAddr)
			if err == nil && len(workerPubkey) == 33 {
				pk := secp256k1.PubKey(workerPubkey)
				if pk.VerifySignature(h[:], keyMsg.Signature) {
					verified = true
					log.Printf("SDK: keyexchange signature verified for model %s node %s", modelId, keyMsg.NodeAddr)
				} else {
					log.Printf("SDK: keyexchange signature INVALID for model %s node %s — rejecting", modelId, keyMsg.NodeAddr)
					return
				}
			} else {
				log.Printf("SDK: keyexchange cannot query worker pubkey for %s: %v", keyMsg.NodeAddr, err)
			}
		}
		if !verified {
			log.Printf("SDK: keyexchange signature not verified (no chain client) for model %s — rejecting", modelId)
			return
		}
	} else {
		log.Printf("SDK: keyexchange missing signature or node_addr for model %s — rejecting", modelId)
		return
	}
	c.recipientEncryptKey = keyMsg.Pubkey
}

// SetRecipientEncryptionKey sets the Leader's X25519 public key for TLS encryption.
func (c *Client) SetRecipientEncryptionKey(key []byte) {
	c.recipientEncryptKey = key
}

// PrivacyMode returns the active privacy mode.
func (c *Client) PrivacyMode() privacy.Mode {
	return c.privacyMode
}

// Close shuts down the SDK client and privacy transport.
func (c *Client) Close() error {
	if c.transport != nil {
		c.transport.Close()
	}
	return c.host.Close()
}
