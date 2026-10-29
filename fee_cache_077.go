package main

// Fee receipts retain only checked FIFO arithmetic. They are not transaction
// bytes, proof of unspentness, or a substitute for ordered input amounts.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"./internal/updateapply"
)

const satFeePageRows = 256
const satFeeMaxTransactions = 500000

type satFeeRow struct {
	Fee    uint64 `json:"fee_sats"`
	Before uint64 `json:"prior_fees_sats"`
}
type satFeePage struct {
	Schema   int         `json:"schema"`
	Start    int         `json:"start"`
	Rows     []satFeeRow `json:"rows"`
	Checksum string      `json:"checksum"`
}
type satFeePageRef struct {
	Hash   string `json:"hash"`
	Before uint64 `json:"prior_fees_sats"`
	After  uint64 `json:"end_fees_sats"`
}
type satFeeBlock struct {
	Schema       int             `json:"schema"`
	Height       int64           `json:"height"`
	BlockHash    string          `json:"block_hash"`
	Transactions int             `json:"transaction_count"`
	Pages        []satFeePageRef `json:"pages"`
	Total        uint64          `json:"total_fees_sats"`
	Checksum     string          `json:"checksum"`
}

func satFeePageDigest(p satFeePage) string   { p.Checksum = ""; return indexDigest(p) }
func satFeeBlockDigest(p satFeeBlock) string { p.Checksum = ""; return indexDigest(p) }
func satFeeDirectory(root string) string     { return filepath.Join(root, "satline", "fees-v1") }
func satFeeBlockPath(root string, height int64, hash string) (string, error) {
	if height < 0 || !validHash(hash) {
		return "", fmt.Errorf("invalid fee block anchor")
	}
	return filepath.Join(satFeeDirectory(root), "blocks", strconv.FormatInt(height/1000, 10), fmt.Sprintf("%d-%s.json", height, hash)), nil
}
func readSatFeeJSON(ctx context.Context, path string, target any, budget int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, budget+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > budget {
		return fmt.Errorf("fee receipt exceeds read budget")
	}
	if err = json.Unmarshal(raw, target); err != nil {
		return err
	}
	return ctx.Err()
}
func readSatFeeBlockContext(ctx context.Context, root string, height int64, hash string) (satFeeBlock, error) {
	var p satFeeBlock
	path, err := satFeeBlockPath(root, height, hash)
	if err != nil {
		return p, err
	}
	if err = readSatFeeJSON(ctx, path, &p, 1<<20); err != nil {
		return p, err
	}
	if p.Schema != 1 || p.Height != height || p.BlockHash != hash || p.Transactions < 1 || p.Transactions > satFeeMaxTransactions || len(p.Pages) != (p.Transactions+satFeePageRows-1)/satFeePageRows || p.Checksum != satFeeBlockDigest(p) {
		return p, fmt.Errorf("fee manifest identity/checksum mismatch")
	}
	var prior uint64
	for _, ref := range p.Pages {
		if !validHash(ref.Hash) || ref.Before != prior || ref.After < ref.Before {
			return p, fmt.Errorf("invalid fee page coverage")
		}
		prior = ref.After
	}
	if prior != p.Total {
		return p, fmt.Errorf("fee manifest total mismatch")
	}
	return p, nil
}
func readSatFeePageContext(ctx context.Context, root string, b satFeeBlock, n int) (satFeePage, error) {
	var p satFeePage
	if n < 0 || n >= len(b.Pages) {
		return p, fmt.Errorf("fee page outside block")
	}
	ref := b.Pages[n]
	path := filepath.Join(satFeeDirectory(root), "pages", ref.Hash[:2], ref.Hash+".json")
	if err := readSatFeeJSON(ctx, path, &p, 128<<10); err != nil {
		return p, err
	}
	count := min(satFeePageRows, b.Transactions-n*satFeePageRows)
	if p.Schema != 1 || p.Start != n*satFeePageRows || len(p.Rows) != count || p.Checksum != ref.Hash || satFeePageDigest(p) != ref.Hash {
		return p, fmt.Errorf("fee page identity/checksum mismatch")
	}
	prior := ref.Before
	for i, row := range p.Rows {
		if row.Before != prior || p.Start+i == 0 && row.Fee != 0 {
			return p, fmt.Errorf("invalid fee prefix")
		}
		var err error
		prior, err = addUint64(prior, row.Fee)
		if err != nil {
			return p, err
		}
	}
	if prior != ref.After {
		return p, fmt.Errorf("fee page total mismatch")
	}
	return p, nil
}
func satFeeRowContext(ctx context.Context, root string, height int64, hash string, ordinal int) (satFeeRow, error) {
	b, err := readSatFeeBlockContext(ctx, root, height, hash)
	if err != nil {
		return satFeeRow{}, err
	}
	if ordinal < 0 || ordinal >= b.Transactions {
		return satFeeRow{}, fmt.Errorf("fee transaction outside block")
	}
	p, err := readSatFeePageContext(ctx, root, b, ordinal/satFeePageRows)
	if err != nil {
		return satFeeRow{}, err
	}
	return p.Rows[ordinal%satFeePageRows], nil
}
func satFeePrefixContext(ctx context.Context, root string, height int64, hash string, count int) ([]uint64, error) {
	b, err := readSatFeeBlockContext(ctx, root, height, hash)
	if err != nil {
		return nil, err
	}
	if b.Transactions != count {
		return nil, fmt.Errorf("fee transaction count mismatch")
	}
	prefix := make([]uint64, count+1)
	for n := range b.Pages {
		p, err := readSatFeePageContext(ctx, root, b, n)
		if err != nil {
			return nil, err
		}
		for i, row := range p.Rows {
			prefix[p.Start+i] = row.Before
			prefix[p.Start+i+1], err = addUint64(row.Before, row.Fee)
			if err != nil {
				return nil, err
			}
		}
	}
	return prefix, nil
}

