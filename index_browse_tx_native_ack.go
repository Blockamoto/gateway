package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

type browseTXNativeAcknowledgement struct {
	Schema     int             `json:"schema"`
	Checkpoint indexCheckpoint `json:"checkpoint"`
	Digest     string          `json:"digest"`
}

func browseTXNativeAckDigest(ack browseTXNativeAcknowledgement) string {
	ack.Digest = ""
	return indexDigest(ack)
}
func browseTXNativeAckPath(root string) string {
	return filepath.Join(root, "indexes", "browse", "tx-locator", "native-ack-v3.json")
}

func validBrowseTXNativeCheckpoint(c indexCheckpoint) bool {
	d, err := findIndexDefinition("tx-locator")
	if err != nil {
		return false
	}
	return c.Definition == d.ID && c.Version == d.Version && c.Schema == d.CheckpointSchema && c.RuleHash == d.RuleHash && c.Network == d.Network && c.From >= d.StartHeight && c.Height >= c.From && validHash(c.BlockHash) && c.BlockHash == strings.ToLower(c.BlockHash) && validHash(c.Commitment) && c.Commitment == checkpointHash(c)
}

// Call only after the native physical flush succeeds. The extra directory ack
// also completes a prior visible-head acknowledgement before retiring lookup
// debt. Staged or failed native writes never expand this descriptor.
func (a *app) acknowledgeBrowseTXPointsNative(ctx context.Context, native *indexStore) error {
	if native == nil || native.definition.ID != "tx-locator" {
		return fmt.Errorf("native TX store required")
	}
	if filepath.Clean(native.profileRoot) != filepath.Clean(a.dataDir) || filepath.Clean(native.dir) != filepath.Join(filepath.Clean(a.dataDir), "indexes", "tx-locator") {
		return fmt.Errorf("native TX acknowledgement belongs to a different profile or store")
	}
	cp := native.DurableCheckpoint()
	if cp == nil {
		return nil
	}
	if !validBrowseTXNativeCheckpoint(*cp) {
		return fmt.Errorf("invalid native TX acknowledgement checkpoint")
	}
	if err := checkTimelineTrackArmed(ctx, a.dataDir, "tx-locator"); err != nil {
		return err
	}
	current, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil {
		return err
	}
	if current.checkpoint == nil || *current.checkpoint != *cp {
		return fmt.Errorf("native TX durability acknowledgement does not match its visible head")
	}
	if commit, ready, err := current.activeLocatorCommitContext(ctx, cp.Height); err != nil || !ready || commit != cp.Commitment {
		if err != nil {
			return err
		}
		return fmt.Errorf("native TX membership is not acknowledged")
	}
	batch, err := current.readBatch(cp.Commitment)
	if err != nil {
		return err
	}
	if batch.TransactionLocator == nil || validateTransactionPayload(batch, "tx-locator") != nil {
		return fmt.Errorf("native TX acknowledgement requires the complete ordered transaction recipe")
	}
	hashAt := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	if hash, err := hashAt(cp.Height); err != nil || hash != cp.BlockHash {
		if err != nil {
			return err
		}
		return fmt.Errorf("native TX acknowledgement left selected chain")
	}
	if err := native.syncLocatorDirectory(native.dir); err != nil {
		return err
	}
	store := a.newBrowseStore("tx-locator")
	store.locatorSyncDirectory = native.locatorSyncDirectory
	if err := store.makeLocatorDirectories(store.dir); err != nil {
		return err
	}
	ack := browseTXNativeAcknowledgement{Schema: 3, Checkpoint: *cp}
	ack.Digest = browseTXNativeAckDigest(ack)
	path := browseTXNativeAckPath(a.dataDir)
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	var old browseTXNativeAcknowledgement
	have, err := readBrowseTXSmallJSON(ctx, path, 4096, &old)
	if err != nil {
		return err
	}
	if err := checkTimelineTrackArmed(ctx, a.dataDir, "tx-locator"); err != nil {
		return err
	}
	if !have || old != ack {
		if err := atomicWriteJSON(path, ack); err != nil {
			return err
		}
	}
	// Even an identical visible descriptor can follow a failed enclosing-
	// directory acknowledgement. Deduplication must finish that boundary.
	return store.syncLocatorDirectory(store.dir)
}

// An acknowledgement is only a reverse-lookup optimization. Physical points
// stay independently readable/exportable/deletable, including after native
// reorg or range deletion makes this ancestor proof unusable.
func (a *app) browseTXNativeAck(ctx context.Context) (*indexCheckpoint, error) {
	var ack browseTXNativeAcknowledgement
	have, err := readBrowseTXSmallJSON(ctx, browseTXNativeAckPath(a.dataDir), 4096, &ack)
	if err != nil {
		return nil, err
	}
	if !have {
		return nil, nil
	}
	if ack.Schema != 3 || ack.Digest != browseTXNativeAckDigest(ack) || !validBrowseTXNativeCheckpoint(ack.Checkpoint) {
		return nil, fmt.Errorf("invalid native TX point acknowledgement")
	}
	native, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil {
		return nil, err
	}
	cp := native.checkpoint
	old := ack.Checkpoint
	if cp == nil || cp.From > old.From || cp.Height < old.Height {
		return nil, nil
	}
	if !validBrowseTXNativeCheckpoint(*cp) {
		return nil, fmt.Errorf("invalid current native TX checkpoint")
	}
	if commit, ready, err := native.activeLocatorCommitContext(ctx, old.Height); err != nil {
		return nil, err
	} else if !ready || commit != old.Commitment {
		return nil, nil
	}
	hashAt := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	if hash, err := hashAt(old.Height); err != nil {
		return nil, err
	} else if hash != old.BlockHash {
		return nil, nil
	}
	if hash, err := hashAt(cp.Height); err != nil {
		return nil, err
	} else if hash != cp.BlockHash {
		return nil, nil
	}
	return &old, nil
}
