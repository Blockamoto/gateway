package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (a *app) indexLookupContext(ctx context.Context) (context.Context, func()) {
	if a.network == nil {
		return ctx, func() {}
	}
	merged, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.network.ctx, cancel)
	if a.network.ctx.Err() != nil {
		cancel()
	}
	return merged, func() { stop(); cancel() }
}

const indexExplorerMaxBatches = 512
const indexExplorerMaxBytes int64 = 64 << 20

// Bitcoin batches reuse the same immutable range/checkpoint machinery. Blocks
// preserve decoded transaction evidence; locator-only batches preserve IDs in
// block order and require a source provider for transaction bytes.
type bitcoinIndexBlock struct {
	VerifierVersion int               `json:"verifier_version,omitempty"`
	TxIDs           []string          `json:"txids"`
	Transactions    []transactionView `json:"transactions,omitempty"`
}

func reusableBitcoinEvidence(block *bitcoinIndexBlock) bool {
	if block.VerifierVersion == blockVerifierVersion {
		return true
	}
	// The established pre-0.6.5 batch schema was created only by verifier v2.
	// Do not reinterpret these implicit receipts after a future verifier bump.
	return block.VerifierVersion == 0 && blockVerifierVersion == 2
}

// Resolver lookups use a bounded window of the existing durable commits. They
// never build a second whole-chain transaction map. A truncated walk is unknown,
// including when it saw a match: older duplicate transaction IDs cannot then be
// ruled out. Existing verified locators/providers remain available as fallback.
func walkExplorerIndex(ctx context.Context, s *indexStore, visit func(indexBatch) error) (bool, error) {
	next, remaining := s.head.Commitment, indexExplorerMaxBytes
	var child *indexBatch
	for batches := 0; next != "" && batches < indexExplorerMaxBatches; batches++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !validHash(next) {
			return false, fmt.Errorf("invalid explorer checkpoint")
		}
		info, err := os.Stat(filepath.Join(s.dir, "commits", next+".json"))
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() || info.Size() < 1 {
			return false, fmt.Errorf("invalid explorer batch file")
		}
		if info.Size() > remaining {
			return false, nil
		}
		remaining -= info.Size()
		b, err := s.readBatch(next)
		if err != nil {
			return false, err
		}
		if child != nil && (child.Checkpoint.Height != b.Checkpoint.Height+1 || child.Checkpoint.From != b.Checkpoint.From || child.PreviousBlockHash != b.Checkpoint.BlockHash) {
			return false, fmt.Errorf("explorer checkpoint discontinuity")
		}
		if err = visit(b); err != nil {
			return false, err
		}
		next = b.Checkpoint.PreviousCommitment
		if next == "" && b.Checkpoint.Height != b.Checkpoint.From {
			return false, fmt.Errorf("explorer checkpoint missing prefix")
		}
		child = &b
	}
	return next == "", ctx.Err()
}

func (a *app) indexedTransaction(txid string) (txResolutionView, bool, error) {
	ctx := context.Background()
	if a.network != nil {
		ctx = a.network.ctx
	}
	return a.indexedTransactionContext(ctx, txid)
}

func (a *app) indexedTransactionContext(ctx context.Context, txid string) (txResolutionView, bool, error) {
	return a.indexedTransactionBeforeContext(ctx, txid, -1)
}

func transactionFitsHeightContext(height int64, coinbase bool, beforeHeight int64) bool {
	if beforeHeight < 0 {
		return true
	}
	if height > beforeHeight {
		return false
	}
	replacement, duplicate := bip30ReplacementHeight(height)
	return !coinbase || !duplicate || replacement > beforeHeight
}