// orderedInputValues is indexed by actual transaction ordinal and input ordinal.
// Sat processing supplies it after resolving inputs. Three-argument callers may
// use already retained, complete TXIO value evidence; absence remains an error.
func storeSatFeeBlockContext(ctx context.Context, root string, block blockView, orderedInputValues ...[][]uint64) error {
	if !integrityVerified(block) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) || block.Height < 0 || !validHash(block.Hash) || len(block.Transactions) < 1 || len(block.Transactions) > satFeeMaxTransactions {
		return fmt.Errorf("fee cache requires checked selected block evidence")
	}
	var values [][]uint64
	if len(orderedInputValues) > 1 {
		return fmt.Errorf("ambiguous fee input values")
	}
	if len(orderedInputValues) == 1 {
		values = orderedInputValues[0]
	} else {
		values = make([][]uint64, len(block.Transactions))
		for ti, tx := range block.Transactions {
			if ti == 0 {
				continue
			}
			v, err := satlineTXIOInputValuesContext(ctx, root, tx, block.Height, block.Hash, nil)
			if err != nil {
				return err
			}
			for _, in := range tx.Inputs {
				n, ok := v[satlineOutpointKey(in.PrevTxID, in.PrevVout)]
				if !ok {
					return fmt.Errorf("fee input value unavailable")
				}
				values[ti] = append(values[ti], n)
			}
		}
	}
	if len(values) != len(block.Transactions) {
		return fmt.Errorf("fee input transaction count mismatch")
	}
	rows := make([]satFeeRow, len(block.Transactions))
	var total uint64
	for ti, tx := range block.Transactions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tx.Index != ti || tx.Coinbase != (ti == 0) || !validHash(tx.TxID) {
			return fmt.Errorf("fee transaction order mismatch")
		}
		rows[ti].Before = total
		if ti == 0 {
			if len(values[ti]) != 0 {
				return fmt.Errorf("coinbase has no ordinary fee inputs")
			}
			continue
		}
		if len(values[ti]) != len(tx.Inputs) || len(tx.Inputs) == 0 {
			return fmt.Errorf("fee ordered input values incomplete")
		}
		var input uint64
		for i, value := range values[ti] {
			if tx.Inputs[i].Coinbase {
				return fmt.Errorf("ordinary fee transaction has coinbase input")
			}
			var err error
			input, err = addUint64(input, value)
			if err != nil {
				return err
			}
		}
		output, err := sumOutputs(tx)
		if err != nil {
			return err
		}
		if output > input {
			return fmt.Errorf("fee outputs exceed inputs")
		}
		rows[ti].Fee = input - output
		total, err = addUint64(total, rows[ti].Fee)
		if err != nil {
			return err
		}
	}
	return storeSatFeeRowsContext077(ctx, root, block.Height, block.Hash, rows, total)
}

func storeSatFeePrefixContext077(ctx context.Context, root string, block blockView, prefix []uint64) error {
	if len(block.Transactions) < 1 || len(block.Transactions) > satFeeMaxTransactions || len(prefix) != len(block.Transactions)+1 || prefix[0] != 0 || prefix[1] != 0 {
		return fmt.Errorf("fee prefix layout incomplete")
	}
	rows := make([]satFeeRow, len(block.Transactions))
	for i := range rows {
		if prefix[i+1] < prefix[i] {
			return fmt.Errorf("fee prefix decreases")
		}
		rows[i] = satFeeRow{Fee: prefix[i+1] - prefix[i], Before: prefix[i]}
	}
	return storeSatFeeRowsContext077(ctx, root, block.Height, block.Hash, rows, prefix[len(prefix)-1])
}
func storeSatFeeRowsContext077(ctx context.Context, root string, height int64, hash string, rows []satFeeRow, total uint64) error {
	if height < 0 || !validHash(hash) || len(rows) < 1 || len(rows) > satFeeMaxTransactions {
		return fmt.Errorf("invalid fee receipt identity")
	}
	b := satFeeBlock{Schema: 1, Height: height, BlockHash: hash, Transactions: len(rows), Total: total, Pages: []satFeePageRef{}}
	for start := 0; start < len(rows); start += satFeePageRows {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+satFeePageRows, len(rows))
		p := satFeePage{Schema: 1, Start: start, Rows: rows[start:end]}
		p.Checksum = satFeePageDigest(p)
		path := filepath.Join(satFeeDirectory(root), "pages", p.Checksum[:2], p.Checksum+".json")
		if err := updateapply.CheckPath(path); err != nil {
			return err
		}
		if err := atomicWriteJSON(path, p); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		b.Pages = append(b.Pages, satFeePageRef{p.Checksum, p.Rows[0].Before, p.Rows[len(p.Rows)-1].Before + p.Rows[len(p.Rows)-1].Fee})
	}
	b.Checksum = satFeeBlockDigest(b)
	path, err := satFeeBlockPath(root, height, hash)
	if err != nil {
		return err
	}
	// All pages and their directory entries precede the visibility manifest.
	for _, dir := range []string{filepath.Join(satFeeDirectory(root), "pages"), satFeeDirectory(root), filepath.Dir(satFeeDirectory(root)), root} {
		if err = syncDirectory(dir); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = updateapply.CheckPath(path); err != nil {
		return err
	}
	if err = atomicWriteJSON(path, b); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path)), satFeeDirectory(root)} {
		if err = syncDirectory(dir); err != nil {
			return err
		}
	}
	return nil
}
