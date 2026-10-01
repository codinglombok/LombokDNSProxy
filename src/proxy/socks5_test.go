// Package proxy — LombokDNSProxy · SOCKS5 server tests
//
// These drive the real server over loopback sockets and speak RFC 1928 by
// hand, so the wire format is checked as a client would actually see it
// rather than through the implementation's own helpers.
//
// Everything here stays on 127.0.0.1 — no external network required.
//
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// startSOCKS5 boots a server on a free loopback port and returns its address.
func startSOCKS5(t *testing.T, cfg SOCKS5Config) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg.ListenAddr = addr
	srv := NewSOCKS5Server(cfg, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.ListenAndServe(ctx) //nolint:errcheck

	// Wait for the listener rather than sleeping a fixed amount.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("SOCKS5 server never came up on %s", addr)
	return ""
}

// startEchoServer returns the address of a loopback TCP server that echoes
// whatever it receives — the CONNECT target for relay tests.
func startEchoServer(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) //nolint:errcheck
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// greet performs the SOCKS5 method-selection handshake and returns the
// method byte the server chose.
func greet(t *testing.T, conn net.Conn, methods ...byte) byte {
	t.Helper()
	msg := append([]byte{socks5Version, byte(len(methods))}, methods...)
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("writing greeting: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("reading method selection: %v", err)
	}
	if reply[0] != socks5Version {
		t.Fatalf("server replied with version %d, want %d", reply[0], socks5Version)
	}
	return reply[1]
}

// connectRequest sends a CONNECT for host:port and returns the reply code.
func connectRequest(t *testing.T, conn net.Conn, host string, port uint16) byte {
	t.Helper()

	req := []byte{socks5Version, cmdConnect, 0x00, atypDomain, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("writing CONNECT: %v", err)
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatalf("reading reply header: %v", err)
	}

	// Drain the bound address so the stream is positioned at the payload.
	switch head[3] {
	case atypIPv4:
		io.ReadFull(conn, make([]byte, 4+2)) //nolint:errcheck
	case atypIPv6:
		io.ReadFull(conn, make([]byte, 16+2)) //nolint:errcheck
	case atypDomain:
		l := make([]byte, 1)
		io.ReadFull(conn, l)                          //nolint:errcheck
		io.ReadFull(conn, make([]byte, int(l[0])+2)) //nolint:errcheck
	}
	return head[1]
}

// ─── Method negotiation ───────────────────────────────────────────────────────

func TestSOCKS5_NoAuthAccepted(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if got := greet(t, conn, authNone); got != authNone {
		t.Errorf("server chose method %#x, want authNone (%#x)", got, authNone)
	}
}

func TestSOCKS5_RejectsClientWithoutNoAuth(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Client offers only GSSAPI (0x01), which this server doesn't implement.
	if got := greet(t, conn, 0x01); got != authNoAccept {
		t.Errorf("server chose %#x, want NO ACCEPTABLE METHODS (%#x)", got, authNoAccept)
	}
}

func TestSOCKS5_RequiresPasswordWhenConfigured(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{Username: "user", Password: "pass"})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// An auth-configured server must not accept a no-auth client.
	if got := greet(t, conn, authNone); got != authNoAccept {
		t.Errorf("server accepted method %#x despite credentials being configured", got)
	}
}

func TestSOCKS5_RejectsWrongSOCKSVersion(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// SOCKS4 greeting — server should hang up rather than try to parse it.
	conn.Write([]byte{0x04, 0x01, authNone}) //nolint:errcheck
	conn.SetReadDeadline(time.Now().Add(time.Second))

	if _, err := io.ReadFull(conn, make([]byte, 2)); err == nil {
		t.Error("server replied to a SOCKS4 greeting instead of closing the connection")
	}
}

// ─── Username/password authentication ─────────────────────────────────────────

func authenticateAs(t *testing.T, conn net.Conn, user, pass string) byte {
	t.Helper()
	msg := []byte{0x01, byte(len(user))}
	msg = append(msg, user...)
	msg = append(msg, byte(len(pass)))
	msg = append(msg, pass...)
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("writing auth: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("reading auth reply: %v", err)
	}
	return reply[1]
}

func TestSOCKS5_AuthAcceptsCorrectCredentials(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{Username: "alice", Password: "s3cret"})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if m := greet(t, conn, authPassword); m != authPassword {
		t.Fatalf("method = %#x, want authPassword", m)
	}
	if status := authenticateAs(t, conn, "alice", "s3cret"); status != 0x00 {
		t.Errorf("auth status = %#x, want success (0x00)", status)
	}
}

func TestSOCKS5_AuthRejectsBadPassword(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{Username: "alice", Password: "s3cret"})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greet(t, conn, authPassword)
	if status := authenticateAs(t, conn, "alice", "wrong"); status == 0x00 {
		t.Error("server accepted an incorrect password")
	}
}