// Internal input resolution has a consuming-block bound. BIP30 outpoints refer
// to the latest canonical incarnation at or before that block, whereas a public
// txid-only lookup has no such context and must preserve historical ambiguity.
func (a *app) indexedTransactionBeforeContext(ctx context.Context, txid string, beforeHeight int64) (txResolutionView, bool, error) {
	ctx, done := a.indexLookupContext(ctx)
	defer done()
	txid = strings.ToLower(strings.TrimSpace(txid))
	if !validHash(txid) {
		return txResolutionView{}, false, fmt.Errorf("invalid transaction identifier")
	}
	var location *txLocation
	var transaction *transactionView
	for _, id := range []string{"blocks", "tx-locator"} {
		if !releaseFeatureAvailable(id) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return txResolutionView{}, false, err
		}
		s, err := indexStoreHead(a.dataDir, id)
		if err != nil {
			return txResolutionView{}, false, err
		}
		if s.checkpoint == nil || a.indexChainState(s.checkpoint) != "selected_chain" {
			continue
		}
		if err = s.ensureIndexLocators(ctx); err != nil {
			return txResolutionView{}, false, err
		}
		var candidate *txLocation
		var retained *transactionView
		ambiguous := false
		visit := func(b indexBatch) error {
			if b.Bitcoin == nil || beforeHeight >= 0 && b.Checkpoint.Height > beforeHeight {
				return nil
			}
			for i, value := range b.Bitcoin.TxIDs {
				if value != txid {
					continue
				}
				if candidate != nil && (candidate.BlockHash != b.Checkpoint.BlockHash || candidate.Height != b.Checkpoint.Height || candidate.TxIndex != i) {
					if beforeHeight < 0 || candidate.Height == b.Checkpoint.Height {
						ambiguous = true
						continue
					}
					if candidate.Height > b.Checkpoint.Height {
						continue
					}
					// A newer locator must never inherit another incarnation's
					// retained evidence merely because its transaction ID matches.
					retained = nil
				}
				candidate = &txLocation{TxID: txid, Height: b.Checkpoint.Height, BlockHash: b.Checkpoint.BlockHash, TxIndex: i}
				if reusableBitcoinEvidence(b.Bitcoin) && len(b.Bitcoin.Transactions) == len(b.Bitcoin.TxIDs) {
					copy := b.Bitcoin.Transactions[i]
					if copy.TxID != txid {
						return fmt.Errorf("indexed transaction identity mismatch")
					}
					retained = &copy
				}
			}
			return nil
		}
		records, err := s.bitcoinLocatorRecords(ctx, txid)
		if err != nil {
			return txResolutionView{}, false, err
		}
		complete := true
		if len(records) == 0 {
			// Auxiliary fallback for old/repairable preview data; new commits
			// persist the compact pointer before publishing their head.
			complete, err = walkExplorerIndex(ctx, s, visit)
		} else {
			for _, record := range records {
				if err = ctx.Err(); err != nil {
					break
				}
				var b indexBatch
				var current bool
				b, current, err = a.validatedLocatorBatch(ctx, s, record)
				if err != nil {
					break
				}
				if !current {
					continue
				}
				if b.Bitcoin == nil || record.TxIndex >= len(b.Bitcoin.TxIDs) || b.Bitcoin.TxIDs[record.TxIndex] != txid {
					err = fmt.Errorf("locator pointer does not match committed transaction position")
					break
				}
				if err = visit(b); err != nil || ambiguous {
					break
				}
			}
		}
		if err != nil {
			return txResolutionView{}, false, err
		}
		if ambiguous {
			return txResolutionView{}, true, fmt.Errorf("historical transaction ID is ambiguous; supply its containing block")
		}
		if !complete || candidate == nil {
			continue
		}
		if location != nil && (location.BlockHash != candidate.BlockHash || location.TxIndex != candidate.TxIndex || location.Height != candidate.Height) {
			if beforeHeight < 0 || location.Height == candidate.Height {
				return txResolutionView{}, true, fmt.Errorf("historical transaction ID is ambiguous; supply its containing block")
			}
			if location.Height > candidate.Height {
				continue
			}
			transaction = nil
		}
		location = candidate
		if retained != nil {
			transaction = retained
		}
	}
	if err := ctx.Err(); err != nil {
		return txResolutionView{}, false, err
	}
	if location != nil {
		if !transactionFitsHeightContext(location.Height, location.TxIndex == 0, beforeHeight) {
			// An older partial index cannot establish the displaced incarnation
			// as current after its known replacement. Let another provider locate
			// the later block instead of inventing an ordinal origin.
			return txResolutionView{}, false, nil
		}
		if transaction != nil {
			return txResolutionView{TxID: txid, TxIndex: location.TxIndex, Height: location.Height, BlockHash: location.BlockHash, Transaction: *transaction, SourceNetwork: "local_index", LocatorPeer: "locally derived Bitcoin index", LocatorVerified: true, TransactionVerified: true, VerificationState: "header_anchored"}, true, nil
		}
		r, err := a.verifyTxLocation(txid, *location, "committed transaction locator")
		if err != nil {
			return txResolutionView{TxID: txid, TxIndex: location.TxIndex, Height: location.Height, BlockHash: location.BlockHash, SourceNetwork: "local_index_locator", LocatorPeer: "committed transaction locator", LocatorVerified: true, TransactionVerified: false, VerificationState: "header_anchored_locator", ResolutionState: "located_bytes_unavailable", Note: "Saved containing-block location is available. Verified transaction bytes require a block source; output, witness and sat state remain unknown."}, true, nil
		}
		r.TxIndex = location.TxIndex
		return r, true, nil
	}
	return txResolutionView{}, false, nil
}

