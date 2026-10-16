package main

import (
	"context"
	"fmt"
)

// This is a direct known-block compatibility read, never a historical search
// or locator repair. Old decoded evidence is reconstructed and fully verified
// before it enters the same shared acquisition path as serialized bodies.
func (a *app) legacyRawBodyBase(ctx context.Context, target blockTarget) (blockView, bool, error) {
	if target.Height < 0 {
		return blockView{}, false, nil
	}
	s, e := indexStoreHead(a.dataDir, "blocks")
	if e != nil {
		return blockView{}, false, e
	}
	if s.checkpoint == nil || target.Height < s.checkpoint.From || target.Height > s.checkpoint.Height {
		return blockView{}, false, nil
	}
	commit, ready, e := s.activeLocatorCommitContext(ctx, target.Height)
	if e != nil || !ready || commit == "" {
		return blockView{}, false, e
	}
	b, e := s.readBatch(commit)
	if e != nil {
		return blockView{}, false, e
	}
	if b.Checkpoint.BlockHash != target.HashDisplay || b.Bitcoin == nil || b.Bitcoin.BodySchema != 0 {
		return blockView{}, false, nil
	}
	header := target.ExpectedHeader
	if len(header) != 80 {
		header, e = a.readSelectedHeader(target.Height)
		if e != nil {
			return blockView{}, false, e
		}
	}
	hash := hash256(header)
	if reverseHex(hash[:]) != target.HashDisplay {
		return blockView{}, false, fmt.Errorf("legacy body target differs from selected header")
	}
	raw, e := serializeIndexedBlock(header, b.Bitcoin.Transactions)
	if e != nil {
		return blockView{}, false, e
	}
	v, e := parseBlockDetailed(target.Height, target.HashDisplay, header, raw, "Gateway legacy decoded Blocks evidence", false)
	if e == nil {
		v.SourceNetwork = "local_index"
		v.CacheVisibility = "private"
	}
	return v, e == nil, e
}
