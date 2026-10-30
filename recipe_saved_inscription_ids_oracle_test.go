package main

import "fmt"

// Frozen saved-position projection from before JSON migration.
func sparseTransactionsFromInscriptionOracle(source indexBatch, block *blockView) (*transactionLocatorPayload, error) {
	coordinates := inscriptionBatchCoordinates(source)
	var count *uint32
	if source.InscriptionBlock != nil {
		value := source.InscriptionBlock.TransactionCount
		count = &value
	}
	payload := &transactionLocatorPayload{Block: transactionLocatorBlock{Height: source.Checkpoint.Height, TransactionCount: count, Transactions: []transactionLocatorEntry{}}, Completeness: "sparse", Selection: "inscription_reveals", SourceCommitment: source.Checkpoint.Commitment}
	if !validHash(payload.SourceCommitment) {
		return nil, fmt.Errorf("inscription source is not committed")
	}
	if source.Checkpoint.Definition != "inscriptions" || checkpointHash(source.Checkpoint) != payload.SourceCommitment || source.Checkpoint.RecordsHash != batchRecordsHash(source) {
		return nil, fmt.Errorf("inscription source commitment is invalid")
	}
	if err := validateInscriptionPayload(source); err != nil {
		return nil, err
	}
	if block != nil && (!integrityVerified(*block) || block.Hash != source.Checkpoint.BlockHash || block.Height != source.Checkpoint.Height) {
		return nil, fmt.Errorf("enrichment block does not match committed source")
	}
	if block != nil && count != nil && uint64(len(block.Transactions)) != uint64(*count) {
		return nil, fmt.Errorf("enrichment transaction count differs from committed source")
	}
	if block != nil {
		for i, tx := range block.Transactions {
			if tx.Index != i || !validHash(tx.TxID) {
				return nil, fmt.Errorf("enrichment requires ordered verified transactions")
			}
		}
	}
	for i, coordinate := range coordinates {
		position, ok := parseBODCoordinate(coordinate)
		if !ok || position.Kind != coordInscription || position.Height != source.Checkpoint.Height {
			return nil, fmt.Errorf("invalid source inscription position")
		}
		if len(payload.Block.Transactions) > 0 && int(payload.Block.Transactions[len(payload.Block.Transactions)-1].TxIndex) == position.TxIndex {
			continue
		}
		var txid string
		if len(source.Inscriptions) == len(coordinates) {
			txid = source.Inscriptions[i].TxID
		} else if block != nil && position.TxIndex < len(block.Transactions) {
			txid = block.Transactions[position.TxIndex].TxID
		} else {
			return nil, fmt.Errorf("inscription positions need their verified reveal block")
		}
		if !validHash(txid) {
			return nil, fmt.Errorf("invalid reveal transaction identifier")
		}
		payload.Block.Transactions = append(payload.Block.Transactions, transactionLocatorEntry{uint32(position.TxIndex), txid})
	}
	return payload, nil
}