func (a *app) validatedLocatorBatch(ctx context.Context, s *indexStore, record indexLocatorRecord) (indexBatch, bool, error) {
	if s.checkpoint == nil || record.Height < s.checkpoint.From || record.Height > s.checkpoint.Height {
		return indexBatch{}, false, nil
	}
	active, ready, err := s.activeLocatorCommitContext(ctx, record.Height)
	if err != nil {
		return indexBatch{}, false, err
	}
	if !ready {
		return indexBatch{}, false, fmt.Errorf("locator commit membership unavailable; resume required")
	}
	if active != record.Commit {
		return indexBatch{}, false, nil
	}
	b, err := s.readBatch(record.Commit)
	if err != nil {
		return b, false, err
	}
	if b.Checkpoint.Height != record.Height || b.Checkpoint.From != s.checkpoint.From {
		return b, false, fmt.Errorf("locator pointer exceeds committed index range")
	}
	hash, err := a.canonicalHashAtHeight(record.Height)
	if err != nil {
		return b, false, err
	}
	return b, strings.EqualFold(hash, b.Checkpoint.BlockHash), nil
}

func (a *app) indexedInscription(ctx context.Context, id string) (ordRecord, bool, error) {
	ctx, done := a.indexLookupContext(ctx)
	defer done()
	if err := ctx.Err(); err != nil {
		return ordRecord{}, false, err
	}
	txid, index, err := inscriptionParts(id)
	if err != nil {
		return ordRecord{}, false, err
	}
	id = fmt.Sprintf("%si%d", txid, index)
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		return ordRecord{}, false, err
	}
	if s.checkpoint == nil || a.indexChainState(s.checkpoint) != "selected_chain" {
		return ordRecord{}, false, nil
	}
	if err = s.ensureIndexLocators(ctx); err != nil {
		return ordRecord{}, false, err
	}
	var found *ordRecord
	visit := func(b indexBatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, o := range b.Inscriptions {
			if o.ID != id {
				continue
			}
			if o.TxID != txid || int(o.Index) != index || o.ID != fmt.Sprintf("%si%d", o.TxID, o.Index) || o.ParserProfile != inscriptionParserProfile || o.BlockHash != b.Checkpoint.BlockHash || o.BlockHeight != b.Checkpoint.Height {
				return fmt.Errorf("indexed inscription profile or anchor mismatch")
			}
			sum := sha256.Sum256(o.Body)
			if hex.EncodeToString(sum[:]) != o.ContentSHA256 {
				return fmt.Errorf("indexed inscription content hash mismatch")
			}
			env := o.Envelope
			env.Body = o.Body
			r := ordRecord{Schema: 1, VerifierVersion: blockVerifierVersion, ID: o.ID, TxID: o.TxID, Index: int(o.Index), BlockHash: o.BlockHash, Height: o.BlockHeight, Envelope: env, Size: len(o.Body), SHA256: o.ContentSHA256, Evidence: "header_anchored", Provider: "local_occurrence_index", Profile: o.ParserProfile, Interpretation: "indexed_occurrence", InitialState: "not_resolved", ContentURL: a.ordContentURL(o.ID), Note: "Content retained by the local occurrence index. Canonical numbering and ownership are not inferred."}
			if found != nil && !strings.EqualFold(found.BlockHash, r.BlockHash) {
				return fmt.Errorf("ambiguous indexed inscription; supply a reveal block")
			}
			found = &r
		}
		return nil
	}
	sum := sha256.Sum256([]byte(id))
	records, err := s.bitcoinLocatorRecords(ctx, hex.EncodeToString(sum[:]))
	if err != nil {
		return ordRecord{}, false, err
	}
	complete := true
	if len(records) == 0 {
		complete, err = walkExplorerIndex(ctx, s, visit)
	} else {
		for _, record := range records {
			if err = ctx.Err(); err != nil {
				break
			}
			var b indexBatch
			var current bool
			b, current, err = a.validatedLocatorBatch(ctx, s, record)
			if err != nil {
				break
			}
			if !current {
				continue
			}
			if record.TxIndex >= len(b.Inscriptions) || b.Inscriptions[record.TxIndex].ID != id {
				err = fmt.Errorf("occurrence pointer does not match committed inscription identity")
				break
			}
			if err = visit(b); err != nil {
				break
			}
		}
	}
	if err != nil {
		return ordRecord{}, false, err
	}
	if !complete || found == nil {
		return ordRecord{}, false, nil
	}
	if err = ctx.Err(); err != nil {
		return ordRecord{}, false, err
	}
	if err = a.saveOrdRecord(*found, found.Envelope.Body); err != nil {
		return ordRecord{}, true, err
	}
	return *found, true, nil
}
