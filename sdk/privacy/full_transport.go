package privacy

import (
	"context"
)

// fullTransport combines Tor anonymity (IP hiding) with TLS encryption
// (message confidentiality). This is the maximum privacy mode.
//
// Data flow:
//
//	Send: plaintext → TLS encrypt → (send via Tor proxy)
//	Recv: (received via Tor proxy) → TLS decrypt → plaintext
type fullTransport struct {
	tls *tlsTransport
	tor *torTransport
}

func newFullTransport(cfg *config) (*fullTransport, error) {
	tls, err := newTLSTransport(cfg)
	if err != nil {
		return nil, err
	}

	tor, err := newTorTransport(cfg)
	if err != nil {
		return nil, err
	}

	return &fullTransport{tls: tls, tor: tor}, nil
}

// Wrap encrypts data with TLS transport. Tor routing happens at the network layer.
func (f *fullTransport) Wrap(ctx context.Context, data []byte, recipientPubkey []byte) ([]byte, error) {
	return f.tls.Wrap(ctx, data, recipientPubkey)
}

// Unwrap decrypts data with TLS transport.
func (f *fullTransport) Unwrap(ctx context.Context, data []byte) ([]byte, error) {
	return f.tls.Unwrap(ctx, data)
}

// Dialer returns the Tor SOCKS5 proxy dialer for network-level anonymity.
func (f *fullTransport) Dialer() ProxyDialer {
	return f.tor.Dialer()
}

// TLS returns the underlying TLS transport for key access.
func (f *fullTransport) TLS() *tlsTransport {
	return f.tls
}

func (f *fullTransport) Close() error {
	f.tls.Close()
	f.tor.Close()
	return nil
}
