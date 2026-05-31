package socks5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
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

func readGreeting(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("read greeting header: %w", err)
	}

	if header[0] != socks5Version {
		return fmt.Errorf("unexpected SOCKS version: 0x%02x", header[0])
	}

	numMethods := int(header[1])
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
