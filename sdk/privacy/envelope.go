package privacy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Envelope wraps an encrypted P2P message with metadata needed for decryption.
// Used to encrypt entire InferRequest/StreamToken messages at the SDK layer.
type Envelope struct {
	Version       byte   `json:"version"`
	SenderPubkey  []byte `json:"sender_pubkey"`
	EncryptedData []byte `json:"encrypted_data"`
	ContentHash   []byte `json:"content_hash"`
	IsEncrypted   bool   `json:"is_encrypted"`
}

// Seal creates an encrypted Envelope from plaintext data.
func Seal(data []byte, senderPubkey []byte, recipientPubkey []byte) (*Envelope, error) {
	if len(recipientPubkey) != 32 {
		return nil, fmt.Errorf("recipient pubkey must be 32 bytes")
	}

	contentHash := sha256.Sum256(data)

	encrypted, err := EncryptField(string(data), recipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("encrypt envelope: %w", err)
	}

	return &Envelope{
		Version:       currentVersion,
		SenderPubkey:  senderPubkey,
		EncryptedData: encrypted,
		ContentHash:   contentHash[:],
		IsEncrypted:   true,
	}, nil
}

// Open decrypts an Envelope and returns the plaintext data.
func Open(env *Envelope, recipientPrivKey [32]byte, recipientPubKey [32]byte) ([]byte, error) {
	if !env.IsEncrypted {
		return env.EncryptedData, nil
	}

	plaintext, err := DecryptField(env.EncryptedData, recipientPrivKey, recipientPubKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt envelope: %w", err)
	}

	// Verify content hash
	contentHash := sha256.Sum256([]byte(plaintext))
	for i := range contentHash {
		if i >= len(env.ContentHash) || contentHash[i] != env.ContentHash[i] {
			return nil, fmt.Errorf("envelope content hash mismatch: data tampered")
		}
	}

	return []byte(plaintext), nil
}

// SealJSON encrypts a JSON-serializable message and returns the envelope as JSON bytes.
func SealJSON(msg interface{}, senderPubkey []byte, recipientPubkey []byte) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal message: %w", err)
	}

	env, err := Seal(data, senderPubkey, recipientPubkey)
	if err != nil {
		return nil, err
	}

	return json.Marshal(env)
}

// OpenJSON decrypts a JSON envelope and unmarshals the inner message.
func OpenJSON(envData []byte, recipientPrivKey [32]byte, recipientPubKey [32]byte, dest interface{}) error {
	var env Envelope
	if err := json.Unmarshal(envData, &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}

	plaintext, err := Open(&env, recipientPrivKey, recipientPubKey)
	if err != nil {
		return err
	}

	return json.Unmarshal(plaintext, dest)
}

// PlainEnvelope creates a non-encrypted envelope (for ModePlain).
func PlainEnvelope(data []byte) *Envelope {
	contentHash := sha256.Sum256(data)
	return &Envelope{
		Version:       currentVersion,
		SenderPubkey:  nil,
		EncryptedData: data,
		ContentHash:   contentHash[:],
		IsEncrypted:   false,
	}
}
