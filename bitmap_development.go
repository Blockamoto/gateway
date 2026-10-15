package main

// The development profiles below are explicit Gateway interpretations. Their
// fingerprints are not the unfinished canonical Bitmap profile serialization.
import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"mime"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const bitmapReferenceRevision = "3dbe009313a34abf0fc0cce51a2bde39fc92d1ff"
const bitmapDevelopmentStart int64 = 792435

type bitmapDevelopmentProfile struct {
	ID                  string   `json:"id"`
	FingerprintSchema   string   `json:"fingerprint_schema"`
	ReferenceRevision   string   `json:"reference_revision"`
	ParserProfile       string   `json:"parser_profile"`
	ParserReference     string   `json:"parser_reference"`
	CanonicalStandard   bool     `json:"canonical_standard"`
	Rules               []string `json:"rules"`
	DistrictProfileHash string   `json:"district_profile_hash,omitempty"`
}

// Display names and research/build provenance do not define semantic identity.
// This is a declared development encoding, not the unfinished standard format.
func bitmapProfileHash(p bitmapDevelopmentProfile) string {
	return indexDigest(struct {
		Encoding        string
		Parser          string
		ParserReference string
		Rules           []string
		DistrictProfile string
	}{p.FingerprintSchema, p.ParserProfile, p.ParserReference, p.Rules, p.DistrictProfileHash})
}

func bitmapProfile(kind string) bitmapDevelopmentProfile {
	p := bitmapDevelopmentProfile{ID: "gateway-bitmap-" + kind + "-development-v1", FingerprintSchema: "gateway-ordered-json-semantics-v1", ReferenceRevision: bitmapReferenceRevision, ParserProfile: inscriptionParserProfile, ParserReference: inscriptionReferenceCommit, Rules: []string{
		"Use only the inscription's own undelegated body; no delegate or decompression fetch.",
		"Require parsed text/plain media type; MIME type matching is case insensitive.",
		"No charset means strict ASCII without BOM. Accepted labels: ascii/us-ascii, utf8/utf-8, utf-16/utf-16le/utf-16be, utf-32/utf-32le/utf-32be. Generic UTF-16/32 requires BOM; explicit endian accepts matching BOM, rejects contrary BOM. Strict scalar decoding; no normalization or trimming.",
		"Unknown declared charset leaves evaluation unresolved. Malformed supported decoding is ineligible.",
		"Whole decoded text uses canonical ASCII unsigned decimal components, no leading zeros except zero, followed by lowercase .bitmap.",
		"Physical order is reveal height, transaction position, transaction-wide inscription index; this is a declared development tie-break, not global Ord numbering.",
	}}
	if kind == "district" {
		p.Rules = append(p.Rules, "Referenced district height must not exceed reveal height.", "First eligible claim wins; do not add curse/reinscription/unbound/child exclusions.")
	} else if kind == "parcel" {
		p.DistrictProfileHash = bitmapProfileHash(bitmapProfile("district"))
		p.Rules = append(p.Rules, "Transaction coordinate must exist in its referenced district block.", "At least one declared parent must be the selected District and have checked input provenance; other parent edges do not invalidate it.", "First eligible child wins in physical order; unavailable provenance stays unresolved, not false.", "No current ownership is inferred.")
	} else {
		p.DistrictProfileHash = bitmapProfileHash(bitmapProfile("district"))
		p.Rules = append(p.Rules, "Commit District outcomes only using bitmap-chain-v2 separated outcome encoding.", "One step per fully evaluated block, including empty outcomes; never advance through a missing or unresolved evaluation.")
	}
	return p
}

func bitmapConsumerID(id string) bool {
	return id == "bitmap-districts" || id == "bitmap-parcels" || id == "bitmap-chain"
}
func bitmapConsumerKind(id string) string {
	if id == "bitmap-parcels" {
		return "parcel"
	}
	if id == "bitmap-chain" {
		return "chain"
	}
	return "district"
}

