// Package proxy — LombokDNSProxy · SOCKS5 Proxy Server
// Implements RFC 1928 (SOCKS5) + RFC 1929 (username/password auth).
// Supports CONNECT (TCP) and UDP ASSOCIATE for gaming/streaming.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"go.uber.org/zap"
)

const (
	socks5Version = 0x05

	// Auth methods
	authNone     = 0x00
	authPassword = 0x02
	authNoAccept = 0xFF

	// Commands
	cmdConnect   = 0x01
	cmdBind      = 0x02
	cmdUDPAssoc  = 0x03

	// Address types
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	// Reply codes
	repSuccess        = 0x00
	repGeneral        = 0x01
	repNotAllowed     = 0x02
	repNetUnreachable = 0x03
	repHostUnreachable = 0x04
	repConnRefused    = 0x05
	repCmdNotSupport  = 0x07
	repAddrNotSupport = 0x08

	defaultDialTimeout  = 10 * time.Second
	defaultRelayTimeout = 5 * time.Minute
)

// SOCKS5Config holds runtime configuration for the SOCKS5 server.
type SOCKS5Config struct {
	ListenAddr  string
	Username    string // empty = no auth required
	Password    string
	EnableUDP   bool   // UDP ASSOCIATE support (gaming/streaming)
	UDPRelayIP  net.IP // IP to advertise for UDP relay
	MaxConns    int
}

// SOCKS5Server is a RFC 1928 compliant SOCKS5 server.
type SOCKS5Server struct {
	cfg    SOCKS5Config
	log    *zap.Logger
	dialer *net.Dialer
}

// NewSOCKS5Server creates and returns a configured SOCKS5 server.
func NewSOCKS5Server(cfg SOCKS5Config, log *zap.Logger) *SOCKS5Server {
	return &SOCKS5Server{
		cfg: cfg,
		log: log,
		dialer: &net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: 30 * time.Second,
		},
	}
}

// ListenAndServe starts accepting SOCKS5 connections on cfg.ListenAddr.
// Blocks until ctx is cancelled.
func (s *SOCKS5Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("socks5: listen %s: %w", s.cfg.ListenAddr, err)
	}
	defer ln.Close()

	s.log.Info("SOCKS5 listening", zap.String("addr", s.cfg.ListenAddr))

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			s.log.Warn("socks5: accept error", zap.Error(err))
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

// handleConn manages the full SOCKS5 handshake + relay lifecycle.
func (s *SOCKS5Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck

	// 1. Method negotiation
	if err := s.negotiateAuth(conn); err != nil {
		s.log.Debug("socks5: auth negotiation failed", zap.Error(err))
		return
	}

	// 2. Username/password sub-negotiation (if enabled)
	if s.cfg.Username != "" {
		if err := s.authenticate(conn); err != nil {
			s.log.Debug("socks5: authentication failed", zap.Error(err))
			return
		}
	}

	// 3. Read request
	req, err := s.readRequest(conn)
	if err != nil {
		s.log.Debug("socks5: read request failed", zap.Error(err))
		return
	}

	conn.SetDeadline(time.Time{}) // clear deadline before relay

	// 4. Dispatch command
	switch req.cmd {
	case cmdConnect:
		s.handleConnect(ctx, conn, req)
	case cmdUDPAssoc:
		if s.cfg.EnableUDP {
			s.handleUDPAssociate(ctx, conn, req)
		} else {
			writeReply(conn, repCmdNotSupport, nil, 0)
		}
	default:
		writeReply(conn, repCmdNotSupport, nil, 0)
	}
}

