package privacy

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// torTransport provides IP anonymity by routing connections through a Tor SOCKS5 proxy.
// It wraps messages with a Tor routing header but does NOT encrypt them;
// for encryption + Tor, use ModeFull which combines both.
//
// The user must run a local Tor daemon (tor or Tor Browser) with SOCKS5
// enabled on the configured address (default 127.0.0.1:9050).
type torTransport struct {
	socksAddr string
	dialer    *socks5Dialer
}

func newTorTransport(cfg *config) (*torTransport, error) {
	addr := cfg.torSocksAddr
	if addr == "" {
		addr = "127.0.0.1:9050"
	}

	t := &torTransport{
		socksAddr: addr,
		dialer:    &socks5Dialer{proxyAddr: addr},
	}

	return t, nil
}

// Wrap passes messages through unmodified — Tor provides IP-level anonymity,
// not message-level encryption. For encryption, layer TLS on top.
func (t *torTransport) Wrap(_ context.Context, data []byte, _ []byte) ([]byte, error) {
	return data, nil
}

// Unwrap passes messages through unmodified.
func (t *torTransport) Unwrap(_ context.Context, data []byte) ([]byte, error) {
	return data, nil
}

// Dialer returns a ProxyDialer for use with libp2p transport configuration.
func (t *torTransport) Dialer() ProxyDialer {
	return t.dialer
}

// SocksAddr returns the SOCKS5 proxy address.
func (t *torTransport) SocksAddr() string {
	return t.socksAddr
}

// CheckConnectivity verifies the Tor SOCKS5 proxy is reachable.
func (t *torTransport) CheckConnectivity(ctx context.Context) error {
	conn, err := t.dialer.DialContext(ctx, "tcp", "check.torproject.org:443")
	if err != nil {
		return fmt.Errorf("tor connectivity check failed (is tor running on %s?): %w", t.socksAddr, err)
	}
	conn.Close()
	return nil
}

func (t *torTransport) Close() error { return nil }

// socks5Dialer implements ProxyDialer for Tor SOCKS5 connections.
// Follows RFC 1928 (SOCKS5 protocol) for proxied TCP connections.
type socks5Dialer struct {
	proxyAddr string
}

func (d *socks5Dialer) ProxyAddr() string {
	return d.proxyAddr
}

// DialContext establishes a SOCKS5 connection through the Tor proxy.
func (d *socks5Dialer) DialContext(ctx context.Context, network, addr string) (io.ReadWriteCloser, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("socks5: only TCP supported, got %s", network)
	}

	dialer := net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", d.proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("socks5: connect to proxy %s: %w", d.proxyAddr, err)
	}

	if err := socks5Handshake(conn, addr); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// socks5Handshake performs the SOCKS5 handshake (RFC 1928) over an established connection.
func socks5Handshake(conn net.Conn, targetAddr string) error {
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return fmt.Errorf("socks5: invalid target address %q: %w", targetAddr, err)
	}

	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return fmt.Errorf("socks5: invalid port %q: %w", portStr, err)
	}

	// 1. Auth negotiation: no authentication
	// VER=0x05, NMETHODS=1, METHOD=0x00 (no auth)
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return fmt.Errorf("socks5: auth request: %w", err)
	}

	authResp := make([]byte, 2)
	if _, err := io.ReadFull(conn, authResp); err != nil {
		return fmt.Errorf("socks5: auth response: %w", err)
	}
	if authResp[0] != 0x05 || authResp[1] != 0x00 {
		return fmt.Errorf("socks5: auth rejected (ver=%d method=%d)", authResp[0], authResp[1])
	}

	// 2. Connect request
	// VER=0x05, CMD=0x01 (CONNECT), RSV=0x00, ATYP=0x03 (domain name)
	req := []byte{0x05, 0x01, 0x00, 0x03}
	req = append(req, byte(len(host)))
	req = append(req, []byte(host)...)
	req = append(req, byte(port>>8), byte(port&0xff))

	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("socks5: connect request: %w", err)
	}

	// 3. Read connect response
	// VER(1) + REP(1) + RSV(1) + ATYP(1) = 4 bytes header
	respHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return fmt.Errorf("socks5: connect response: %w", err)
	}
	if respHeader[1] != 0x00 {
		return fmt.Errorf("socks5: connect failed with reply code %d", respHeader[1])
	}

	// Read and discard bound address
	switch respHeader[3] {
	case 0x01: // IPv4
		discard := make([]byte, 4+2)
		_, _ = io.ReadFull(conn, discard)
	case 0x03: // Domain
		lenBuf := make([]byte, 1)
		_, _ = io.ReadFull(conn, lenBuf)
		discard := make([]byte, int(lenBuf[0])+2)
		_, _ = io.ReadFull(conn, discard)
	case 0x04: // IPv6
		discard := make([]byte, 16+2)
		_, _ = io.ReadFull(conn, discard)
	}

	return nil
}