func bitmapDecodeBody(body []byte, contentType string) (string, string) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(media, "text/plain") {
		return "", "text_plain_required"
	}
	charset := strings.ToLower(params["charset"])
	switch charset {
	case "", "ascii", "us-ascii":
		for _, b := range body {
			if b >= 128 {
				return "", "invalid_ascii"
			}
		}
		return string(body), ""
	case "utf8", "utf-8":
		if !utf8.Valid(body) {
			return "", "invalid_utf8"
		}
		return string(body), ""
	case "utf-16", "utf-16le", "utf-16be", "utf-32", "utf-32le", "utf-32be":
		width := 2
		if strings.HasPrefix(charset, "utf-32") {
			width = 4
		}
		little := strings.HasSuffix(charset, "le")
		generic := charset == "utf-16" || charset == "utf-32"
		bomLE, bomBE := []byte{255, 254}, []byte{254, 255}
		if width == 4 {
			bomLE = []byte{255, 254, 0, 0}
			bomBE = []byte{0, 0, 254, 255}
		}
		if bytes.HasPrefix(body, bomLE) {
			if !generic && !little {
				return "", "contrary_bom"
			}
			little = true
			body = body[width:]
		} else if bytes.HasPrefix(body, bomBE) {
			if !generic && little {
				return "", "contrary_bom"
			}
			little = false
			body = body[width:]
		} else if generic {
			return "", "bom_required"
		}
		if len(body)%width != 0 {
			return "", "invalid_unicode_length"
		}
		var order binary.ByteOrder = binary.BigEndian
		if little {
			order = binary.LittleEndian
		}
		runes := []rune{}
		for i := 0; i < len(body); i += width {
			var r rune
			if width == 4 {
				n := order.Uint32(body[i : i+4])
				if n > utf8.MaxRune || n >= 0xd800 && n <= 0xdfff {
					return "", "invalid_unicode_scalar"
				}
				r = rune(n)
			} else {
				n := rune(order.Uint16(body[i : i+2]))
				r = n
				if n >= 0xd800 && n <= 0xdbff {
					if i+4 > len(body) {
						return "", "invalid_surrogate"
					}
					m := rune(order.Uint16(body[i+2 : i+4]))
					if m < 0xdc00 || m > 0xdfff {
						return "", "invalid_surrogate"
					}
					r = utf16.DecodeRune(n, m)
					i += 2
				} else if n >= 0xdc00 && n <= 0xdfff {
					return "", "invalid_surrogate"
				}
			}
			runes = append(runes, r)
		}
		return string(runes), ""
	default:
		return "", "unresolved_charset"
	}
}

func bitmapCanonicalDecimal(value string) (uint64, bool) {
	if value == "" || len(value) > 20 || len(value) > 1 && value[0] == '0' {
		return 0, false
	}
	for _, b := range []byte(value) {
		if b < '0' || b > '9' {
			return 0, false
		}
	}
	n, e := strconv.ParseUint(value, 10, 64)
	return n, e == nil
}

// The following three encoders implement only the pinned separated commitment
// byte format. They do not attest source, winner selection or profile validity.
func bitmapIsolatedOutcome(ids []string) (string, error) {
	h := sha256.New()
	h.Write([]byte("bitmap/outcome-block"))
	seen := map[string]bool{}
	for _, id := range ids {
		i := strings.LastIndexByte(id, 'i')
		if i != 64 || !validHash(id[:i]) {
			return "", fmt.Errorf("invalid winning inscription identity")
		}
		n, ok := bitmapCanonicalDecimal(id[i+1:])
		if !ok || n > 0xffffffff {
			return "", fmt.Errorf("invalid inscription index")
		}
		raw, e := hex.DecodeString(id[:i])
		if e != nil {
			return "", e
		}
		for a, b := 0, len(raw)-1; a < b; a, b = a+1, b-1 {
			raw[a], raw[b] = raw[b], raw[a]
		}
		var index [4]byte
		binary.LittleEndian.PutUint32(index[:], uint32(n))
		record := append(raw, index[:]...)
		key := hex.EncodeToString(record)
		if seen[key] {
			return "", fmt.Errorf("duplicate winning identity")
		}
		seen[key] = true
		h.Write(record)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func bitmapCumulativeOutcome(previous, isolated string) (string, error) {
	if !validHash(previous) || !validHash(isolated) {
		return "", fmt.Errorf("outcome digests required")
	}
	a, _ := hex.DecodeString(previous)
	b, _ := hex.DecodeString(isolated)
	h := sha256.New()
	h.Write([]byte("bitmap/outcome-chain"))
	h.Write(a)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func bitmapScopedCommitment(start, height int64, source, cumulative, profile string) (string, error) {
	if start < 0 || height < start || !validHash(source) || !validHash(cumulative) || !validHash(profile) {
		return "", fmt.Errorf("complete scoped commitment fields required")
	}
	b := []byte("bitmap/commitment")
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(start))
	b = append(b, n[:]...)
	binary.LittleEndian.PutUint64(n[:], uint64(height))
	b = append(b, n[:]...)
	raw, _ := hex.DecodeString(source)
	for a, z := 0, len(raw)-1; a < z; a, z = a+1, z-1 {
		raw[a], raw[z] = raw[z], raw[a]
	}
	b = append(b, raw...)
	raw, _ = hex.DecodeString(cumulative)
	b = append(b, raw...)
	raw, _ = hex.DecodeString(profile)
	b = append(b, raw...)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
