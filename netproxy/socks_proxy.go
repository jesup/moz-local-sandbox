package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

// Minimal SOCKS5 proxy. Spec: RFC 1928. We implement only the bits we need:
//   - greeting with method NO_AUTHENTICATION_REQUIRED (0x00)
//   - CONNECT (cmd 0x01) with IPv4 / DOMAINNAME / IPv6 atyp
//   - UDP ASSOCIATE / BIND are unsupported (cmd not supported)
//
// Why hand-rolled instead of a library: the protocol is tiny (this file is
// under 200 lines), and adding a Go module dependency for one method on one
// handshake isn't worth it.

const (
	socksVersion         = 0x05
	socksAuthNone        = 0x00
	socksAuthNoAcceptable = 0xff
	socksCmdConnect      = 0x01
	socksAtypIPv4        = 0x01
	socksAtypDomain      = 0x03
	socksAtypIPv6        = 0x04
	socksRepSuccess      = 0x00
	socksRepFailure      = 0x01
	socksRepNotAllowed   = 0x02
	socksRepHostUnreach  = 0x04
	socksRepRefused      = 0x05
	socksRepCmdNotSupp   = 0x07
	socksRepAtypNotSupp  = 0x08
)

func startSOCKSProxy(policy *Policy, addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("socks proxy: listen %s: %w", addr, err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go handleSOCKSConn(policy, conn)
		}
	}()
	return ln, nil
}

func handleSOCKSConn(policy *Policy, client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))

	// --- Greeting: VER, NMETHODS, METHODS... ---
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(client, hdr); err != nil {
		return
	}
	if hdr[0] != socksVersion {
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(client, methods); err != nil {
		return
	}
	// We only support no-auth. If the client didn't offer it, refuse.
	if !containsByte(methods, socksAuthNone) {
		_, _ = client.Write([]byte{socksVersion, socksAuthNoAcceptable})
		return
	}
	if _, err := client.Write([]byte{socksVersion, socksAuthNone}); err != nil {
		return
	}

	// --- Request: VER, CMD, RSV, ATYP, ADDR..., PORT ---
	reqHead := make([]byte, 4)
	if _, err := io.ReadFull(client, reqHead); err != nil {
		return
	}
	if reqHead[0] != socksVersion {
		return
	}
	if reqHead[1] != socksCmdConnect {
		writeSOCKSReply(client, socksRepCmdNotSupp)
		return
	}

	var host string
	var dialAddr string
	switch reqHead[3] {
	case socksAtypIPv4:
		buf := make([]byte, 4+2)
		if _, err := io.ReadFull(client, buf); err != nil {
			return
		}
		ip := net.IP(buf[:4]).String()
		port := binary.BigEndian.Uint16(buf[4:6])
		host = ip
		dialAddr = fmt.Sprintf("%s:%d", ip, port)
	case socksAtypDomain:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(client, lb); err != nil {
			return
		}
		buf := make([]byte, int(lb[0])+2)
		if _, err := io.ReadFull(client, buf); err != nil {
			return
		}
		host = string(buf[:int(lb[0])])
		port := binary.BigEndian.Uint16(buf[int(lb[0]):])
		dialAddr = fmt.Sprintf("%s:%d", host, port)
	case socksAtypIPv6:
		buf := make([]byte, 16+2)
		if _, err := io.ReadFull(client, buf); err != nil {
			return
		}
		ip := net.IP(buf[:16]).String()
		port := binary.BigEndian.Uint16(buf[16:18])
		host = ip
		dialAddr = fmt.Sprintf("[%s]:%d", ip, port)
	default:
		writeSOCKSReply(client, socksRepAtypNotSupp)
		return
	}

	if !policy.Allows(host) {
		log.Printf("DENY SOCKS %s", dialAddr)
		writeSOCKSReply(client, socksRepNotAllowed)
		return
	}
	log.Printf("SOCKS %s", dialAddr)

	// Clear the deadline before piping — we want the long-running tunnel
	// to live as long as both sides are alive.
	_ = client.SetDeadline(time.Time{})

	upstream, err := net.DialTimeout("tcp", dialAddr, 10*time.Second)
	if err != nil {
		log.Printf("SOCKS dial %s: %v", dialAddr, err)
		// Map common errors to SOCKS reply codes.
		code := byte(socksRepFailure)
		if isRefused(err) {
			code = socksRepRefused
		} else if isUnreachable(err) {
			code = socksRepHostUnreach
		}
		writeSOCKSReply(client, code)
		return
	}
	defer upstream.Close()

	writeSOCKSReply(client, socksRepSuccess)
	bidirectionalCopy(client, upstream)
}

// writeSOCKSReply sends a CONNECT response with the given status code and
// a zeroed BND.ADDR/BND.PORT (we don't expose the bound address).
func writeSOCKSReply(w io.Writer, status byte) {
	_, _ = w.Write([]byte{
		socksVersion, status, 0x00,
		socksAtypIPv4, 0, 0, 0, 0, // BND.ADDR (0.0.0.0)
		0, 0, // BND.PORT
	})
}

func containsByte(s []byte, b byte) bool {
	for _, x := range s {
		if x == b {
			return true
		}
	}
	return false
}

func isRefused(err error) bool {
	return err != nil && containsLowerSubstr(err.Error(), "refused")
}

func isUnreachable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return containsLowerSubstr(s, "unreachable") ||
		containsLowerSubstr(s, "no route")
}

func containsLowerSubstr(s, sub string) bool {
	// Avoid pulling strings just for this.
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			c := s[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