func TestSOCKS5_AuthRejectsUnknownUser(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{Username: "alice", Password: "s3cret"})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greet(t, conn, authPassword)
	if status := authenticateAs(t, conn, "mallory", "s3cret"); status == 0x00 {
		t.Error("server accepted an unknown username")
	}
}

// ─── CONNECT and relay ────────────────────────────────────────────────────────

func TestSOCKS5_ConnectRelaysTraffic(t *testing.T) {
	echo := startEchoServer(t)
	addr := startSOCKS5(t, SOCKS5Config{})

	host, portStr, _ := net.SplitHostPort(echo)
	var port uint16
	if _, err := fmtSscan(portStr, &port); err != nil {
		t.Fatalf("parsing echo port: %v", err)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greet(t, conn, authNone)
	if rep := connectRequest(t, conn, host, port); rep != repSuccess {
		t.Fatalf("CONNECT reply = %#x, want success", rep)
	}

	payload := []byte("lombok-dns-proxy relay check")
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("writing through tunnel: %v", err)
	}

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reading echo: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("relayed %q, sent %q", got, payload)
	}
}

func TestSOCKS5_ConnectToDeadPortFails(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{})

	// Reserve then release a port so nothing is listening on it.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	deadPort := uint16(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greet(t, conn, authNone)
	if rep := connectRequest(t, conn, "127.0.0.1", deadPort); rep == repSuccess {
		t.Error("CONNECT to a closed port reported success")
	}
}

func TestSOCKS5_UnsupportedCommandRejected(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greet(t, conn, authNone)

	// BIND is accepted by the grammar but not implemented.
	req := []byte{socks5Version, cmdBind, 0x00, atypIPv4, 127, 0, 0, 1, 0x00, 0x50}
	conn.Write(req) //nolint:errcheck

	reply := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("reading reply: %v", err)
	}
	if reply[1] != repCmdNotSupport {
		t.Errorf("reply = %#x, want COMMAND NOT SUPPORTED (%#x)", reply[1], repCmdNotSupport)
	}
}

func TestSOCKS5_UDPAssociateRejectedWhenDisabled(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{EnableUDP: false})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greet(t, conn, authNone)

	req := []byte{socks5Version, cmdUDPAssoc, 0x00, atypIPv4, 0, 0, 0, 0, 0x00, 0x00}
	conn.Write(req) //nolint:errcheck

	reply := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("reading reply: %v", err)
	}
	if reply[1] != repCmdNotSupport {
		t.Errorf("reply = %#x, want COMMAND NOT SUPPORTED while UDP is off", reply[1])
	}
}

// ─── Address type handling ────────────────────────────────────────────────────

func TestSOCKS5_UnknownAddressTypeRejected(t *testing.T) {
	addr := startSOCKS5(t, SOCKS5Config{})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	greet(t, conn, authNone)

	// 0x09 is not a defined ATYP.
	conn.Write([]byte{socks5Version, cmdConnect, 0x00, 0x09}) //nolint:errcheck

	reply := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("reading reply: %v", err)
	}
	if reply[1] != repAddrNotSupport {
		t.Errorf("reply = %#x, want ADDRESS TYPE NOT SUPPORTED (%#x)", reply[1], repAddrNotSupport)
	}
}

// ─── writeReply shape ─────────────────────────────────────────────────────────

func TestWriteReply_NilIPEmitsZeroedIPv4(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		writeReply(server, repSuccess, nil, 0)
		server.Close()
	}()

	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("reading reply: %v", err)
	}

	want := []byte{socks5Version, repSuccess, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	if len(got) != len(want) {
		t.Fatalf("reply length %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("byte %d = %#x, want %#x", i, got[i], want[i])
		}
	}
}

func TestWriteReply_IPv4CarriesAddressAndPort(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		writeReply(server, repSuccess, net.IPv4(192, 0, 2, 33), 8080)
		server.Close()
	}()

	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("reading reply: %v", err)
	}
	if got[3] != atypIPv4 {
		t.Fatalf("ATYP = %#x, want IPv4", got[3])
	}
	if ip := net.IP(got[4:8]).String(); ip != "192.0.2.33" {
		t.Errorf("bound address = %s, want 192.0.2.33", ip)
	}
	if port := binary.BigEndian.Uint16(got[8:10]); port != 8080 {
		t.Errorf("bound port = %d, want 8080", port)
	}
}

// fmtSscan parses a decimal port without dragging fmt into the import list
// for a single call.
func fmtSscan(s string, out *uint16) (int, error) {
	var v uint32
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, &net.AddrError{Err: "non-numeric port", Addr: s}
		}
		v = v*10 + uint32(c-'0')
	}
	*out = uint16(v)
	return 1, nil
}
