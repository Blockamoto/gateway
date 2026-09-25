package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"time"
)

func encodeAddressList(rows []advertisedPeer) []byte {
	var body bytes.Buffer
	count := uint64(0)
	for _, row := range rows {
		host, port, e := net.SplitHostPort(row.Addr)
		if e != nil {
			continue
		}
		ip := net.ParseIP(host)
		p, e := strconv.Atoi(port)
		if ip == nil || e != nil || p <= 0 || p > 65535 {
			continue
		}
		timestamp := row.Timestamp
		if timestamp == 0 {
			timestamp = uint32(time.Now().Unix())
		}
		_ = binary.Write(&body, binary.LittleEndian, timestamp)
		_ = binary.Write(&body, binary.LittleEndian, row.Services)
		body.Write(ip.To16())
		_ = binary.Write(&body, binary.BigEndian, uint16(p))
		count++
	}
	return append(encodeVarInt(count), body.Bytes()...)
}

// addressMessage uses the format this connection negotiated, preserving ports.
func addressMessage(rows []advertisedPeer, v2 bool) (string, []byte) {
	if !v2 {
		return "addr", encodeAddressList(rows)
	}
	var body bytes.Buffer
	count := uint64(0)
	for _, row := range rows {
		host, port, e := net.SplitHostPort(row.Addr)
		if e != nil {
			continue
		}
		ip := net.ParseIP(host)
		p, e := strconv.Atoi(port)
		if ip == nil || e != nil || p < 1 || p > 65535 {
			continue
		}
		timestamp := row.Timestamp
		if timestamp == 0 {
			timestamp = uint32(time.Now().Unix())
		}
		binary.Write(&body, binary.LittleEndian, timestamp)
		body.Write(encodeVarInt(row.Services))
		if ip4 := ip.To4(); ip4 != nil {
			body.WriteByte(1)
			body.WriteByte(4)
			body.Write(ip4)
		} else {
			body.WriteByte(2)
			body.WriteByte(16)
			body.Write(ip.To16())
		}
		binary.Write(&body, binary.BigEndian, uint16(p))
		count++
	}
	return "addrv2", append(encodeVarInt(count), body.Bytes()...)
}
func parseAddressMessage(m message) ([]advertisedPeer, error) {
	if m.command == "addr" {
		return parseAddrPayload(m.payload)
	}
	if m.command != "addrv2" {
		return nil, fmt.Errorf("not an address message")
	}
	// BIP155 IPv4 and IPv6 endpoints are usable on this TCP transport. Other
	// networks are parsed with strict bounds and skipped, not misread as IPs.
	p := m.payload
	n, used, e := decodeCompactSizeMinimal(p)
	if e != nil || n > 1000 {
		return nil, fmt.Errorf("invalid addrv2 count")
	}
	pos := used
	out := []advertisedPeer{}
	for i := uint64(0); i < n; i++ {
		if len(p)-pos < 4 {
			return nil, fmt.Errorf("truncated addrv2 timestamp")
		}
		timestamp := binary.LittleEndian.Uint32(p[pos : pos+4])
		pos += 4
		services, used, e := decodeCompactSizeMinimal(p[pos:])
		if e != nil {
			return nil, e
		}
		pos += used
		if pos >= len(p) {
			return nil, fmt.Errorf("missing addrv2 network")
		}
		network := p[pos]
		pos++
		size, used, e := decodeCompactSizeMinimal(p[pos:])
		if e != nil || size > 512 {
			return nil, fmt.Errorf("invalid addrv2 address size")
		}
		pos += used
		if uint64(len(p)-pos) < size+2 {
			return nil, fmt.Errorf("truncated addrv2 address")
		}
		address := p[pos : pos+int(size)]
		pos += int(size)
		port := binary.BigEndian.Uint16(p[pos : pos+2])
		pos += 2
		expected := map[byte]uint64{1: 4, 2: 16, 3: 10, 4: 32, 5: 32, 6: 16, 7: 16}
		if width, known := expected[network]; known && size != width {
			return nil, fmt.Errorf("invalid IP address size in addrv2")
		}
		if (network == 1 || network == 2) && port != 0 {
			ip := net.IP(address)
			if network == 2 && ip.To4() != nil {
				out = append(out, advertisedPeer{Invalid: "mapped IPv4 in addrv2 IPv6"})
				continue // Ignore mapped encodings, as specified by BIP155.
			}
			out = append(out, advertisedPeer{Addr: net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), Services: services, Timestamp: timestamp})
		} else if port == 0 {
			out = append(out, advertisedPeer{Invalid: "zero port"})
		} else {
			out = append(out, advertisedPeer{Network: network, RawAddress: append([]byte(nil), address...), Port: port, Services: services, Timestamp: timestamp})
		}
	}
	if pos != len(p) {
		return nil, fmt.Errorf("trailing addrv2 bytes")
	}
	return out, nil
}
