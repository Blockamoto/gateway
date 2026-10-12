package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Resolve only explicitly frozen sequential publications. Browsed receipts,
// current private head growth and private caches are not publication consent.
// The shared locator tables are advisory: the published head slot binds each
// candidate to its immutable receipt before any containing-block hint is sent.
func (a *app) publishedTransactionLocation(ctx context.Context, txid, blockHash string) (txLocation, bool, error) {
	a.settingsMu.RLock()
	serving := a.settings.ServeGatewayData
	a.settingsMu.RUnlock()
	if !serving {
		return txLocation{}, false, nil
	}
	var match *txLocation
	publications := map[string]indexPublication{}
	for _, id := range []string{inscriptionLocatorIndex, "tx-locator", "blocks"} {
		if err := ctx.Err(); err != nil {
			return txLocation{}, false, err
		}
		path := filepath.Join(a.dataDir, "indexes", id, "publication.json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return txLocation{}, false, fmt.Errorf("published locator metadata unavailable")
		}
		p, err := a.readIndexPublication(id)
		if err != nil {
			return txLocation{}, false, err
		}
		publications[id] = p
		d, _ := findIndexDefinition(id)
		checkpoint := p.Manifest.Checkpoint
		publicationDir := filepath.Dir(filepath.Dir(indexCommitReadPath(a.dataDir, id, checkpoint.Commitment)))
		s := &indexStore{profileRoot: a.dataDir, dir: publicationDir, definition: d, head: indexHead{Commitment: checkpoint.Commitment}, checkpoint: &checkpoint}
		records, err := s.bitcoinLocatorRecordsReadOnly(ctx, txid)
		if err != nil {
			return txLocation{}, false, err
		}
		for _, record := range records {
			if record.Height < checkpoint.From || record.Height > checkpoint.Height {
				continue
			}
			active, ready, err := s.activeLocatorCommitContext(ctx, record.Height)
			if err != nil || !ready {
				if err == nil {
					err = fmt.Errorf("published locator membership requires local repair")
				}
				return txLocation{}, false, err
			}
			if active != record.Commit {
				continue
			}
			b, err := s.readBatch(record.Commit)
			if err != nil {
				return txLocation{}, false, err
			}
			if b.Checkpoint.Height != record.Height || b.Checkpoint.From != checkpoint.From {
				return txLocation{}, false, fmt.Errorf("published locator receipt exceeds its approved range")
			}
			hash, err := a.canonicalHashAtHeight(record.Height)
			if err != nil {
				return txLocation{}, false, err
			}
			if hash != b.Checkpoint.BlockHash || blockHash != "" && blockHash != hash {
				continue
			}
			if id == inscriptionLocatorIndex {
				d, _ := findIndexDefinition("inscriptions")
				sourceStore := &indexStore{dir: filepath.Join(a.dataDir, "indexes", "inscriptions"), definition: d}
				source, err := sourceStore.readBatch(b.TransactionLocator.SourceCommitment)
				if err != nil {
					return txLocation{}, false, err
				}
				if source.Checkpoint.BlockHash != hash {
					return txLocation{}, false, fmt.Errorf("published related locator source differs from the selected block")
				}
				if err = validateSparseInscriptionSource(b.TransactionLocator, source); err != nil {
					return txLocation{}, false, err
				}
			}
			entries := transactionBatchEntries(b)
			if record.TxIndex < 0 || record.TxIndex >= len(entries) || entries[record.TxIndex].TxID != txid {
				return txLocation{}, false, fmt.Errorf("published locator differs from its committed transaction position")
			}
			candidate := txLocation{TxID: txid, BlockHash: hash, Height: record.Height, TxIndex: int(entries[record.TxIndex].TxIndex)}
			if match != nil && (match.BlockHash != candidate.BlockHash || match.Height != candidate.Height || match.TxIndex != candidate.TxIndex) {
				return txLocation{}, true, fmt.Errorf("historical transaction ID is ambiguous; supply its containing block")
			}
			match = &candidate
		}
	}
	if err := ctx.Err(); err != nil {
		return txLocation{}, false, err
	}
	// A revoked or replaced snapshot must not escape an in-flight local lookup.
	for id, original := range publications {
		current, err := a.readIndexPublication(id)
		if err != nil || indexDigest(current) != indexDigest(original) {
			return txLocation{}, false, fmt.Errorf("locator publication changed during lookup")
		}
	}
	a.settingsMu.RLock()
	serving = a.settings.ServeGatewayData
	a.settingsMu.RUnlock()
	if !serving || match == nil {
		return txLocation{}, false, nil
	}
	return *match, true, nil
}
