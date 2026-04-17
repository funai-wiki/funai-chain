package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestGenerateX25519Keypair(t *testing.T) {
	priv, pub, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}
	if priv == [32]byte{} {
		t.Fatal("private key is all zeros")
	}
	if pub == [32]byte{} {
		t.Fatal("public key is all zeros")
	}

	// Two keypairs should differ
	priv2, pub2, err := GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("second GenerateX25519Keypair: %v", err)
	}
	if priv == priv2 {
		t.Fatal("two private keys are identical")
	}
	if pub == pub2 {
		t.Fatal("two public keys are identical")
	}
}

func TestTLSTransportRoundtrip(t *testing.T) {
	ctx := context.Background()

	// Sender and recipient with different keypairs
	senderPriv, senderPub, _ := GenerateX25519Keypair()
	recipPriv, recipPub, _ := GenerateX25519Keypair()

	sender, err := newTLSTransport(&config{
		localPrivKey: senderPriv[:],
		localPubKey:  senderPub[:],
	})
	if err != nil {
		t.Fatalf("create sender transport: %v", err)
	}

	recip, err := newTLSTransport(&config{
		localPrivKey: recipPriv[:],
		localPubKey:  recipPub[:],
	})
	if err != nil {
		t.Fatalf("create recipient transport: %v", err)
	}

	plaintext := []byte("Hello, this is a secret prompt about quantum computing!")

	// Sender encrypts for recipient
	encrypted, err := sender.Wrap(ctx, plaintext, recipPub[:])
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}

	if bytes.Equal(encrypted, plaintext) {
		t.Fatal("encrypted data equals plaintext")
	}
	if len(encrypted) <= len(plaintext) {
		t.Fatal("encrypted data should be longer than plaintext")
	}

	// Recipient decrypts
	decrypted, err := recip.Unwrap(ctx, encrypted)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted does not match: got %q, want %q", decrypted, plaintext)
	}
}

func TestTLSTransportForwardSecrecy(t *testing.T) {
	ctx := context.Background()
	_, recipPub, _ := GenerateX25519Keypair()

	senderPriv, senderPub, _ := GenerateX25519Keypair()
	sender, _ := newTLSTransport(&config{
		localPrivKey: senderPriv[:],
		localPubKey:  senderPub[:],
	})

	plaintext := []byte("same message")
	enc1, _ := sender.Wrap(ctx, plaintext, recipPub[:])
	enc2, _ := sender.Wrap(ctx, plaintext, recipPub[:])

	// Each encryption should produce different ciphertext (ephemeral keys)
	if bytes.Equal(enc1, enc2) {
		t.Fatal("two encryptions of the same message produced identical ciphertext (no forward secrecy)")
	}
}

func TestTLSTransportWrongKey(t *testing.T) {
	ctx := context.Background()

	senderPriv, senderPub, _ := GenerateX25519Keypair()
	_, recipPub, _ := GenerateX25519Keypair()
	wrongPriv, wrongPub, _ := GenerateX25519Keypair()

	sender, _ := newTLSTransport(&config{
		localPrivKey: senderPriv[:],
		localPubKey:  senderPub[:],
	})

	wrongRecip, _ := newTLSTransport(&config{
		localPrivKey: wrongPriv[:],
		localPubKey:  wrongPub[:],
	})

	plaintext := []byte("secret data")
	encrypted, _ := sender.Wrap(ctx, plaintext, recipPub[:])

	// Wrong recipient should fail to decrypt
	_, err := wrongRecip.Unwrap(ctx, encrypted)
	if err == nil {
		t.Fatal("expected decryption to fail with wrong key")
	}
}

func TestTLSTransportEmptyMessage(t *testing.T) {
	ctx := context.Background()

	recipPriv, recipPub, _ := GenerateX25519Keypair()
	senderPriv, senderPub, _ := GenerateX25519Keypair()

	sender, _ := newTLSTransport(&config{
		localPrivKey: senderPriv[:],
		localPubKey:  senderPub[:],
	})
	recip, _ := newTLSTransport(&config{
		localPrivKey: recipPriv[:],
		localPubKey:  recipPub[:],
	})

	encrypted, err := sender.Wrap(ctx, []byte{}, recipPub[:])
	if err != nil {
		t.Fatalf("Wrap empty: %v", err)
	}

	decrypted, err := recip.Unwrap(ctx, encrypted)
	if err != nil {
		t.Fatalf("Unwrap empty: %v", err)
	}

	if len(decrypted) != 0 {
		t.Fatalf("expected empty, got %d bytes", len(decrypted))
	}
}