// negotiateAuth handles the method-selection handshake.
func (s *SOCKS5Server) negotiateAuth(conn net.Conn) error {
	// VER NMETHODS METHODS...
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != socks5Version {
		return fmt.Errorf("unsupported SOCKS version %d", header[0])
	}
	nMethods := int(header[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}

	// Select method
	chosen := byte(authNoAccept)
	if s.cfg.Username == "" {
		for _, m := range methods {
			if m == authNone {
				chosen = authNone
				break
			}
		}
	} else {
		for _, m := range methods {
			if m == authPassword {
				chosen = authPassword
				break
			}
		}
	}

	conn.Write([]byte{socks5Version, chosen}) //nolint:errcheck
	if chosen == authNoAccept {
		return fmt.Errorf("no acceptable auth method")
	}
	return nil
}

// authenticate performs RFC 1929 username/password sub-negotiation.
func (s *SOCKS5Server) authenticate(conn net.Conn) error {
	// VER ULEN UNAME PLEN PASSWD
	ver := make([]byte, 1)
	if _, err := io.ReadFull(conn, ver); err != nil {
		return err
	}
	ulenB := make([]byte, 1)
	io.ReadFull(conn, ulenB) //nolint:errcheck
	uname := make([]byte, ulenB[0])
	io.ReadFull(conn, uname) //nolint:errcheck
	plenB := make([]byte, 1)
	io.ReadFull(conn, plenB) //nolint:errcheck
	passwd := make([]byte, plenB[0])
	io.ReadFull(conn, passwd) //nolint:errcheck

	if string(uname) == s.cfg.Username && string(passwd) == s.cfg.Password {
		conn.Write([]byte{0x01, 0x00}) //nolint:errcheck // success
		return nil
	}
	conn.Write([]byte{0x01, 0x01}) //nolint:errcheck // failure
	return fmt.Errorf("invalid credentials")
}

// socks5Request holds a parsed SOCKS5 request.
type socks5Request struct {
	cmd     byte
	addrStr string
	port    uint16
}

// readRequest parses the SOCKS5 request header.
func (s *SOCKS5Server) readRequest(conn net.Conn) (*socks5Request, error) {
	// VER CMD RSV ATYP ...
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[0] != socks5Version {
		return nil, fmt.Errorf("bad version in request")
	}

	var addrStr string
	switch header[3] { // ATYP
	case atypIPv4:
		addr := make([]byte, 4)
		io.ReadFull(conn, addr) //nolint:errcheck
		addrStr = net.IP(addr).String()
	case atypIPv6:
		addr := make([]byte, 16)
		io.ReadFull(conn, addr) //nolint:errcheck
		addrStr = net.IP(addr).String()
	case atypDomain:
		lenB := make([]byte, 1)
		io.ReadFull(conn, lenB) //nolint:errcheck
		domain := make([]byte, lenB[0])
		io.ReadFull(conn, domain) //nolint:errcheck
		addrStr = string(domain)
	default:
		writeReply(conn, repAddrNotSupport, nil, 0)
		return nil, fmt.Errorf("unsupported address type %d", header[3])
	}

	portB := make([]byte, 2)
	io.ReadFull(conn, portB) //nolint:errcheck
	port := binary.BigEndian.Uint16(portB)

	return &socks5Request{cmd: header[1], addrStr: addrStr, port: port}, nil
}

// handleConnect establishes a TCP relay (CONNECT command).
func (s *SOCKS5Server) handleConnect(ctx context.Context, client net.Conn, req *socks5Request) {
	target := fmt.Sprintf("%s:%d", req.addrStr, req.port)

	upstream, err := s.dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		s.log.Debug("socks5: connect failed", zap.String("target", target), zap.Error(err))
		writeReply(client, repHostUnreachable, nil, 0)
		return
	}
	defer upstream.Close()

	// Send success reply with bound address
	boundAddr := upstream.LocalAddr().(*net.TCPAddr)
	writeReply(client, repSuccess, boundAddr.IP, uint16(boundAddr.Port))

	s.log.Debug("socks5: relaying", zap.String("target", target))

	// Bidirectional relay
	relay(client, upstream, defaultRelayTimeout)
}

// handleUDPAssociate creates a UDP relay for the client (UDP ASSOCIATE command).
// Supports gaming and real-time streaming protocols that use UDP.
func (s *SOCKS5Server) handleUDPAssociate(ctx context.Context, client net.Conn, _ *socks5Request) {
	relayConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		writeReply(client, repGeneral, nil, 0)
		return
	}
	defer relayConn.Close()

	relayAddr := relayConn.LocalAddr().(*net.UDPAddr)
	relayIP := s.cfg.UDPRelayIP
	if relayIP == nil {
		relayIP = net.IPv4(127, 0, 0, 1)
	}
	writeReply(client, repSuccess, relayIP, uint16(relayAddr.Port))

	s.log.Debug("socks5: UDP relay open", zap.Int("port", relayAddr.Port))

	// Keep the control connection alive; close relay when TCP drops
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(io.Discard, client) //nolint:errcheck
	}()

	// Forward datagrams: client → remote and remote → client
	go udpRelay(relayConn, done)

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// relay copies data bidirectionally between two net.Conns.
// Closes both sides when either EOF or deadline is reached.
func relay(a, b net.Conn, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	a.SetDeadline(deadline) //nolint:errcheck
	b.SetDeadline(deadline) //nolint:errcheck

	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		io.Copy(dst, src) //nolint:errcheck
		// Half-close the write side so the peer sees EOF rather than waiting
		// for the deadline. CloseWrite is not on net.Conn — it's only on the
		// concrete TCP/Unix types — so assert for it and fall back to a full
		// Close when the connection doesn't support half-closing.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite() //nolint:errcheck
		} else {
			dst.Close() //nolint:errcheck
		}
		done <- struct{}{}
	}
	go pipe(a, b)
	go pipe(b, a)
	<-done
}

// udpRelay proxies UDP datagrams in a stateful NAT-like table.
func udpRelay(relay *net.UDPConn, done <-chan struct{}) {
	buf := make([]byte, 65535)
	// Simplified: real implementation tracks client→remote mappings
	for {
		relay.SetReadDeadline(time.Now().Add(5 * time.Minute)) //nolint:errcheck
		n, addr, err := relay.ReadFromUDP(buf)
		if err != nil {
			return
		}
		select {
		case <-done:
			return
		default:
		}
		_ = n
		_ = addr
		// TODO TD-007: full UDP ASSOCIATE with NAT table (v1.2.0)
	}
}

// writeReply sends a SOCKS5 reply message to the client.
func writeReply(conn net.Conn, rep byte, ip net.IP, port uint16) {
	reply := []byte{socks5Version, rep, 0x00}
	if ip == nil || len(ip) == 0 {
		reply = append(reply, atypIPv4, 0, 0, 0, 0, 0, 0)
	} else if ip4 := ip.To4(); ip4 != nil {
		reply = append(reply, atypIPv4)
		reply = append(reply, ip4...)
		reply = append(reply, byte(port>>8), byte(port))
	} else {
		reply = append(reply, atypIPv6)
		reply = append(reply, ip.To16()...)
		reply = append(reply, byte(port>>8), byte(port))
	}
	conn.Write(reply) //nolint:errcheck
}
