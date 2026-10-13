package main

// Output pages are immutable, shared by optional TXIO history, historical
// address outputs and mandatory UTXO state/recovery. Readers authenticate the
// page, not just a file name.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

const outputFactPageRows = 128
const outputFactPageBytes = 9 << 20 // one maximum-size serialized script in hex

var errDamagedOutputFactPage = errors.New("damaged output fact page")

type outputFactRef struct {
	Page string `json:"page"`
	Row  uint32 `json:"row"`
}
type outputFact struct {
	Height       int64  `json:"height"`
	BlockHash    string `json:"block_hash"`
	TxIndex      int    `json:"tx_index"`
	Vout         uint32 `json:"vout"`
	ValueSats    uint64 `json:"value_sats"`
	ScriptPubKey string `json:"script_pub_key"`
	Coinbase     bool   `json:"coinbase"`
}
type outputFactRow struct {
	TxIndex int     `json:"tx"`
	Vout    uint32  `json:"output"`
	Value   *uint64 `json:"value_sats,omitempty"`
	Script  *string `json:"script_pub_key,omitempty"`
}
type outputFactPage struct {
	Schema    int             `json:"schema"`
	Height    int64           `json:"height"`
	BlockHash string          `json:"block_hash"`
	Values    bool            `json:"values"`
	Scripts   bool            `json:"scripts"`
	Rows      []outputFactRow `json:"outputs"`
}

