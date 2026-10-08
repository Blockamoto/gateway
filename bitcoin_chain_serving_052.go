package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Core batch calls avoid 4,000 individual HTTP requests per 2,000-header reply.
// This helper is local-provider-only and bounded independently of peer input.
func (c *coreRPC) batchContext(ctx context.Context, method string, params [][]any) ([]json.RawMessage, error) {
	if len(params) > 256 {
		return nil, fmt.Errorf("RPC batch exceeds 256 entries")
	}
	if len(params) == 0 {
		return nil, nil
	}
	rows := make([]map[string]any, len(params))
	for i, p := range params {
		rows[i] = map[string]any{"jsonrpc": "2.0", "id": strconv.Itoa(i), "method": method, "params": p}
	}
	raw, e := json.Marshal(rows)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Content-Type", "application/json")
	response, e := c.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Core batch HTTP status %d", response.StatusCode)
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(body) > 2<<20 {
		return nil, fmt.Errorf("Core batch exceeds response budget")
	}
	var replies []struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if e = json.Unmarshal(body, &replies); e != nil {
		return nil, e
	}
	if len(replies) != len(rows) {
		return nil, fmt.Errorf("Core batch response count mismatch")
	}
	out := make([]json.RawMessage, len(rows))
	seen := map[int]bool{}
	for _, r := range replies {
		i, e := strconv.Atoi(r.ID)
		if e != nil || i < 0 || i >= len(rows) || seen[i] {
			return nil, fmt.Errorf("Core batch response identity mismatch")
		}
		if r.Error != nil {
			return nil, fmt.Errorf("Core batch %s: %s", method, r.Error.Message)
		}
		seen[i] = true
		out[i] = r.Result
	}
	return out, nil
}
func parseChainLocator(payload []byte) ([]string, string, error) {
	if len(payload) < 37 {
		return nil, "", fmt.Errorf("short chain locator")
	}
	count, used, e := decodeCompactSizeMinimal(payload[4:])
	if e != nil || count > 101 {
		return nil, "", fmt.Errorf("invalid or excessive block locator count")
	}
	pos := 4 + used
	if len(payload) != pos+int(count)*32+32 {
		return nil, "", fmt.Errorf("chain locator length mismatch")
	}
	locators := make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		locators = append(locators, reverseHex(payload[pos:pos+32]))
		pos += 32
	}
	stop := reverseHex(payload[pos : pos+32])
	return locators, stop, nil
}
func (s *overlayServer) handleChainRequest(c net.Conn, command string, payload []byte) {
	locators, stop, e := parseChainLocator(payload)
	if e != nil {
		return
	}
	max := 2000
	if command == "getblocks" {
		max = 500
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	headers, e := s.app.headersForBitcoinPeer(ctx, locators, stop, max, command == "getheaders")
	if e != nil {
		s.app.invalidateBitcoinServing("Chain-serving provider failed: " + e.Error())
		return
	}
	var out bytes.Buffer
	out.Write(encodeVarInt(uint64(len(headers))))
	for _, header := range headers {
		if command == "getheaders" {
			out.Write(header)
			out.WriteByte(0)
		} else {
			h := hash256(header)
			_ = binary.Write(&out, binary.LittleEndian, uint32(2))
			out.Write(h[:])
		}
	}
	response := "headers"
	if command == "getblocks" {
		response = "inv"
	}
	_ = writeMessage(c, response, out.Bytes())
}
func (a *app) headersForBitcoinPeer(ctx context.Context, locators []string, stop string, limit int, headersRequest bool) ([][]byte, error) {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if !settings.ServeData {
		return nil, fmt.Errorf("Bitcoin serving disabled")
	}
	ready := a.currentBitcoinServing()
	if ready.Archival || ready.Limited {
		c, e := newCoreRPC(settings)
		if e != nil {
			return nil, e
		}
		if len(locators) == 0 && headersRequest {
			if stop == strings.Repeat("0", 64) {
				return nil, nil
			}
			var raw string
			if e = c.callContext(ctx, "getblockheader", []any{stop, false}, &raw); e != nil {
				return nil, nil
			}
			header, e := hex.DecodeString(raw)
			if e != nil || len(header) != 80 {
				return nil, fmt.Errorf("invalid Core header")
			}
			h := hash256(header)
			if reverseHex(h[:]) != stop {
				return nil, fmt.Errorf("Core header hash mismatch")
			}
			return [][]byte{header}, nil
		}
		start := int64(1)
		for _, hash := range locators {
			var x struct {
				Height        int64 `json:"height"`
				Confirmations int64 `json:"confirmations"`
			}
			if e = c.callContext(ctx, "getblockheader", []any{hash, true}, &x); e == nil && x.Confirmations > 0 {
				start = x.Height + 1
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if ready.Limited && !headersRequest && start < ready.Height-287 {
			start = ready.Height - 287
		}
		end := start + int64(limit) - 1
		if end > ready.Height {
			end = ready.Height
		}
		if stop != strings.Repeat("0", 64) {
			var x struct {
				Height        int64 `json:"height"`
				Confirmations int64 `json:"confirmations"`
			}
			if c.callContext(ctx, "getblockheader", []any{stop, true}, &x) == nil && x.Confirmations > 0 && x.Height >= start && x.Height < end {
				end = x.Height
			}
		}
		all := [][]byte{}
		for height := start; height <= end; {
			size := int64(256)
			if end-height+1 < size {
				size = end - height + 1
			}
			queries := make([][]any, size)
			for i := range queries {
				queries[i] = []any{height + int64(i)}
			}
			hashes, e := c.batchContext(ctx, "getblockhash", queries)
			if e != nil {
				return nil, e
			}
			headerQueries := make([][]any, len(hashes))
			decodedHashes := make([]string, len(hashes))
			for i, h := range hashes {
				if json.Unmarshal(h, &decodedHashes[i]) != nil || !validHash(decodedHashes[i]) {
					return nil, fmt.Errorf("invalid Core block hash")
				}
				headerQueries[i] = []any{decodedHashes[i], false}
			}
			raws, e := c.batchContext(ctx, "getblockheader", headerQueries)
			if e != nil {
				return nil, e
			}
			for i, raw := range raws {
				var encoded string
				if json.Unmarshal(raw, &encoded) != nil {
					return nil, fmt.Errorf("invalid Core header response")
				}
				h, e := hex.DecodeString(encoded)
				if e != nil || len(h) != 80 {
					return nil, fmt.Errorf("invalid Core raw header")
				}
				hash := hash256(h)
				if reverseHex(hash[:]) != decodedHashes[i] {
					return nil, fmt.Errorf("Core header identity mismatch")
				}
				if len(all) > 0 {
					prev := hash256(all[len(all)-1])
					if !bytes.Equal(h[4:36], prev[:]) {
						return nil, fmt.Errorf("Core chain changed during header batch")
					}
				}
				all = append(all, h)
			}
			height += size
		}
		return all, nil
	}
	// Native selected headers remain useful even for sparse nodes; no full-chain
	// service flag is inferred from possession of this header sequence.
	if len(locators) == 0 && headersRequest {
		_, h, e := a.findSelectedHeader(stop)
		if e != nil {
			return nil, nil
		}
		return [][]byte{h}, nil
	}
	start := int64(1)
	for _, hash := range locators {
		height, _, e := a.findSelectedHeader(hash)
		if e == nil {
			start = height + 1
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	count := a.getStatus().HeaderCount
	out := [][]byte{}
	for h := start; h < count && len(out) < limit; h++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		raw, e := a.readSelectedHeader(h)
		if e != nil {
			return nil, e
		}
		out = append(out, raw)
		hash := hash256(raw)
		if reverseHex(hash[:]) == stop {
			break
		}
	}
	return out, nil
}
