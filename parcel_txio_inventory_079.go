package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// During the explicit full-prefix audit, callers supply the checked Bitcoin
// spending block plus selected-chain committed creation catalogues. This
// verifies the input inventory, which TXIO's v3 rows alone cannot prove.
// Every creation catalogue is independently compared with its Bitcoin block
// when the audit reaches that height before the audit can become complete.
func verifyParcelTXIOInputInventory079(ctx context.Context, batch indexBatch, block blockView, catalog txioTranslationBlock, creation func(context.Context, int64) (indexBatch, txioTranslationBlock, error)) error {
	p := batch.Spenders
	if p == nil || p.Version != 2 && p.Version != 3 || !integrityVerified(block) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) || block.TransactionCount != uint64(len(block.Transactions)) || block.Height != batch.Checkpoint.Height || !strings.EqualFold(block.Hash, batch.Checkpoint.BlockHash) || catalog.Height != block.Height || !strings.EqualFold(catalog.BlockHash, block.Hash) || len(catalog.Transactions) != len(block.Transactions) {
		return fmt.Errorf("TXIO checked input inventory block/catalog mismatch")
	}
	if p.Version == 3 && (len(block.Transactions) != int(p.TransactionCount) || len(p.OutputCounts) != len(block.Transactions)) || p.Version == 2 && len(p.Origins) != len(block.Transactions) {
		return fmt.Errorf("TXIO transaction inventory count mismatch")
	}
	if len(p.PendingTranslations) != 0 {
		return fmt.Errorf("TXIO input inventory contains unresolved links")
	}
	for i, tx := range block.Transactions {
		origin := catalog.Transactions[i]
		if tx.Index != i || origin.TxIndex != i || !strings.EqualFold(tx.TxID, origin.TxID) || tx.Coinbase != origin.Coinbase || origin.Coinbase != (i == 0) || uint64(len(tx.Outputs)) != uint64(origin.Outputs) || p.Version == 3 && origin.Outputs != p.OutputCounts[i] || p.Version == 2 && origin != p.Origins[i] {
			return fmt.Errorf("TXIO transaction inventory mismatch at block %d position %d", block.Height, i)
		}
	}
	aliases := make(map[string]txioTranslationParent, len(catalog.Parents))
	for _, alias := range catalog.Parents {
		if _, exists := aliases[alias.Output]; exists {
			return fmt.Errorf("duplicate TXIO creation alias")
		}
		aliases[alias.Output] = alias
	}
	used := map[string]bool{}
	rowIndex := 0
	for txIndex, tx := range block.Transactions {
		if tx.Coinbase {
			continue
		}
		for vin, input := range tx.Inputs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if input.Coinbase || rowIndex >= len(p.Rows) {
				return fmt.Errorf("TXIO input inventory incomplete at %s", inputCoordinate(vin, txIndex, block.Height))
			}
			row := txioSpendRow(p, rowIndex)
			rowIndex++
			if row.TxIndex != txIndex || row.Vin != vin || row.Vout != input.PrevVout || row.Output == "" {
				return fmt.Errorf("TXIO input link mismatch at %s", inputCoordinate(vin, txIndex, block.Height))
			}
			if p.Version == 2 && (!strings.EqualFold(row.PrevTxID, input.PrevTxID) || !strings.EqualFold(row.TxID, tx.TxID)) {
				return fmt.Errorf("TXIO input native IDs do not match checked transaction")
			}
			pos, ok := parseBODCoordinate(row.Output)
			if !ok || pos.Kind != coordOutput || pos.TxID != "" || pos.Height > block.Height || pos.Height == block.Height && pos.TxIndex >= txIndex || pos.IOIndex < 0 || uint64(pos.IOIndex) != uint64(input.PrevVout) {
				return fmt.Errorf("TXIO creation position mismatch")
			}
			alias, ok := aliases[row.Output]
			if !ok || !strings.EqualFold(alias.TxID, input.PrevTxID) || !strings.EqualFold(alias.BlockHash, p.CreationAnchors[strconv.FormatInt(pos.Height, 10)]) {
				return fmt.Errorf("TXIO creation alias does not match input outpoint")
			}
			used[row.Output] = true
			originalBatch, originalCatalog := batch, catalog
			if pos.Height != block.Height {
				if creation == nil {
					return fmt.Errorf("TXIO creation catalogue unavailable at %d", pos.Height)
				}
				var err error
				originalBatch, originalCatalog, err = creation(ctx, pos.Height)
				if err != nil {
					return err
				}
			}
			if originalBatch.Spenders == nil || originalBatch.Spenders.Version != 2 && originalBatch.Spenders.Version != 3 || originalBatch.Checkpoint.Height != pos.Height || !strings.EqualFold(originalBatch.Checkpoint.BlockHash, alias.BlockHash) || originalCatalog.Height != pos.Height || !strings.EqualFold(originalCatalog.BlockHash, alias.BlockHash) || pos.TxIndex < 0 || pos.TxIndex >= len(originalCatalog.Transactions) {
				return fmt.Errorf("TXIO original catalogue unavailable or changed")
			}
			origin := originalCatalog.Transactions[pos.TxIndex]
			stored := originalBatch.Spenders
			if stored.Version == 3 && (pos.TxIndex >= len(stored.OutputCounts) || origin.Outputs != stored.OutputCounts[pos.TxIndex]) || stored.Version == 2 && (pos.TxIndex >= len(stored.Origins) || origin != stored.Origins[pos.TxIndex]) || origin.TxIndex != pos.TxIndex || !strings.EqualFold(origin.TxID, input.PrevTxID) || uint64(pos.IOIndex) >= uint64(origin.Outputs) || !transactionFitsHeightContext(pos.Height, origin.Coinbase, block.Height) {
				return fmt.Errorf("TXIO original transaction/output does not match input")
			}
		}
	}
	if rowIndex != len(p.Rows) {
		return fmt.Errorf("TXIO has extra input rows beyond the checked block inventory")
	}
	if len(used) != len(aliases) {
		return fmt.Errorf("TXIO has unmatched creation aliases")
	}
	return nil
}
