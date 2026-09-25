package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Profile is deliberately explicit. It extracts reference-style envelopes and
// fields; global numbering and historical binding require indexed Ord context.
const ordInterpretationProfile = "gateway-envelope-v1/ordinal-handbook-2026-09-23"

type ordEnvelope struct {
	Input           int                 `json:"input"`
	Offset          int                 `json:"envelope_offset"`
	Body            []byte              `json:"-"`
	HasBody         bool                `json:"has_body"`
	ContentType     string              `json:"content_type"`
	ContentEncoding string              `json:"content_encoding,omitempty"`
	Fields          map[string][]string `json:"fields_hex"`
	Parents         []string            `json:"parent_claims,omitempty"`
	Delegate        string              `json:"delegate,omitempty"`
	Pointer         *uint64             `json:"pointer,omitempty"`
	Metadata        string              `json:"metadata_hex,omitempty"`
	Pushnum         bool                `json:"pushnum"`
	Stutter         bool                `json:"stutter"`
	Duplicate       bool                `json:"duplicate_fields"`
	Incomplete      bool                `json:"incomplete_field"`
	Unbound         bool                `json:"unrecognized_even_field"`
}
type scriptInstruction struct {
	op   byte
	push bool
	data []byte
}

func scriptInstructions(script []byte) ([]scriptInstruction, error) {
	if len(script) > 4000000 {
		return nil, fmt.Errorf("script exceeds block budget")
	}
	out := []scriptInstruction{}
	for pos := 0; pos < len(script); {
		op := script[pos]
		pos++
		n := uint64(0)
		push := false
		switch {
		case op <= 75:
			n = uint64(op)
			push = true
		case op == 76:
			if pos >= len(script) {
				return nil, fmt.Errorf("short PUSHDATA1")
			}
			n = uint64(script[pos])
			pos++
			push = true
		case op == 77:
			if pos+2 > len(script) {
				return nil, fmt.Errorf("short PUSHDATA2")
			}
			n = uint64(binary.LittleEndian.Uint16(script[pos:]))
			pos += 2
			push = true
		case op == 78:
			if pos+4 > len(script) {
				return nil, fmt.Errorf("short PUSHDATA4")
			}
			n = uint64(binary.LittleEndian.Uint32(script[pos:]))
			pos += 4
			push = true
		}
		t := scriptInstruction{op: op, push: push}
		if push {
			if n > uint64(len(script)-pos) {
				return nil, fmt.Errorf("truncated data push")
			}
			t.data = script[pos : pos+int(n)]
			pos += int(n)
		}
		out = append(out, t)
	}
	return out, nil
}
func parseOrdEnvelopes(tx transactionView) []ordEnvelope {
	out := []ordEnvelope{}
	for vin, in := range tx.Inputs {
		w := in.Witness
		if len(w) < 2 {
			continue
		}
		last, e := hex.DecodeString(w[len(w)-1])
		if e != nil {
			continue
		}
		if len(last) > 0 && last[0] == 0x50 {
			w = w[:len(w)-1]
		}
		if len(w) < 2 {
			continue
		}
		control, ce := hex.DecodeString(w[len(w)-1])
		if ce != nil || len(control) < 33 || len(control) > 33+32*128 || (len(control)-33)%32 != 0 || control[0]&0xfe != 0xc0 {
			continue
		}
		script, e := hex.DecodeString(w[len(w)-2])
		if e != nil {
			continue
		}
		ins, e := scriptInstructions(script)
		if e != nil {
			continue
		}
		offset := 0
		stuttered := false
		for i := 0; i < len(ins); i++ {
			if !ins[i].push || len(ins[i].data) != 0 {
				continue
			}
			if i+1 >= len(ins) {
				break
			}
			if ins[i+1].op != 0x63 || ins[i+1].push {
				stuttered = ins[i+1].push && len(ins[i+1].data) == 0
				continue
			}
			i++
			if i+1 >= len(ins) {
				break
			}
			if !ins[i+1].push || !bytes.Equal(ins[i+1].data, []byte("ord")) {
				stuttered = ins[i+1].push && len(ins[i+1].data) == 0
				continue
			}
			i++
			payload := [][]byte{}
			pushnum := false
			closed := false
			for i++; i < len(ins); i++ {
				t := ins[i]
				if !t.push && t.op == 0x68 {
					closed = true
					break
				}
				if t.push {
					payload = append(payload, t.data)
					continue
				}
				if t.op == 0x4f {
					pushnum = true
					payload = append(payload, []byte{0x81})
					continue
				}
				if t.op >= 0x51 && t.op <= 0x60 {
					pushnum = true
					payload = append(payload, []byte{t.op - 0x50})
					continue
				}
				break
			}
			if closed {
				en := parseOrdFields(payload)
				en.Input = vin
				en.Offset = offset
				en.Stutter = stuttered
				en.Pushnum = pushnum
				out = append(out, en)
				offset++
			} else {
				stuttered = false
			}
		}
	}
	return out
}
func parseOrdFields(payload [][]byte) ordEnvelope {
	e := ordEnvelope{Fields: map[string][]string{}, ContentType: "application/octet-stream"}
	end := len(payload)
	for i := 0; i < len(payload); i += 2 {
		if len(payload[i]) == 0 {
			end = i
			e.HasBody = true
			for _, b := range payload[i+1:] {
				e.Body = append(e.Body, b...)
			}
			break
		}
	}
	fields := map[string][][]byte{}
	for i := 0; i < end; i += 2 {
		if i+1 >= end {
			e.Incomplete = true
			break
		}
		tag := hex.EncodeToString(payload[i])
		fields[tag] = append(fields[tag], payload[i+1])
		e.Fields[tag] = append(e.Fields[tag], hex.EncodeToString(payload[i+1]))
	}
	for _, vs := range fields {
		if len(vs) > 1 {
			e.Duplicate = true
		}
	}
	take := func(tag byte) []byte {
		k := fmt.Sprintf("%02x", tag)
		v := fields[k]
		if len(v) == 0 {
			return nil
		}
		if len(v) == 1 {
			delete(fields, k)
		} else {
			fields[k] = v[1:]
		}
		return v[0]
	}
	if b := take(1); b != nil {
		e.ContentType = string(b)
	}
	e.ContentEncoding = string(take(9))
	if b := take(11); b != nil {
		e.Delegate = decodeInscriptionReference(b)
	}
	if b := take(2); b != nil {
		if n, ok := ordPointer(b); ok {
			e.Pointer = &n
		}
	}
	for _, b := range fields["03"] {
		if id := decodeInscriptionReference(b); id != "" {
			e.Parents = append(e.Parents, id)
		}
	}
	delete(fields, "03")
	meta := []byte{}
	for _, b := range fields["05"] {
		meta = append(meta, b...)
	}
	e.Metadata = hex.EncodeToString(meta)
	delete(fields, "05")
	for k := range fields {
		tag, _ := hex.DecodeString(k)
		if len(tag) > 0 && tag[0]%2 == 0 {
			e.Unbound = true
		}
	}
	return e
}
func ordPointer(b []byte) (uint64, bool) {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	if len(b) > 8 {
		return 0, false
	}
	var n uint64
	for i, v := range b {
		n |= uint64(v) << (8 * i)
	}
	return n, true
}
func decodeInscriptionReference(b []byte) string {
	if len(b) < 32 || len(b) > 36 {
		return ""
	}
	if len(b) > 32 && b[len(b)-1] == 0 {
		return ""
	}
	var n uint32
	for i, v := range b[32:] {
		n |= uint32(v) << (8 * i)
	}
	return fmt.Sprintf("%si%d", reverseHex(b[:32]), n)
}
func inscriptionParts(id string) (string, int, error) {
	s := strings.ToLower(strings.TrimSpace(id))
	if len(s) < 66 || s[64] != 'i' || !validHash(s[:64]) {
		return "", 0, fmt.Errorf("use a reveal txid followed by i and its inscription index")
	}
	n, e := strconv.ParseUint(s[65:], 10, 32)
	if e != nil {
		return "", 0, fmt.Errorf("invalid inscription index")
	}
	return s[:64], int(n), nil
}
func isInscriptionID(id string) bool { _, _, e := inscriptionParts(id); return e == nil }
