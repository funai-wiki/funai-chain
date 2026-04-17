package privacy

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
)

const (
	// nonceSize is 12 bytes for AES-256-GCM.
	nonceSize = 12
	// headerSize: 1 byte version + 32 bytes ephemeral pubkey.
	headerSize = 1 + 32
	// currentVersion is the wire format version.
	currentVersion byte = 0x01
)

// tlsTransport provides end-to-end encryption using X25519 ECDH + AES-256-GCM.
//
// Wire format for encrypted messages:
//
//	[1B version][32B ephemeral_pubkey][12B nonce][...ciphertext+tag...]
//
// The sender generates an ephemeral X25519 keypair per message, performs ECDH
// with the recipient's static public key, derives an AES-256 key via
// SHA-256(shared_secret || ephemeral_pubkey || recipient_pubkey), then
// encrypts with AES-256-GCM.
//
// The recipient uses their static private key + ephemeral pubkey from the header
// to recover the same shared secret and decrypt.
type tlsTransport struct {
	localPrivKey [32]byte
	localPubKey  [32]byte

	sessions map[string]cipher.AEAD // cached AEAD per peer pubkey (for performance)
}

func newTLSTransport(cfg *config) (*tlsTransport, error) {
	t := &tlsTransport{
		sessions: make(map[string]cipher.AEAD),
	}

	if len(cfg.localPrivKey) == 32 && len(cfg.localPubKey) == 32 {
		copy(t.localPrivKey[:], cfg.localPrivKey)
		copy(t.localPubKey[:], cfg.localPubKey)
	} else {
		priv, pub, err := GenerateX25519Keypair()
		if err != nil {
			return nil, fmt.Errorf("generate X25519 keypair: %w", err)
		}
		t.localPrivKey = priv
		t.localPubKey = pub
	}

	return t, nil
}

// GenerateX25519Keypair generates a new X25519 keypair for ECDH.
func GenerateX25519Keypair() (privKey [32]byte, pubKey [32]byte, err error) {
	if _, err = io.ReadFull(rand.Reader, privKey[:]); err != nil {
		return privKey, pubKey, fmt.Errorf("read random: %w", err)
	}
	// Clamp the private key per X25519 spec
	privKey[0] &= 248
	privKey[31] &= 127
	privKey[31] |= 64

	pub, err := curve25519.X25519(privKey[:], curve25519.Basepoint)
	if err != nil {
		return privKey, pubKey, fmt.Errorf("compute public key: %w", err)
	}
	copy(pubKey[:], pub)
	return privKey, pubKey, nil
}

// Wrap encrypts data for the given recipient using ephemeral ECDH.
func (t *tlsTransport) Wrap(_ context.Context, data []byte, recipientPubkey []byte) ([]byte, error) {
	if len(recipientPubkey) != 32 {
		return nil, fmt.Errorf("recipient pubkey must be 32 bytes, got %d", len(recipientPubkey))
	}

	// Generate ephemeral keypair for forward secrecy
	ephPriv, ephPub, err := GenerateX25519Keypair()
	if err != nil {
		return nil, err
	}

	// ECDH: shared_secret = X25519(ephemeral_priv, recipient_pub)
	sharedSecret, err := curve25519.X25519(ephPriv[:], recipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("ECDH: %w", err)
	}

	// Derive AES key = SHA-256(shared_secret || ephemeral_pub || recipient_pub)
	aead, err := deriveAEAD(sharedSecret, ephPub[:], recipientPubkey)
	if err != nil {
		return nil, err
	}

	// Generate random nonce
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Encrypt
	ciphertext := aead.Seal(nil, nonce, data, nil)

	// Build wire format: [version][ephemeral_pub][nonce][ciphertext]
	out := make([]byte, 0, headerSize+nonceSize+len(ciphertext))
	out = append(out, currentVersion)
	out = append(out, ephPub[:]...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	return out, nil
}

// Unwrap decrypts data using the local static private key.
func (t *tlsTransport) Unwrap(_ context.Context, data []byte) ([]byte, error) {
	minLen := headerSize + nonceSize + aes.BlockSize
	if len(data) < minLen {
		return nil, fmt.Errorf("encrypted message too short: %d < %d", len(data), minLen)
	}

	version := data[0]
	if version != currentVersion {
		return nil, fmt.Errorf("unsupported encryption version: %d", version)
	}

	ephPub := data[1 : 1+32]
	nonce := data[headerSize : headerSize+nonceSize]
	ciphertext := data[headerSize+nonceSize:]

	// ECDH: shared_secret = X25519(local_priv, ephemeral_pub)
	sharedSecret, err := curve25519.X25519(t.localPrivKey[:], ephPub)
	if err != nil {
		return nil, fmt.Errorf("ECDH decrypt: %w", err)
	}

	// Derive same AES key
	aead, err := deriveAEAD(sharedSecret, ephPub, t.localPubKey[:])
	if err != nil {
		return nil, err
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return plaintext, nil
}

func (t *tlsTransport) Close() error { return nil }

// PublicKey returns the transport's static X25519 public key.
func (t *tlsTransport) PublicKey() []byte {
	return t.localPubKey[:]
}

// deriveAEAD creates an AES-256-GCM AEAD from the ECDH shared secret.
func deriveAEAD(sharedSecret, ephPub, recipientPub []byte) (cipher.AEAD, error) {
	h := sha256.New()
	h.Write(sharedSecret)
	h.Write(ephPub)
	h.Write(recipientPub)
	key := h.Sum(nil)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	return aead, nil
}

// EncryptField encrypts a single string field (e.g., prompt) for a specific recipient.
// Returns the encrypted bytes as a length-prefixed blob that can be embedded in JSON.
func EncryptField(plaintext string, recipientPubkey []byte) ([]byte, error) {
	if len(recipientPubkey) != 32 {
		return nil, fmt.Errorf("recipient pubkey must be 32 bytes")
	}

	ephPriv, ephPub, err := GenerateX25519Keypair()
	if err != nil {
		return nil, err
	}

	sharedSecret, err := curve25519.X25519(ephPriv[:], recipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("ECDH: %w", err)
	}

	aead, err := deriveAEAD(sharedSecret, ephPub[:], recipientPubkey)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, []byte(plaintext), nil)

	// [4B plaintext_len][32B ephemeral_pub][12B nonce][ciphertext+tag]
	out := make([]byte, 4+32+nonceSize+len(ciphertext))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(plaintext)))
	copy(out[4:36], ephPub[:])
	copy(out[36:48], nonce)
	copy(out[48:], ciphertext)

	return out, nil
}

// DecryptField decrypts a field encrypted by EncryptField.
func DecryptField(encrypted []byte, recipientPrivKey [32]byte, recipientPubKey [32]byte) (string, error) {
	if len(encrypted) < 4+32+nonceSize+aes.BlockSize {
		return "", fmt.Errorf("encrypted field too short")
	}

	ephPub := encrypted[4:36]
	nonce := encrypted[36:48]
	ciphertext := encrypted[48:]

	sharedSecret, err := curve25519.X25519(recipientPrivKey[:], ephPub)
	if err != nil {
		return "", fmt.Errorf("ECDH: %w", err)
	}

	aead, err := deriveAEAD(sharedSecret, ephPub, recipientPubKey[:])
	if err != nil {
		return "", err
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt field: %w", err)
	}

	return string(plaintext), nil
}
