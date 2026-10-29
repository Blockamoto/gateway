package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"./internal/updateapply"
)

type satlineReverseResult struct {
	State     string         `json:"state"`
	SatNumber *uint64        `json:"sat_number,omitempty"`
	Start     satlinePoint   `json:"start_satpoint"`
	Frontier  *satlinePoint  `json:"frontier,omitempty"`
	Snapshot  *chainSnapshot `json:"snapshot,omitempty"`
	Steps     int            `json:"steps"`
	Note      string         `json:"note"`
}
type satlineReverseAnchor077 struct {
	Height int64  `json:"height"`
	Hash   string `json:"block_hash"`
}
type satlineReversePage077 struct {
	Schema   int                       `json:"schema"`
	Anchors  []satlineReverseAnchor077 `json:"anchors"`
	Checksum string                    `json:"checksum"`
}
type satlineReverseCache077 struct {
	Schema   int                  `json:"schema"`
	Input    string               `json:"input"`
	Result   satlineReverseResult `json:"result"`
	Pages    []string             `json:"pages"`
	Anchors  int                  `json:"anchor_count"`
	Checksum string               `json:"checksum"`
}

func satlineReversePageDigest077(p satlineReversePage077) string {
	p.Checksum = ""
	return indexDigest(p)
}
func satlineReverseCacheDigest077(p satlineReverseCache077) string {
	p.Checksum = ""
	return indexDigest(p)
}
func satlineReverseDirectory077(root string) string {
	return filepath.Join(root, "satline", "reverse-v1")
}
func storeSatlineReverse077(ctx context.Context, root, input string, result satlineReverseResult, anchors map[int64]string) error {
	if len(anchors) > satlineMaxHops || result.Steps > satlineMaxHops {
		return fmt.Errorf("reverse cache work budget exceeded")
	}
	ordered := make([]satlineReverseAnchor077, 0, len(anchors))
	for h, hash := range anchors {
		ordered = append(ordered, satlineReverseAnchor077{h, hash})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Height < ordered[j].Height })
	cache := satlineReverseCache077{Schema: 1, Input: input, Result: result, Pages: []string{}, Anchors: len(anchors)}
	dir := satlineReverseDirectory077(root)
	for i := 0; i < len(ordered); i += 256 {
		p := satlineReversePage077{Schema: 1, Anchors: ordered[i:min(i+256, len(ordered))]}
		p.Checksum = satlineReversePageDigest077(p)
		path := filepath.Join(dir, "pages", p.Checksum[:2], p.Checksum+".json")
		if err := updateapply.CheckPath(path); err != nil {
			return err
		}
		if err := atomicWriteJSON(path, p); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		cache.Pages = append(cache.Pages, p.Checksum)
	}
	if len(cache.Pages) > 0 {
		if err := syncDirectory(filepath.Join(dir, "pages")); err != nil {
			return err
		}
	}
	cache.Checksum = satlineReverseCacheDigest077(cache)
	path := filepath.Join(dir, "queries", satlineFollowKey(input)+".json")
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if err := atomicWriteJSON(path, cache); err != nil {
		return err
	}
	for _, path := range []string{filepath.Dir(path), dir, filepath.Dir(dir), root} {
		if err := syncDirectory(path); err != nil {
			return err
		}
	}
	return nil
}
func readSatlineReverse077(ctx context.Context, root, input string, hashAt func(int64) (string, error)) (satlineReverseResult, map[int64]string, bool, error) {
	var c satlineReverseCache077
	path := filepath.Join(satlineReverseDirectory077(root), "queries", satlineFollowKey(input)+".json")
	err := readSatFeeJSON(ctx, path, &c, 1<<20)
	if os.IsNotExist(err) {
		return c.Result, nil, false, nil
	}
	if err != nil {
		return c.Result, nil, false, err
	}
	if c.Schema != 1 || c.Input != input || c.Checksum != satlineReverseCacheDigest077(c) || c.Anchors < 0 || c.Anchors > satlineMaxHops || len(c.Pages) != (c.Anchors+255)/256 || c.Result.Steps < 0 || c.Result.Steps > satlineMaxHops {
		return c.Result, nil, false, fmt.Errorf("reverse cache identity/checksum mismatch")
	}
	anchors := map[int64]string{}
	prior := int64(-1)
	for _, hash := range c.Pages {
		if !validHash(hash) {
			return c.Result, nil, false, fmt.Errorf("invalid reverse anchor page")
		}
		var p satlineReversePage077
		path := filepath.Join(satlineReverseDirectory077(root), "pages", hash[:2], hash+".json")
		if err = readSatFeeJSON(ctx, path, &p, 128<<10); err != nil {
			return c.Result, nil, false, err
		}
		if p.Schema != 1 || p.Checksum != hash || satlineReversePageDigest077(p) != hash || len(p.Anchors) < 1 || len(p.Anchors) > 256 {
			return c.Result, nil, false, fmt.Errorf("reverse anchor checksum mismatch")
		}
		for _, anchor := range p.Anchors {
			if anchor.Height <= prior || !validHash(anchor.Hash) {
				return c.Result, nil, false, fmt.Errorf("invalid reverse anchor order")
			}
			actual, e := hashAt(anchor.Height)
			if e != nil {
				return c.Result, nil, false, e
			}
			if actual != anchor.Hash {
				return c.Result, nil, false, nil
			}
			anchors[anchor.Height] = anchor.Hash
			prior = anchor.Height
		}
	}
	if len(anchors) != c.Anchors || anchors[c.Result.Start.Height] != c.Result.Start.BlockHash {
		return c.Result, nil, false, fmt.Errorf("reverse cache start not anchored")
	}
	if c.Result.State == "known" {
		if c.Result.SatNumber == nil || *c.Result.SatNumber >= theoreticalSatSupply() {
			return c.Result, nil, false, fmt.Errorf("invalid cached sat identity")
		}
	} else if c.Result.Frontier == nil {
		return c.Result, nil, false, fmt.Errorf("reverse cache missing frontier")
	} else if anchors[c.Result.Frontier.Height] != c.Result.Frontier.BlockHash {
		return c.Result, nil, false, fmt.Errorf("reverse cache frontier not anchored")
	}
	return c.Result, anchors, true, nil
}
func (b *satlineEvidenceBackend077) PreviousPoint(tx transactionView, height int64, hash string, vin int, offset uint64) (satlinePoint, error) {
	s, batch, err := satlineTXIOBatchContext(b.ctx, b.a.dataDir, height, hash)
	if err != nil {
		return satlinePoint{}, err
	}
	catalogue, err := s.readTXIOTranslation(b.ctx, batch.Spenders.LocatorRef)
	if err != nil {
		return satlinePoint{}, err
	}
	if tx.Index < 1 || tx.Index >= len(catalogue.Transactions) || catalogue.Transactions[tx.Index].TxID != tx.TxID {
		return satlinePoint{}, fmt.Errorf("previous point transaction mismatch")
	}
	for _, row := range batch.Spenders.Rows {
		if row.TxIndex != tx.Index || row.Vin != vin {
			continue
		}
		if row.Output == "" {
			return satlinePoint{}, fmt.Errorf("previous point is unresolved")
		}
		c, _ := parseBODCoordinate(row.Output)
		hash := batch.Spenders.CreationAnchors[fmt.Sprint(c.Height)]
		actual, err := b.hashAt(c.Height)
		if err != nil || actual != hash {
			return satlinePoint{}, fmt.Errorf("previous point anchor unavailable")
		}
		for _, parent := range catalogue.Parents {
			if parent.Output == row.Output && parent.BlockHash == hash && vin < len(tx.Inputs) && parent.TxID == tx.Inputs[vin].PrevTxID && row.Vout == tx.Inputs[vin].PrevVout {
				return satlinePoint{TxID: parent.TxID, Vout: row.Vout, Offset: offset, Height: c.Height, BlockHash: hash, TxIndex: c.TxIndex}, nil
			}
		}
		return satlinePoint{}, fmt.Errorf("previous point native identity mismatch")
	}
	return satlinePoint{}, fmt.Errorf("previous point not retained")
}
func (r *satlineResolver) reverseInputPoint077(tx transactionView, position uint64, height int64, hash string) (satlinePoint, error) {
	r.primeInputValues(tx, height)
	for vin, in := range tx.Inputs {
		if in.Coinbase {
			return satlinePoint{}, fmt.Errorf("coinbase has no ordinary input")
		}
		value, err := r.prevoutValue(in.PrevTxID, in.PrevVout, height)
		if err != nil {
			return satlinePoint{}, err
		}
		if position < value {
			if b, ok := r.backend.(interface {
				PreviousPoint(transactionView, int64, string, int, uint64) (satlinePoint, error)
			}); ok {
				if point, e := b.PreviousPoint(tx, height, hash, vin, position); e == nil {
					return point, nil
				}
			}
			parent, err := r.transaction(in.PrevTxID, height)
			if err != nil {
				return satlinePoint{}, err
			}
			if parent.Height > height || parent.Height == height && parent.Tx.Index >= tx.Index {
				return satlinePoint{}, fmt.Errorf("previous transaction order mismatch")
			}
			return satlinePoint{TxID: in.PrevTxID, Vout: in.PrevVout, Offset: position, Height: parent.Height, BlockHash: parent.BlockHash, TxIndex: parent.Tx.Index}, nil
		}
		position -= value
	}
	return satlinePoint{}, fmt.Errorf("position exceeds ordered input values")
}

