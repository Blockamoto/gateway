package main

import (
	"context"
	"fmt"
	"strconv"
)

// Frozen pre-composition v3 creation/proof selection, test-only.
func (s *indexStore) deriveTXIOBlockOracle(block blockView) (*indexSpenderBlock, error) {
	ctx := s.locatorContext
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := deriveIndexSpendersOracle(block)
	if err != nil {
		return nil, err
	}
	out.Version = 3
	out.CreationAnchors = map[string]string{}
	out.TransactionCount = uint32(len(block.Transactions))
	for _, tx := range block.Transactions {
		out.OutputCounts = append(out.OutputCounts, uint32(len(tx.Outputs)))
	}
	parents := []txioTranslationParent{}
	parentSeen := map[string]bool{}
	out.Values, out.Scripts = s.retainOutputValues, s.retainOutputScripts
	if out.Values || out.Scripts {
		out.OutputFacts, err = storeBlockOutputFactLayersContext(ctx, s.profileRoot, block, out.Values, out.Scripts)
		if err != nil {
			return nil, err
		}
	}
	for i := range out.Rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r := &out.Rows[i]
		in := block.Transactions[r.TxIndex].Inputs[r.Vin]
		creation := txioCreation{}
		if coin, known := runPrevout(ctx, r.TxIndex, r.Vin); known && coin.TxID == in.PrevTxID && coin.Vout == in.PrevVout {
			creation = txioCreation{coin.Position, coin.BlockHash}
		}
		// Transaction order is authoritative for same-block spends, including
		// duplicate txid incarnations. Never use a later transaction in this block.
		for j := r.TxIndex - 1; creation.Position == "" && j >= 0; j-- {
			tx := block.Transactions[j]
			if tx.TxID == in.PrevTxID {
				if int(in.PrevVout) >= len(tx.Outputs) {
					return nil, fmt.Errorf("previous output is absent from its latest same-block incarnation")
				}
				creation = txioCreation{outputCoordinate(int(in.PrevVout), j, block.Height), block.Hash}
				break
			}
		}
		if creation.Position == "" {
			creation, err = s.ownTXIOCreation(ctx, in, block.Height)
			if err != nil {
				return nil, err
			}
		}
		if creation.Position == "" && s.spenderOrigin != nil {
			creation, err = s.spenderOrigin(ctx, in, block.Height, r.TxIndex)
			if err != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if creation.Position != "" {
			c, ok := parseBODCoordinate(creation.Position)
			if !ok || c.Kind != coordOutput || uint64(c.IOIndex) != uint64(in.PrevVout) || c.Height > block.Height || c.Height == block.Height && c.TxIndex >= r.TxIndex || !validHash(creation.BlockHash) {
				return nil, fmt.Errorf("TXIO provider returned invalid creation evidence")
			}
			h := strconv.FormatInt(c.Height, 10)
			if old := out.CreationAnchors[h]; old != "" && old != creation.BlockHash {
				return nil, fmt.Errorf("TXIO creation providers disagree on chain identity")
			}
			out.CreationAnchors[h] = creation.BlockHash
			r.Output = creation.Position
			if !parentSeen[r.Output] {
				parents = append(parents, txioTranslationParent{r.PrevTxID, r.Output, creation.BlockHash})
				parentSeen[r.Output] = true
			}
		} else { // Pending translation is separate from resolved positional rows.
			out.PendingTranslations = append(out.PendingTranslations, txioPendingTranslation{uint32(i), r.PrevTxID})
		}
		r.PrevTxID = ""
		r.TxID = ""
	}
	out.LocatorRef, err = s.storeTXIOTranslation(ctx, block, parents...)
	if err != nil {
		return nil, err
	}
	if err = validateTXIOBatch(indexBatch{Checkpoint: indexCheckpoint{Height: block.Height, BlockHash: block.Hash}, Spenders: out}); err != nil {
		return nil, err
	}
	return out, nil
}
