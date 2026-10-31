package main

import (
	"./internal/updateapply"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Frozen pre-migration physical derivation, used only for differential tests.
func storeBlockOutputFactLayersUncachedOracle(ctx context.Context, root string, block blockView, values, scripts bool) (map[string]outputFactRef, error) {
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