func TestTLSTransportLargeMessage(t *testing.T) {
	ctx := context.Background()

	recipPriv, recipPub, _ := GenerateX25519Keypair()
	senderPriv, senderPub, _ := GenerateX25519Keypair()

	sender, _ := newTLSTransport(&config{
		localPrivKey: senderPriv[:],
		localPubKey:  senderPub[:],
	})
	recip, _ := newTLSTransport(&config{
		localPrivKey: recipPriv[:],
		localPubKey:  recipPub[:],
	})

	// 1MB message (simulating a long prompt)
	plaintext := make([]byte, 1024*1024)
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	encrypted, err := sender.Wrap(ctx, plaintext, recipPub[:])
	if err != nil {
		t.Fatalf("Wrap large: %v", err)
	}

	decrypted, err := recip.Unwrap(ctx, encrypted)
	if err != nil {
		t.Fatalf("Unwrap large: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("large message roundtrip failed")
	}
}

func TestEncryptDecryptField(t *testing.T) {
	recipPriv, recipPub, _ := GenerateX25519Keypair()

	plaintext := "What is the meaning of life?"
	encrypted, err := EncryptField(plaintext, recipPub[:])
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}

	decrypted, err := DecryptField(encrypted, recipPriv, recipPub)
	if err != nil {
		t.Fatalf("DecryptField: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("field roundtrip: got %q, want %q", decrypted, plaintext)
	}
}

func TestPlainTransport(t *testing.T) {
	ctx := context.Background()
	tp, err := NewTransport(ModePlain)
	if err != nil {
		t.Fatalf("NewTransport plain: %v", err)
	}
	defer tp.Close()

	data := []byte("hello world")
	wrapped, err := tp.Wrap(ctx, data, nil)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if !bytes.Equal(wrapped, data) {
		t.Fatal("plain transport should not modify data")
	}

	unwrapped, err := tp.Unwrap(ctx, wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(unwrapped, data) {
		t.Fatal("plain unwrap should return original")
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
		err   bool
	}{
		{"plain", ModePlain, false},
		{"", ModeTLS, false},
		{"tls", ModeTLS, false},
		{"tor", ModeTor, false},
		{"full", ModeFull, false},
		{"invalid", ModePlain, true},
	}

	for _, tt := range tests {
		got, err := ParseMode(tt.input)
		if tt.err && err == nil {
			t.Errorf("ParseMode(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("ParseMode(%q): unexpected error: %v", tt.input, err)
		}
		if !tt.err && got != tt.want {
			t.Errorf("ParseMode(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestModeString(t *testing.T) {
	modes := map[Mode]string{
		ModePlain: "plain",
		ModeTLS:   "tls",
		ModeTor:   "tor",
		ModeFull:  "full",
	}
	for m, want := range modes {
		if got := m.String(); got != want {
			t.Errorf("Mode(%d).String() = %q, want %q", m, got, want)
		}
	}
}

func TestEnvelopeSealOpen(t *testing.T) {
	recipPriv, recipPub, _ := GenerateX25519Keypair()
	senderPriv, senderPub, _ := GenerateX25519Keypair()
	_ = senderPriv

	data := []byte(`{"prompt":"hello","model":"llama-3-70b"}`)

	env, err := Seal(data, senderPub[:], recipPub[:])
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if !env.IsEncrypted {
		t.Fatal("envelope should be marked as encrypted")
	}

	plaintext, err := Open(env, recipPriv, recipPub)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if !bytes.Equal(plaintext, data) {
		t.Fatalf("envelope roundtrip: got %q, want %q", plaintext, data)
	}
}

func TestEnvelopeJSON(t *testing.T) {
	recipPriv, recipPub, _ := GenerateX25519Keypair()
	_, senderPub, _ := GenerateX25519Keypair()

	type TestMsg struct {
		Prompt string `json:"prompt"`
		Fee    int    `json:"fee"`
	}

	orig := TestMsg{Prompt: "Tell me about Cosmos SDK", Fee: 1000}

	envData, err := SealJSON(orig, senderPub[:], recipPub[:])
	if err != nil {
		t.Fatalf("SealJSON: %v", err)
	}

	// Verify it's valid JSON
	var rawEnv map[string]interface{}
	if err := json.Unmarshal(envData, &rawEnv); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}

	var decoded TestMsg
	if err := OpenJSON(envData, recipPriv, recipPub, &decoded); err != nil {
		t.Fatalf("OpenJSON: %v", err)
	}

	if decoded.Prompt != orig.Prompt || decoded.Fee != orig.Fee {
		t.Fatalf("JSON roundtrip: got %+v, want %+v", decoded, orig)
	}
}

func TestPlainEnvelope(t *testing.T) {
	data := []byte("unencrypted message")
	env := PlainEnvelope(data)

	if env.IsEncrypted {
		t.Fatal("plain envelope should not be encrypted")
	}

	recipPriv, recipPub, _ := GenerateX25519Keypair()
	plaintext, err := Open(env, recipPriv, recipPub)
	if err != nil {
		t.Fatalf("Open plain: %v", err)
	}

	if !bytes.Equal(plaintext, data) {
		t.Fatal("plain envelope roundtrip failed")
	}
}

func TestTLSTransportAutoKeygen(t *testing.T) {
	// When no keys provided, should auto-generate
	tp, err := newTLSTransport(&config{})
	if err != nil {
		t.Fatalf("auto-keygen: %v", err)
	}

	if tp.localPrivKey == [32]byte{} {
		t.Fatal("auto-generated private key is all zeros")
	}
	if tp.localPubKey == [32]byte{} {
		t.Fatal("auto-generated public key is all zeros")
	}
}

func TestTorTransportCreation(t *testing.T) {
	tp, err := newTorTransport(&config{torSocksAddr: "127.0.0.1:9999"})
	if err != nil {
		t.Fatalf("newTorTransport: %v", err)
	}

	if tp.SocksAddr() != "127.0.0.1:9999" {
		t.Fatalf("SocksAddr: got %q, want %q", tp.SocksAddr(), "127.0.0.1:9999")
	}

	dialer := tp.Dialer()
	if dialer.ProxyAddr() != "127.0.0.1:9999" {
		t.Fatalf("Dialer.ProxyAddr: got %q", dialer.ProxyAddr())
	}
}

func BenchmarkTLSWrapUnwrap(b *testing.B) {
	ctx := context.Background()
	recipPriv, recipPub, _ := GenerateX25519Keypair()
	senderPriv, senderPub, _ := GenerateX25519Keypair()

	sender, _ := newTLSTransport(&config{
		localPrivKey: senderPriv[:],
		localPubKey:  senderPub[:],
	})
	recip, _ := newTLSTransport(&config{
		localPrivKey: recipPriv[:],
		localPubKey:  recipPub[:],
	})

	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encrypted, _ := sender.Wrap(ctx, data, recipPub[:])
		_, _ = recip.Unwrap(ctx, encrypted)
	}
}