// This follows one output-relative point. It never computes all ranges in an
// output or starts a global Sat index. Only visited block anchors are cached.
func (a *app) reverseSatlineSatpointContext(ctx context.Context, input string, rebuild ...bool) (satlineReverseResult, error) {
	if err := requireReleaseFeature("satline"); err != nil {
		return satlineReverseResult{State: "unavailable", Note: err.Error()}, err
	}
	return a.traceSatlineSatpointContext077(ctx, input, rebuild...)
}

// Internal traversal remains testable while the release entry point is gated.
// No UI, API, CLI or persisted-job path calls this implementation directly.
func (a *app) traceSatlineSatpointContext077(ctx context.Context, input string, rebuild ...bool) (satlineReverseResult, error) {
	query, err := parseSatlineQuery(input)
	if err != nil || query.Kind != "satpoint" {
		return satlineReverseResult{}, fmt.Errorf("reverse Satline requires an output-relative satpoint")
	}
	backend := newSatlineEvidenceBackend077(a, ctx)
	resolver := newSatlineResolver(backend)
	resolver.ctx = ctx
	result := satlineReverseResult{State: "unknown", Note: "Missing historical evidence is unknown; this trace does not establish current location or unspentness."}
	anchors := map[int64]string{}
	if _, err = backend.hashAt(backend.pin.Height); err != nil {
		return result, err
	}
	resume := len(rebuild) == 0 || !rebuild[0]
	if resume {
		cached, saved, have, e := readSatlineReverse077(ctx, a.dataDir, query.Input, backend.hashAt)
		if e != nil {
			return result, e
		}
		if have {
			result, anchors = cached, saved
			if result.State == "known" {
				result.Snapshot = &chainSnapshot{Network: "mainnet", Height: backend.pin.Height, Hash: backend.pin.Hash, Source: "local_visited_sat_path"}
				return result, nil
			}
		}
	}
	point := satlinePoint{}
	if result.Frontier != nil {
		point = *result.Frontier
	} else {
		point, err = resolver.resolveStartPoint(query.Input)
		if err != nil {
			return result, err
		}
		result.Start = point
		anchors[point.Height] = point.BlockHash
	}
	result.Snapshot = &chainSnapshot{Network: "mainnet", Height: backend.pin.Height, Hash: backend.pin.Hash, Source: "local_visited_sat_path"}
	seen := map[string]bool{}
	for result.Steps < satlineMaxHops {
		if err = ctx.Err(); err != nil {
			break
		}
		key := fmt.Sprintf("%d:%d:%d:%d", point.Height, point.TxIndex, point.Vout, point.Offset)
		if seen[key] {
			err = fmt.Errorf("cycle in visited sat dependencies")
			break
		}
		seen[key] = true
		block, e := resolver.block(point.Height)
		if e != nil {
			err = e
			break
		}
		if block.Hash != point.BlockHash || point.TxIndex < 0 || point.TxIndex >= len(block.Transactions) {
			err = fmt.Errorf("visited point block identity mismatch")
			break
		}
		tx := block.Transactions[point.TxIndex]
		if tx.TxID != point.TxID || int(point.Vout) >= len(tx.Outputs) || point.Offset >= tx.Outputs[point.Vout].ValueSats {
			err = fmt.Errorf("visited output is absent or offset out of range")
			break
		}
		anchors[block.Height] = block.Hash
		position := point.Offset
		for oi := 0; oi < int(point.Vout); oi++ {
			position, err = addUint64(position, tx.Outputs[oi].ValueSats)
			if err != nil {
				break
			}
		}
		if err != nil {
			break
		}
		if tx.Coinbase {
			subsidy := subsidyAtHeight(block.Height)
			if position < subsidy {
				number := firstSatAtHeight(block.Height) + position
				result.SatNumber = &number
				result.Steps++
				result.State = "known"
				result.Frontier = nil
				result.Note = "Identity derived along the visited FIFO path. Current ownership and complete output ranges were not computed."
				break
			}
			feePosition := position - subsidy
			ordinal := -1
			var offset uint64
			if prefix, ok := resolver.coreFeePrefix(block); ok && len(prefix) == len(block.Transactions)+1 {
				n := sort.Search(len(block.Transactions)-1, func(i int) bool { return prefix[i+2] > feePosition })
				if n < len(block.Transactions)-1 {
					ordinal = n + 1
					offset = feePosition - prefix[ordinal]
				}
			} else {
				for ti := 1; ti < len(block.Transactions); ti++ {
					fee, e := resolver.transactionFee(block.Transactions[ti], block.Height)
					if e != nil {
						err = e
						break
					}
					if feePosition < fee {
						ordinal, offset = ti, feePosition
						break
					}
					feePosition -= fee
				}
			}
			if err != nil {
				break
			}
			if ordinal < 1 {
				err = fmt.Errorf("coinbase position exceeds checked subsidy and fees")
				break
			}
			tx = block.Transactions[ordinal]
			output, e := sumOutputs(tx)
			if e != nil {
				err = e
				break
			}
			position, err = addUint64(output, offset)
			if err != nil {
				break
			}
		}
		next, nextErr := resolver.reverseInputPoint077(tx, position, block.Height, block.Hash)
		if nextErr != nil {
			err = nextErr
			break
		}
		point = next
		result.Steps++
		anchors[point.Height] = point.BlockHash
		result.Frontier = &point
		if len(resolver.blocks) > 16 {
			clear(resolver.blocks)
		}
		if len(resolver.txs) > 8192 {
			clear(resolver.txs)
		}
		if len(resolver.values) > 8192 {
			clear(resolver.values)
		}
		if len(resolver.feePrefixes) > 16 {
			clear(resolver.feePrefixes)
		}
	}
	if result.State != "known" {
		result.Frontier = &point
		if ctx.Err() != nil {
			result.State = "paused"
		} else {
			result.State = "unresolved"
		}
		if err == nil {
			err = fmt.Errorf("visited path work budget reached")
		}
		result.Note = err.Error()
	}
	// A pause may save the completed frontier, but cannot resume Bitcoin work.
	// Use a short independent context solely to authenticate and persist it.
	cacheCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		cacheCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		backend.contextSatlineBackend.ctx = cacheCtx
	}
	// Authenticate every visited anchor before publishing identity or progress.
	for h, hash := range anchors {
		actual, e := backend.hashAt(h)
		if e != nil || !strings.EqualFold(actual, hash) {
			return satlineReverseResult{State: "unresolved", Start: result.Start, Note: "Selected-chain evidence changed or became unavailable."}, fmt.Errorf("visited path anchor changed: %v", e)
		}
	}
	if cacheErr := storeSatlineReverse077(cacheCtx, a.dataDir, query.Input, result, anchors); cacheErr != nil {
		return result, cacheErr
	}
	return result, err
}