func outputFactDirectory(root string) string {
	return filepath.Join(root, "indexes", "output-facts-v1")
}
func outputFactPath(root, hash string) (string, error) {
	if !validHash(hash) || hash != strings.ToLower(hash) {
		return "", fmt.Errorf("invalid output fact reference")
	}
	return filepath.Join(outputFactDirectory(root), hash[:2], hash+".json"), nil
}
func readOutputFactPageContext(ctx context.Context, root, hash string) (outputFactPage, error) {
	var p outputFactPage
	if e := ctx.Err(); e != nil {
		return p, e
	}
	path, e := outputFactPath(root, hash)
	if e != nil {
		return p, e
	}
	if e = updateapply.CheckPath(path); e != nil {
		return p, e
	}
	f, e := os.Open(path)
	if e != nil {
		return p, fmt.Errorf("required output facts unavailable: %w", e)
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, outputFactPageBytes+1))
	if e != nil {
		return p, e
	}
	if len(b) > outputFactPageBytes {
		return p, fmt.Errorf("%w: page exceeds byte budget", errDamagedOutputFactPage)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != hash {
		return p, fmt.Errorf("%w: checksum mismatch", errDamagedOutputFactPage)
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, e
	}
	if p.Schema != 1 || p.Height < 0 || !validHash(p.BlockHash) || len(p.Rows) == 0 || len(p.Rows) > outputFactPageRows || !p.Values && !p.Scripts {
		return p, fmt.Errorf("invalid output fact page")
	}
	for i, r := range p.Rows {
		if r.TxIndex < 0 || (r.Value != nil) != p.Values || (r.Script != nil) != p.Scripts || i > 0 && (r.TxIndex < p.Rows[i-1].TxIndex || r.TxIndex == p.Rows[i-1].TxIndex && r.Vout <= p.Rows[i-1].Vout) {
			return p, fmt.Errorf("invalid output fact row")
		}
		if r.Script != nil {
			if _, e = hex.DecodeString(*r.Script); e != nil || *r.Script != strings.ToLower(*r.Script) {
				return p, fmt.Errorf("invalid exact output script")
			}
		}
	}
	return p, ctx.Err()
}
func readOutputFactLayersContext(ctx context.Context, root string, ref outputFactRef) (outputFact, bool, bool, error) {
	p, e := readOutputFactPageContext(ctx, root, ref.Page)
	if e != nil {
		return outputFact{}, false, false, e
	}
	if uint64(ref.Row) >= uint64(len(p.Rows)) {
		return outputFact{}, false, false, fmt.Errorf("output fact row outside page")
	}
	r := p.Rows[ref.Row]
	f := outputFact{Height: p.Height, BlockHash: p.BlockHash, TxIndex: r.TxIndex, Vout: r.Vout, Coinbase: r.TxIndex == 0}
	if r.Value != nil {
		f.ValueSats = *r.Value
	}
	if r.Script != nil {
		f.ScriptPubKey = *r.Script
	}
	return f, p.Values, p.Scripts, nil
}
func readOutputFactContext(ctx context.Context, root string, ref outputFactRef) (outputFact, error) {
	f, v, s, e := readOutputFactLayersContext(ctx, root, ref)
	if e == nil && (!v || !s) {
		e = fmt.Errorf("live/recovery coin lacks complete output facts")
	}
	return f, e
}
func storeBlockOutputFactsContext(ctx context.Context, root string, block blockView) (map[string]outputFactRef, error) {
	return storeBlockOutputFactLayersContext(ctx, root, block, true, true)
}
func storeBlockOutputFactLayersContext(ctx context.Context, root string, block blockView, values, scripts bool) (map[string]outputFactRef, error) {
	refs := map[string]outputFactRef{}
	if !values && !scripts {
		return refs, nil
	}
	if block.Height < 0 || !validHash(block.Hash) {
		return nil, fmt.Errorf("output facts need a block identity")
	}
	p := outputFactPage{Schema: 1, Height: block.Height, BlockHash: block.Hash, Values: values, Scripts: scripts}
	dirtyDirectories := map[string]bool{}
	flush := func() error {
		if len(p.Rows) == 0 {
			return nil
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		b, e := json.Marshal(p)
		if e != nil {
			return e
		}
		if len(b) > outputFactPageBytes {
			return fmt.Errorf("output facts exceed page budget")
		}
		sum := sha256.Sum256(b)
		hash := hex.EncodeToString(sum[:])
		path, e := outputFactPath(root, hash)
		if e != nil {
			return e
		}
		if e = updateapply.CheckPath(path); e != nil {
			return e
		}
		info, statErr := os.Stat(path)
		if os.IsNotExist(statErr) {
			if e = atomicWriteBytes(path, b); e != nil {
				return e
			}
		} else if statErr != nil {
			return statErr
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("output fact path is not a regular file")
		} else if _, e = readOutputFactPageContext(ctx, root, hash); e != nil {
			if !errors.Is(e, errDamagedOutputFactPage) {
				return e
			}
			// The content address commits to b. Replacing damaged bytes with
			// those exact bytes repairs every owner without changing its fact.
			if e = ctx.Err(); e != nil {
				return e
			}
			if e = atomicWriteBytes(path, b); e != nil {
				return e
			}
		}
		// Returned references become publishable only after the complete block
		// flush below. Reused pages need that same durability acknowledgement.
		dirtyDirectories[filepath.Dir(path)] = true
		for i, r := range p.Rows {
			refs[outputCoordinate(int(r.Vout), r.TxIndex, block.Height)] = outputFactRef{hash, uint32(i)}
		}
		p.Rows = nil
		return nil
	}
	bytes := 0
	for ti, tx := range block.Transactions {
		if tx.Coinbase != (ti == 0) {
			return nil, fmt.Errorf("output facts require verified coinbase position")
		}
		for oi, out := range tx.Outputs {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			if out.N != oi {
				return nil, fmt.Errorf("output facts require canonical output order")
			}
			r := outputFactRow{TxIndex: ti, Vout: uint32(oi)}
			if values {
				v := out.ValueSats
				r.Value = &v
			}
			if scripts {
				s := out.ScriptPubKey
				if _, e := hex.DecodeString(s); e != nil || s != strings.ToLower(s) {
					return nil, fmt.Errorf("output facts require exact script bytes")
				}
				r.Script = &s
			}
			rowBytes := 80
			if r.Script != nil {
				rowBytes += len(*r.Script)
			}
			if len(p.Rows) >= outputFactPageRows || len(p.Rows) > 0 && bytes+rowBytes > 512<<10 {
				if e := flush(); e != nil {
					return nil, e
				}
				bytes = 0
			}
			p.Rows = append(p.Rows, r)
			bytes += rowBytes
		}
	}
	if e := flush(); e != nil {
		return nil, e
	}
	for dir := range dirtyDirectories {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if e := syncDirectory(dir); e != nil {
			return nil, e
		}
	}
	if len(dirtyDirectories) > 0 {
		for _, dir := range []string{outputFactDirectory(root), filepath.Dir(outputFactDirectory(root)), root} {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			if e := syncDirectory(dir); e != nil {
				return nil, e
			}
		}
	}
	return refs, nil
}
