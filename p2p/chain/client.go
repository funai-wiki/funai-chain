package chain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkmath "cosmossdk.io/math"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdkcrypto "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	funaiapp "github.com/funai-wiki/funai-chain/app"
	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"
)

// Client provides access to the funaid chain node via REST/RPC.
// Uses the CometBFT RPC and Cosmos REST endpoints.
type Client struct {
	rpcURL  string
	restURL string
	http    *http.Client

	// Tx signing infrastructure (lazy init)
	txConfigOnce sync.Once
	txConfig     sdkclient.TxConfig

	// Sequence manager for batch submission
	seqMu          sync.Mutex
	cachedAccNum   uint64
	cachedSeq      uint64
	seqInitialized bool
}

func NewClient(rpcURL, restURL string) *Client {
	return &Client{
		rpcURL:  strings.TrimRight(rpcURL, "/"),
		restURL: strings.TrimRight(restURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) getTxConfig() sdkclient.TxConfig {
	c.txConfigOnce.Do(func() {
		enc := funaiapp.MakeEncodingConfig()
		c.txConfig = enc.TxConfig
	})
	return c.txConfig
}

// ABCIQueryResult wraps a raw ABCI query response.
type ABCIQueryResult struct {
	Value []byte
}

// QueryStore performs a raw ABCI store query (same as funaid CLI).
func (c *Client) QueryStore(ctx context.Context, storeKey string, key []byte) ([]byte, error) {
	path := fmt.Sprintf("/abci_query?path=\"/store/%s/key\"&data=0x%x", storeKey, key)
	url := c.rpcURL + path

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query chain: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Result struct {
			Response struct {
				Value []byte `json:"value"`
			} `json:"response"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode query: %w", err)
	}

	return result.Result.Response.Value, nil
}

// GetLatestBlockHash returns the latest block hash from CometBFT.
func (c *Client) GetLatestBlockHash(ctx context.Context) ([]byte, int64, error) {
	url := c.rpcURL + "/status"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("get status: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var status struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHash   string `json:"latest_block_hash"`
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, 0, fmt.Errorf("decode status: %w", err)
	}

	hash := []byte(status.Result.SyncInfo.LatestBlockHash)
	height := int64(0)
	fmt.Sscanf(status.Result.SyncInfo.LatestBlockHeight, "%d", &height)

	return hash, height, nil
}

// GetInferenceBalance queries a user's InferenceAccount balance from x/settlement.
// S8: Must query the settlement module's InferenceAccount, not x/bank.
// Uses ABCI store query via CometBFT RPC (REST gateway is not registered for settlement).
func (c *Client) GetInferenceBalance(ctx context.Context, userAddr string) (uint64, error) {
	// Convert bech32 address to raw bytes for the store key
	addrBytes, err := addressFromBech32(userAddr)
	if err != nil {
		return 0, fmt.Errorf("invalid address %s: %w", userAddr, err)
	}

	// InferenceAccountKey = 0x01 + address_bytes
	storeKey := append([]byte{0x01}, addrBytes...)
	value, err := c.QueryStore(ctx, "settlement", storeKey)
	if err != nil {
		return 0, fmt.Errorf("query inference balance: %w", err)
	}
	if len(value) == 0 {
		return 0, nil // no account yet
	}

	// InferenceAccount is stored as JSON
	var account struct {
		Address string `json:"address"`
		Balance struct {
			Denom  string `json:"denom"`
			Amount string `json:"amount"`
		} `json:"balance"`
	}
	if err := json.Unmarshal(value, &account); err != nil {
		return 0, fmt.Errorf("decode inference account: %w", err)
	}

	var amount uint64
	fmt.Sscanf(account.Balance.Amount, "%d", &amount)
	return amount, nil
}

// addressFromBech32 decodes a bech32 address string to raw bytes.
func addressFromBech32(addr string) ([]byte, error) {
	// Simple bech32 decode — extract the 20-byte address
	// Use the same logic as sdk.AccAddressFromBech32 without requiring SDK import
	if len(addr) < 3 {
		return nil, fmt.Errorf("address too short")
	}
	// Find the separator (last '1' in the string)
	sep := strings.LastIndex(addr, "1")
	if sep < 1 {
		return nil, fmt.Errorf("invalid bech32 address: no separator")
	}
	data := addr[sep+1:]
	// Bech32 decode the data part
	decoded, err := bech32Decode(data)
	if err != nil {
		return nil, err
	}
	// Convert from 5-bit to 8-bit groups
	converted, err := convertBits(decoded, 5, 8, false)
	if err != nil {
		return nil, err
	}
	return converted, nil
}

// bech32Decode decodes bech32 data characters to 5-bit values.
func bech32Decode(data string) ([]byte, error) {
	const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	result := make([]byte, 0, len(data))
	for _, c := range data {
		idx := strings.IndexRune(charset, c)
		if idx < 0 {
			return nil, fmt.Errorf("invalid bech32 character: %c", c)
		}
		result = append(result, byte(idx))
	}
	// Remove 6-byte checksum
	if len(result) < 6 {
		return nil, fmt.Errorf("bech32 data too short")
	}
	return result[:len(result)-6], nil
}

// convertBits converts between bit groups (e.g., 5-bit to 8-bit for bech32).
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint32(0)
	bits := uint(0)
	maxv := uint32((1 << toBits) - 1)
	var result []byte
	for _, b := range data {
		acc = (acc << fromBits) | uint32(b)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			result = append(result, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			result = append(result, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits {
		return nil, fmt.Errorf("invalid padding")
	}
	return result, nil
}

// GetWorkerStake queries a worker's staked amount from x/worker.
func (c *Client) GetWorkerStake(workerAddr string) (sdkmath.Int, error) {
	path := fmt.Sprintf("%s/funai/worker/v1/worker/%s", c.restURL, workerAddr)
	req, err := http.NewRequest("GET", path, nil)
	if err != nil {
		return sdkmath.ZeroInt(), err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return sdkmath.ZeroInt(), fmt.Errorf("query worker: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Worker struct {
			Stake string `json:"stake"`
		} `json:"worker"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return sdkmath.ZeroInt(), fmt.Errorf("decode worker: %w", err)
	}

	stake, ok := sdkmath.NewIntFromString(result.Worker.Stake)
	if !ok {
		return sdkmath.ZeroInt(), nil
	}
	return stake, nil
}

// GetWorkerPubkey queries a worker's registered secp256k1 public key from x/worker.
func (c *Client) GetWorkerPubkey(workerAddr string) ([]byte, error) {
	path := fmt.Sprintf("%s/funai/worker/v1/worker/%s", c.restURL, workerAddr)
	req, err := http.NewRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query worker pubkey: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Worker struct {
			Pubkey string `json:"pubkey"`
		} `json:"worker"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode worker pubkey: %w", err)
	}

	if result.Worker.Pubkey == "" {
		return nil, fmt.Errorf("worker %s has no registered pubkey", workerAddr)
	}

	pubkeyBytes, err := hex.DecodeString(result.Worker.Pubkey)
	if err != nil {
		return nil, fmt.Errorf("decode pubkey hex: %w", err)
	}
	return pubkeyBytes, nil
}

// GetModelAvgFee queries a model's average fee from x/modelreg.
func (c *Client) GetModelAvgFee(ctx context.Context, modelAlias string) (uint64, error) {
	path := fmt.Sprintf("%s/funai/modelreg/v1/model_by_alias/%s", c.restURL, modelAlias)
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("query model avg fee: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Model struct {
			AvgFee uint64 `json:"avg_fee"`
		} `json:"model"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode model avg fee: %w", err)
	}

	return result.Model.AvgFee, nil
}

// QueryModels returns all registered models as raw JSON.
func (c *Client) QueryModels(ctx context.Context) (json.RawMessage, error) {
	path := fmt.Sprintf("%s/funai/modelreg/v1/models", c.restURL)
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query models: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Models json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	return result.Models, nil
}

// WorkerListEntry represents a worker from the chain's worker list endpoint.
type WorkerListEntry struct {
	Address            string   `json:"address"`
	Pubkey             string   `json:"pubkey"`
	Stake              string   `json:"stake"`
	Models             []string `json:"models"`
	MaxConcurrentTasks uint32   `json:"max_concurrent_tasks"`
	ReputationScore    uint32   `json:"reputation_score"`
	AvgLatencyMs       uint32   `json:"avg_latency_ms"`
}

// GetActiveWorkers queries all registered workers from x/worker via ABCI subspace iteration.
// Falls back to REST endpoint if available.
func (c *Client) GetActiveWorkers(ctx context.Context) ([]WorkerListEntry, error) {
	// Use REST API to query all workers (reliable across Cosmos SDK versions)
	url := fmt.Sprintf("%s/funai/worker/v1/workers", c.restURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query workers: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Workers []struct {
			Address string `json:"address"`
			Pubkey  string `json:"pubkey"`
			Stake   struct {
				Amount string `json:"amount"`
			} `json:"stake"`
			SupportedModels    []string `json:"supported_models"`
			Jailed             bool     `json:"jailed"`
			Tombstoned         bool     `json:"tombstoned"`
			MaxConcurrentTasks uint32   `json:"max_concurrent_tasks"`
			ReputationScore    uint32   `json:"reputation_score"`
			AvgLatencyMs       uint32   `json:"avg_latency_ms"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode workers query: %w", err)
	}

	var workers []WorkerListEntry
	for _, w := range result.Workers {
		if w.Jailed || w.Tombstoned {
			continue
		}
		stake := w.Stake.Amount
		if stake == "" {
			stake = "0"
		}
		workers = append(workers, WorkerListEntry{
			Address:            w.Address,
			Pubkey:             w.Pubkey,
			Stake:              stake,
			Models:             w.SupportedModels,
			MaxConcurrentTasks: w.MaxConcurrentTasks,
			ReputationScore:    w.ReputationScore,
			AvgLatencyMs:       w.AvgLatencyMs,
		})
	}

	return workers, nil
}

// BroadcastTx broadcasts a signed transaction to the chain.
func (c *Client) BroadcastTx(ctx context.Context, txBytes []byte) (string, error) {
	url := fmt.Sprintf("%s/broadcast_tx_sync?tx=0x%x", c.rpcURL, txBytes)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("broadcast: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Result struct {
			Hash string `json:"hash"`
			Code int    `json:"code"`
			Log  string `json:"log"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode broadcast: %w", err)
	}

	if result.Result.Code != 0 {
		return "", fmt.Errorf("tx rejected code=%d: %s", result.Result.Code, result.Result.Log)
	}

	return result.Result.Hash, nil
}

// QueryAccountInfo queries account_number and sequence from the chain REST API.
func (c *Client) QueryAccountInfo(ctx context.Context, addr string) (accNum uint64, seq uint64, err error) {
	url := fmt.Sprintf("%s/cosmos/auth/v1beta1/accounts/%s", c.restURL, addr)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("query account: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Account struct {
			AccountNumber string `json:"account_number"`
			Sequence      string `json:"sequence"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, fmt.Errorf("decode account info: %w (body: %s)", err, string(body[:min(len(body), 200)]))
	}
	accNum, _ = strconv.ParseUint(result.Account.AccountNumber, 10, 64)
	seq, _ = strconv.ParseUint(result.Account.Sequence, 10, 64)
	return accNum, seq, nil
}

// getNextSequence returns the next (accNum, sequence) for signing, using cached values when available.
func (c *Client) getNextSequence(ctx context.Context, addr string) (uint64, uint64, error) {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	if !c.seqInitialized {
		accNum, seq, err := c.QueryAccountInfo(ctx, addr)
		if err != nil {
			return 0, 0, err
		}
		c.cachedAccNum = accNum
		c.cachedSeq = seq
		c.seqInitialized = true
	}
	accNum, seq := c.cachedAccNum, c.cachedSeq
	c.cachedSeq++
	return accNum, seq, nil
}

// ResetSequence forces re-query of account sequence on next BroadcastSettlement call.
func (c *Client) ResetSequence() {
	c.seqMu.Lock()
	c.seqInitialized = false
	c.seqMu.Unlock()
}

// BroadcastSettlement builds, signs, and broadcasts a MsgBatchSettlement to the chain.
func (c *Client) BroadcastSettlement(ctx context.Context, msg *settlementtypes.MsgBatchSettlement, privKey []byte, fromAddr string, chainId string) (string, error) {
	txCfg := c.getTxConfig()

	accNum, seq, err := c.getNextSequence(ctx, fromAddr)
	if err != nil {
		return "", fmt.Errorf("get sequence: %w", err)
	}

	txBuilder := txCfg.NewTxBuilder()
	if err := txBuilder.SetMsgs(msg); err != nil {
		return "", fmt.Errorf("set msgs: %w", err)
	}
	gasLimit := uint64(200000) + uint64(len(msg.Entries))*2000
	// E17: cap gas at block gas limit (default 100M) to prevent tx rejection
	const maxBlockGas = uint64(100_000_000)
	if gasLimit > maxBlockGas {
		gasLimit = maxBlockGas
	}
	txBuilder.SetGasLimit(gasLimit)
	txBuilder.SetFeeAmount(sdk.NewCoins(sdk.NewCoin("ufai", sdkmath.NewInt(int64(gasLimit/200)))))

	// Sign with SIGN_MODE_DIRECT
	sdkPrivKey := &sdkcrypto.PrivKey{Key: privKey}
	pubKey := sdkPrivKey.PubKey()

	sigData := &signingtypes.SingleSignatureData{
		SignMode:  signingtypes.SignMode_SIGN_MODE_DIRECT,
		Signature: nil,
	}
	sig := signingtypes.SignatureV2{
		PubKey:   pubKey,
		Data:     sigData,
		Sequence: seq,
	}
	if err := txBuilder.SetSignatures(sig); err != nil {
		return "", fmt.Errorf("set empty sig: %w", err)
	}

	signerData := authsigning.SignerData{
		ChainID:       chainId,
		AccountNumber: accNum,
		Sequence:      seq,
	}
	sigV2, err := clienttx.SignWithPrivKey(ctx,
		signingtypes.SignMode_SIGN_MODE_DIRECT,
		signerData, txBuilder, sdkPrivKey, txCfg, seq)
	if err != nil {
		return "", fmt.Errorf("sign tx: %w", err)
	}
	if err := txBuilder.SetSignatures(sigV2); err != nil {
		return "", fmt.Errorf("set final sig: %w", err)
	}

	txBytes, err := txCfg.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return "", fmt.Errorf("encode tx: %w", err)
	}

	hash, err := c.BroadcastTx(ctx, txBytes)
	if err != nil {
		// Reset sequence on mismatch so next attempt re-queries
		if strings.Contains(err.Error(), "sequence") {
			log.Printf("chain: sequence mismatch, resetting")
			c.ResetSequence()
		}
		return "", err
	}

	log.Printf("chain: BatchSettlement tx broadcast hash=%s entries=%d gas=%d seq=%d", hash, len(msg.Entries), gasLimit, seq)
	return hash, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
