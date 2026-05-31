package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

const (
	AddrIPv4   = 0x01
	AddrDomain = 0x03
	AddrIPv6   = 0x04
)

const (
	MsgOpen   = 0x01
	MsgStatus = 0x02
	MsgData   = 0x03
	MsgClose  = 0x04
)

const SessionIDSize = 4

type Frame struct {
	SessionID uint32
	Type      byte
	Payload   []byte
}

func (f Frame) Marshal() []byte {
	n := SessionIDSize + 1 + len(f.Payload)
	buf := make([]byte, n)
	binary.BigEndian.PutUint32(buf[0:4], f.SessionID)
	buf[4] = f.Type
	copy(buf[5:], f.Payload)
	return buf
}

func UnmarshalFrame(data []byte) (Frame, error) {
	if len(data) < SessionIDSize+1 {
		return Frame{}, fmt.Errorf("frame too short: %d bytes", len(data))
	}
	sid := binary.BigEndian.Uint32(data[0:4])
	msgType := data[4]
	var payload []byte
	if len(data) > 5 {
		payload = data[5:]
	}
	return Frame{SessionID: sid, Type: msgType, Payload: payload}, nil
}

func EncodeTarget(addrType byte, addr string, port uint16) ([]byte, error) {
	switch addrType {
	case AddrIPv4:
		ip := net.ParseIP(addr)
		if ip == nil {
			return nil, fmt.Errorf("invalid IPv4: %s", addr)
		}
		ip4 := ip.To4()
		if ip4 == nil {
			return nil, fmt.Errorf("not IPv4: %s", addr)
		}
		buf := make([]byte, 1+4+2)
		buf[0] = AddrIPv4
		copy(buf[1:5], ip4)
		binary.BigEndian.PutUint16(buf[5:7], port)
		return buf, nil

	case AddrDomain:
		if len(addr) > 255 {
			return nil, fmt.Errorf("domain too long: %d", len(addr))
		}
		buf := make([]byte, 1+1+len(addr)+2)
		buf[0] = AddrDomain
		buf[1] = byte(len(addr))
		copy(buf[2:2+len(addr)], addr)
		binary.BigEndian.PutUint16(buf[2+len(addr):], port)
		return buf, nil

	case AddrIPv6:
		ip := net.ParseIP(addr)
		if ip == nil {
			return nil, fmt.Errorf("invalid IPv6: %s", addr)
		}
		ip16 := ip.To16()
		if ip16 == nil {
			return nil, fmt.Errorf("not IPv6: %s", addr)
		}
		buf := make([]byte, 1+16+2)
		buf[0] = AddrIPv6
		copy(buf[1:17], ip16)
		binary.BigEndian.PutUint16(buf[17:19], port)
		return buf, nil

	default:
		return nil, fmt.Errorf("unknown address type: 0x%02x", addrType)
	}
}

func DecodeTarget(data []byte) (addrType byte, addr string, port uint16, err error) {
	if len(data) < 1 {
		return 0, "", 0, errors.New("empty target data")
	}

	addrType = data[0]
	rest := data[1:]

	switch addrType {
	case AddrIPv4:
		if len(rest) < 6 {
			return 0, "", 0, fmt.Errorf("IPv4 need 6 bytes, got %d", len(rest))
		}
		addr = net.IP(rest[:4]).String()
		port = binary.BigEndian.Uint16(rest[4:6])

	case AddrDomain:
		if len(rest) < 1 {
			return 0, "", 0, errors.New("domain: missing length")
		}
		dlen := int(rest[0])
		if len(rest) < 1+dlen+2 {
			return 0, "", 0, fmt.Errorf("domain need %d bytes, got %d", 1+dlen+2, len(rest))
		}
		addr = string(rest[1 : 1+dlen])
		port = binary.BigEndian.Uint16(rest[1+dlen:])

	case AddrIPv6:
		if len(rest) < 18 {
			return 0, "", 0, fmt.Errorf("IPv6 need 18 bytes, got %d", len(rest))
		}
		addr = net.IP(rest[:16]).String()
		port = binary.BigEndian.Uint16(rest[16:18])

	default:
		return 0, "", 0, fmt.Errorf("unknown address type: 0x%02x", addrType)
	}

	return addrType, addr, port, nil
}
