package main

import (
	"bytes"
	"context"
	"fmt"
)

// A committed all-transactions point island can supply an exact next native
// prefix step. Its singleton From/PreviousCommitment are never copied. The
// normal writer recomputes the native prefix checkpoint from authenticated
// ordered facts, retaining the same per-block recipe and staging boundary.
func (a *app) promoteRunTransactionPoint(ctx context.Context, c *indexRunConsumer, height int64, hash string, retention string) (bool, error) {
	if c == nil || c.store == nil || c.selection.Index != "tx-locator" || c.store.head.Mode == "" {
		return false, nil
	}
	// Turning Raw On explicitly requests body durability. A point receipt alone
	// cannot manufacture a Raw body receipt, so use the ordinary source path.
	if timelineRawBlocksOn(a.dataDir) {
		return false, nil
	}
	if c.next(height) != height {
		return false, fmt.Errorf("transaction point promotion requires its exact next height")
	}
	point, have, err := a.runTransactionPoint(ctx, height, hash)
	if err != nil || !have {
		return false, err
	}
	payload := point.TransactionLocator
	if payload == nil && point.Bitcoin != nil {
		// The authenticated v1 Bitcoin.TxIDs point remains readable under its
		// original recipe. It has no modern denominator/completeness receipt;
		// use the normal verified body path instead of waiting on an immutable
		// legacy payload that this promotion lane cannot consume.
		return false, nil
	}
	if payload == nil || point.Checkpoint.From != height || point.Checkpoint.PreviousCommitment != "" || point.Checkpoint.Height != height || point.Checkpoint.BlockHash != hash || payload.Block.TransactionCount == nil || *payload.Block.TransactionCount == 0 || payload.Selection != "all" || payload.Completeness != "complete" || payload.SourceCommitment != "" {
		return false, fmt.Errorf("transaction point does not prove a complete ordered block")
	}
	if err = validateTransactionPayload(point, "tx-locator"); err != nil {
		return false, err
	}
	header, headerErr := a.readSelectedHeader(height)
	if headerErr != nil || len(header) != 80 {
		return false, nil
	}
	headerHash := hash256(header)
	if reverseHex(headerHash[:]) != hash {
		return false, nil
	}
	hashes := make([][32]byte, len(payload.Block.Transactions))
	for i, entry := range payload.Block.Transactions {
		value, err := displayHashRaw(entry.TxID)
		if err != nil {
			return false, err
		}
		hashes[i] = value
	}
	root := merkleRoot(hashes)
	if merkleMutated(hashes) || !bytes.Equal(root[:], header[36:68]) || point.PreviousBlockHash != reverseHex(header[4:36]) {
		return false, fmt.Errorf("transaction point does not match its selected header Merkle root/ancestry")
	}
	if err = checkIndexConsumerPublication(ctx, a.dataDir, "tx-locator"); err != nil {
		return false, err
	}
	// readBatch authenticated PreviousBlockHash in RecordsHash. commit below
	// requires it to match the actual logical prefix, including staged blocks.
	batch := indexBatch{PreviousBlockHash: point.PreviousBlockHash, TransactionLocator: payload}
	from := c.progress.From
	if c.store.checkpoint != nil {
		from = c.store.checkpoint.From
	}
	parent := ctx
	hashAt := a.addressSelectedHashes(parent, a.currentChainAuthority())
	work := context.WithValue(parent, indexRunPublicationKey{}, func() error {
		if err := checkIndexRunPublication(parent); err != nil {
			return err
		}
		if timelineRawBlocksOn(a.dataDir) {
			return blockUnavailableError{fmt.Errorf("Raw changed while promoting transaction point facts")}
		}
		selected, err := hashAt(height)
		if err != nil {
			return err
		}
		if selected != hash {
			return satPublicationAnchorError077{fmt.Errorf("transaction point source left the selected chain")}
		}
		return nil
	})
	c.store.locatorContext = work
	defer func() { c.store.locatorContext = nil }()
	if err = c.store.commitPreparedIndexBatch(batch, height, hash, from, retention); err != nil {
		// Automatic physical flush can fail after accepting this logical step.
		// Drop the volatile stage while its guards remain alive; the durable
		// point island survives and can be replayed at the exact next height.
		c.abortLocatorBatch()
		c.reconciled = false
		return false, err
	}
	return true, nil
}
