package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
)

// bitcoinP2PServingView describes the deliberately narrow ordinary-Bitcoin
// service exposed on the same listener as Gateway On Demand. It is not a claim
// that Gateway Client is a full validating Bitcoin node.
type bitcoinP2PServingView struct {
	Enabled              bool     `json:"enabled"`
	ListenPort           int      `json:"listen_port"`
	NODE_NETWORK         bool     `json:"node_network"`
	NODE_NETWORK_LIMITED bool     `json:"node_network_limited"`
	NODE_WITNESS         bool     `json:"node_witness"`
	Services             []string `json:"services"`
	GetDataRequests      uint64   `json:"getdata_requests"`
	BlocksServed         uint64   `json:"blocks_served"`
	NotFound             uint64   `json:"not_found"`
	Note                 string   `json:"note"`
}

func bitcoinListenerEnabled(s appSettings) bool {
	return s.ServeData || (releaseFeatureAvailable("gateway-peerhood") && (s.ServeGatewayData || (s.SatlineEnabled && s.SatlineServePublished)))
}

// standardServingServiceFlags advertises only standard service semantics that
// this listener really satisfies. BOD can return witness-inclusive block data
// for blocks it possesses, so NODE_WITNESS is accurate. It intentionally does
// NOT advertise NODE_NETWORK/NODE_NETWORK_LIMITED: sparse possession is not a
// claim of full historical service.
func standardServingServiceFlags() uint64 {
	return nodeWitnessService
}

func (s *overlayServer) bitcoinServingStatus() bitcoinP2PServingView {
	s.app.settingsMu.RLock()
	allowed := s.app.settings.ServeData
	s.app.settingsMu.RUnlock()
	flags, _ := s.app.localBitcoinAdvertisement()
	s.mu.Lock()
	defer s.mu.Unlock()
	port := overlayTCPPort
	if s.tcp != nil {
		_, p, _ := net.SplitHostPort(s.tcp.Addr().String())
		if n, e := strconv.Atoi(p); e == nil {
			port = n
		}
	}
	services := []string{"getheaders", "getblocks", "getdata(MSG_BLOCK)", "getdata(MSG_WITNESS_BLOCK)"}
	if flags&nodeNetworkService != 0 {
		services = append(services, "NODE_NETWORK (Core-backed)")
	}
	if flags&nodeNetworkLimitedService != 0 {
		services = append(services, "NODE_NETWORK_LIMITED (pruned Core-backed)")
	}
	if flags&nodeWitnessService != 0 {
		services = append(services, "NODE_WITNESS")
	}
	return bitcoinP2PServingView{
		Enabled:              allowed && s.tcp != nil,
		ListenPort:           port,
		NODE_NETWORK:         flags&nodeNetworkService != 0,
		NODE_NETWORK_LIMITED: flags&nodeNetworkLimitedService != 0,
		NODE_WITNESS:         flags&nodeWitnessService != 0,
		Services:             services,
		GetDataRequests:      s.standardGetDataRequests,
		BlocksServed:         s.standardBlocksServed,
		NotFound:             s.standardBlockMisses,
		Note:                 "Bitcoin block/header service uses explicit local providers. NODE_NETWORK is advertised only with a checked, ready, unpruned Core backing provider; a checked, ready pruned Core can advertise NODE_NETWORK_LIMITED for its latest 288 blocks. Sparse storage advertises neither coverage promise.",
	}
}

// stripBlockWitness converts a witness-serialized block into the serialization
// required by a MSG_BLOCK getdata response. The header, transaction count and
// each transaction's legacy serialization are preserved byte-for-byte where
// possible; marker/flag and witness stacks are omitted. BIP144 reserves the
// witness-inclusive serialization for MSG_WITNESS_BLOCK requests.
func stripBlockWitness(block []byte) ([]byte, error) {
	if len(block) < 81 {
		return nil, fmt.Errorf("short block")
	}
	p := &byteParser{b: block, off: 80}
	txCount, txCountRaw, err := p.varIntRaw()
	if err != nil {
		return nil, err
	}
	if txCount > 100000 {
		return nil, fmt.Errorf("implausible transaction count %d", txCount)
	}
	var out bytes.Buffer
	out.Write(block[:80])
	out.Write(txCountRaw)
	for i := uint64(0); i < txCount; i++ {
		version, err := p.read(4)
		if err != nil {
			return nil, err
		}
		out.Write(version)

		segwit := false
		if p.remaining() >= 2 && p.b[p.off] == 0x00 && p.b[p.off+1] != 0x00 {
			segwit = true
			if _, err := p.read(2); err != nil {
				return nil, err
			}
		}

		vinCount, vinRaw, err := p.varIntRaw()
		if err != nil {
			return nil, err
		}
		if vinCount > 100000 {
			return nil, fmt.Errorf("implausible input count")
		}
		out.Write(vinRaw)
		for j := uint64(0); j < vinCount; j++ {
			start := p.off
			if _, err := p.read(32 + 4); err != nil {
				return nil, err
			}
			scriptLen, _, err := p.varIntRaw()
			if err != nil {
				return nil, err
			}
			if scriptLen > uint64(p.remaining()) {
				return nil, io.ErrUnexpectedEOF
			}
			if _, err := p.read(int(scriptLen)); err != nil {
				return nil, err
			}
			if _, err := p.read(4); err != nil {
				return nil, err
			}
			out.Write(p.b[start:p.off])
		}

		voutCount, voutRaw, err := p.varIntRaw()
		if err != nil {
			return nil, err
		}
		if voutCount > 100000 {
			return nil, fmt.Errorf("implausible output count")
		}
		out.Write(voutRaw)
		for j := uint64(0); j < voutCount; j++ {
			start := p.off
			if _, err := p.read(8); err != nil {
				return nil, err
			}
			scriptLen, _, err := p.varIntRaw()
			if err != nil {
				return nil, err
			}
			if scriptLen > uint64(p.remaining()) {
				return nil, io.ErrUnexpectedEOF
			}
			if _, err := p.read(int(scriptLen)); err != nil {
				return nil, err
			}
			out.Write(p.b[start:p.off])
		}

		if segwit {
			for j := uint64(0); j < vinCount; j++ {
				items, _, err := p.varIntRaw()
				if err != nil {
					return nil, err
				}
				if items > 100000 {
					return nil, fmt.Errorf("implausible witness item count")
				}
				for k := uint64(0); k < items; k++ {
					itemLen, _, err := p.varIntRaw()
					if err != nil {
						return nil, err
					}
					if itemLen > uint64(p.remaining()) {
						return nil, io.ErrUnexpectedEOF
					}
					if _, err := p.read(int(itemLen)); err != nil {
						return nil, err
					}
				}
			}
		}

		lockTime, err := p.read(4)
		if err != nil {
			return nil, err
		}
		out.Write(lockTime)
	}
	if p.off != len(block) {
		return nil, fmt.Errorf("decoded %d bytes but block contains %d bytes", p.off, len(block))
	}
	return out.Bytes(), nil
}
