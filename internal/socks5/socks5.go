package socks5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"socks5-ws-proxy/internal/protocol"
)

const (
	socks5Version = 0x05

	CmdConnect = 0x01

	RepSuccess          = 0x00
	RepGeneralFailure   = 0x01
	RepConnNotAllowed   = 0x02
	RepNetworkUnreach   = 0x03
	RepHostUnreach      = 0x04
	RepConnRefused      = 0x05
	RepTTLExpired       = 0x06
	RepCmdNotSupported  = 0x07
	RepAddrNotSupported = 0x08
)

func Handshake(conn net.Conn) (addrType byte, addr string, port uint16, err error) {
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer conn.SetDeadline(time.Time{})

	peek := make([]byte, 1)
	if _, err := io.ReadFull(conn, peek); err != nil {
		return 0, "", 0, fmt.Errorf("read version: %w", err)
	}

	switch peek[0] {
	case 0x04:
		return handshakeSOCKS4a(conn)
	case socks5Version:
		return handshakeSOCKS5(conn)
	default:
		return 0, "", 0, fmt.Errorf("unexpected SOCKS version: 0x%02x", peek[0])
	}
}

func SendReply(conn net.Conn, rep byte) error {
	reply := []byte{
		socks5Version,
		rep,
		0x00,
		0x01,
		0, 0, 0, 0,
		0, 0,
	}
	_, err := conn.Write(reply)
	return err
}

func handshakeSOCKS5(conn net.Conn) (addrType byte, addr string, port uint16, err error) {
	if err := readGreeting(conn); err != nil {
		return 0, "", 0, fmt.Errorf("greeting: %w", err)
	}

	if err := writeMethodReply(conn); err != nil {
		return 0, "", 0, fmt.Errorf("method reply: %w", err)
	}

	addrType, addr, port, err = readConnectRequest(conn)
	if err != nil {
		return 0, "", 0, fmt.Errorf("connect request: %w", err)
	}

	return addrType, addr, port, nil
}

func handshakeSOCKS4a(conn net.Conn) (addrType byte, addr string, port uint16, err error) {
	rest := make([]byte, 7)
	if _, err := io.ReadFull(conn, rest); err != nil {
		return 0, "", 0, fmt.Errorf("read socks4 request: %w", err)
	}

	port = binary.BigEndian.Uint16(rest[0:2])

	if rest[2] != 0x01 {
		return 0, "", 0, fmt.Errorf("socks4: unsupported command: 0x%02x", rest[2])
	}

	ip := rest[3:7]

	userID := make([]byte, 256)
	var userIDLen int
	for {
		b := make([]byte, 1)
		if _, err := io.ReadFull(conn, b); err != nil {
			return 0, "", 0, fmt.Errorf("read socks4 userid: %w", err)
		}
		if b[0] == 0x00 {
			break
		}
		if userIDLen < len(userID) {
			userID[userIDLen] = b[0]
			userIDLen++
		}
	}

	if ip[0] == 0x00 && ip[1] == 0x00 && ip[2] == 0x00 && ip[3] != 0x00 {
		var domain strings.Builder
		for {
			b := make([]byte, 1)
			if _, err := io.ReadFull(conn, b); err != nil {
				return 0, "", 0, fmt.Errorf("read socks4a domain: %w", err)
			}
			if b[0] == 0x00 {
				break
			}
			domain.WriteByte(b[0])
		}
		addr = domain.String()
		addrType = protocol.AddrDomain
	} else {
		addr = net.IP(ip).String()
		addrType = protocol.AddrIPv4
	}

	reply := []byte{0x00, 0x5a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := conn.Write(reply); err != nil {
		return 0, "", 0, fmt.Errorf("socks4 reply: %w", err)
	}

	return addrType, addr, port, nil
}

func readGreeting(conn net.Conn) error {
	numMethodsBuf := make([]byte, 1)
	if _, err := io.ReadFull(conn, numMethodsBuf); err != nil {
		return fmt.Errorf("read num methods: %w", err)
	}

	numMethods := int(numMethodsBuf[0])
	if numMethods == 0 {
		return errors.New("no auth methods offered")
	}

	methods := make([]byte, numMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return fmt.Errorf("read methods: %w", err)
	}

	hasNoAuth := false
	for _, m := range methods {
		if m == 0x00 {
			hasNoAuth = true
			break
		}
	}
	if !hasNoAuth {
		return errors.New("client does not support no-auth method")
	}

	return nil
}

func writeMethodReply(conn net.Conn) error {
	_, err := conn.Write([]byte{socks5Version, 0x00})
	return err
}

func readConnectRequest(conn net.Conn) (addrType byte, addr string, port uint16, err error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, "", 0, fmt.Errorf("read request header: %w", err)
	}

	if header[0] != socks5Version {
		return 0, "", 0, fmt.Errorf("unexpected SOCKS version in request: 0x%02x", header[0])
	}

	if header[1] != CmdConnect {
		return 0, "", 0, fmt.Errorf("unsupported command: 0x%02x", header[1])
	}

	addrType = header[3]

	switch addrType {
	case protocol.AddrIPv4:
		ipBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return 0, "", 0, fmt.Errorf("read IPv4: %w", err)
		}
		addr = net.IP(ipBuf).String()

	case protocol.AddrDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return 0, "", 0, fmt.Errorf("read domain length: %w", err)
		}
		domainBuf := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(conn, domainBuf); err != nil {
			return 0, "", 0, fmt.Errorf("read domain: %w", err)
		}
		addr = string(domainBuf)

	case protocol.AddrIPv6:
		ipBuf := make([]byte, 16)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return 0, "", 0, fmt.Errorf("read IPv6: %w", err)
		}
		addr = net.IP(ipBuf).String()

	default:
		return 0, "", 0, fmt.Errorf("unsupported address type: 0x%02x", addrType)
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return 0, "", 0, fmt.Errorf("read port: %w", err)
	}
	port = binary.BigEndian.Uint16(portBuf)

	return addrType, addr, port, nil
}
