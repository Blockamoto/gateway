package main

// Deferred dependency bodies are operation-local facts, never Raw membership.
// Keep a small resident window; spill excess under an owned private temp path.
// The expected digest and parser receipts stay in trusted process metadata.
import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const resolvedResidentBlocks = 8
const resolvedResidentBytes int64 = 32 << 20
const resolvedSpoolRecordBytes = 64 << 20
const resolvedQueuedBytes int64 = 1 << 30
const resolvedDependencyRecords = inscriptionMaxPayloadItems
const resolvedResultExamples = 4096

type resolvedQueuedBlock struct {
	block     *blockView
	path      string
	digest    [32]byte
	height    int64
	hash      string
	bytes     int64
	rawProof  *verifiedBlockBytes
	inventory *inscriptionInventoryReceipt
	timing    blockSourceTiming
}
type resolvedSpoolBlock struct {
	Block      blockView               `json:"block"`
	Raw        []byte                  `json:"raw,omitempty"`
	Extraction *inscriptionBlockResult `json:"extraction,omitempty"`
}

func queueResolvedBlock(q *resolvedIngestionQueue, block blockView) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return fmt.Errorf("resolved source operation already closed")
	}
	if q.problem != nil {
		return q.problem
	}
	key := fmt.Sprintf("%d:%s", block.Height, block.Hash)
	if q.seen == nil {
		q.seen = map[string]bool{}
	}
	if q.seen[key] {
		return nil
	}
	if len(q.seen) >= resolvedDependencyRecords {
		q.problem = fmt.Errorf("resolved source dependency metadata exceeds its protocol-sized operation budget")
		return q.problem
	}
	encoded, err := json.Marshal(resolvedSpoolBlock{Block: block, Raw: block.verifiedRaw, Extraction: block.inscriptionExtraction})
	if err != nil {
		q.problem = err
		return err
	}
	if len(encoded) > resolvedSpoolRecordBytes {
		q.problem = fmt.Errorf("individual deferred source exceeds its bounded fact buffer")
		return q.problem
	}
	limit := q.maxQueuedBytes
	if limit == 0 || limit > resolvedQueuedBytes {
		limit = resolvedQueuedBytes
	}
	if int64(len(encoded)) > limit-q.queuedBytes {
		q.problem = fmt.Errorf("deferred source queue exceeds its bounded operation spool quota")
		return q.problem
	}
	entry := resolvedQueuedBlock{height: block.Height, hash: block.Hash, bytes: int64(len(encoded)), digest: sha256.Sum256(encoded), timing: block.sourceTiming}
	if block.verifiedRawProof != nil {
		proof := *block.verifiedRawProof
		entry.rawProof = &proof
	}
	if block.inscriptionExtraction != nil && block.inscriptionExtraction.inventoryReceipt != nil {
		receipt := *block.inscriptionExtraction.inventoryReceipt
		entry.inventory = &receipt
	}
	maxBlocks, maxBytes := q.maxResidentBlocks, q.maxResidentBytes
	if maxBlocks == 0 {
		maxBlocks = resolvedResidentBlocks
	}
	if maxBytes == 0 {
		maxBytes = resolvedResidentBytes
	}
	if q.residentBlocks < maxBlocks && q.residentBytes+entry.bytes <= maxBytes {
		entry.block = &block
		q.residentBlocks++
		q.residentBytes += entry.bytes
	} else {
		if q.spoolDir == "" {
			q.spoolDir, err = os.MkdirTemp("", "gateway-source-fanout-")
			if err != nil {
				q.problem = err
				return err
			}
		}
		file, err := os.CreateTemp(q.spoolDir, "block-*.json")
		if err != nil {
			q.problem = err
			return err
		}
		entry.path = file.Name()
		if err = file.Chmod(0600); err == nil {
			_, err = file.Write(encoded)
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(entry.path)
			q.problem = err
			return err
		}
	}
	q.queuedBytes += entry.bytes
	q.seen[key] = true
	q.blocks = append(q.blocks, entry)
	return nil
}
func (q *resolvedIngestionQueue) fail(err error) {
	if err == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.problem == nil {
		q.problem = err
	}
}
func (q *resolvedIngestionQueue) err() error { q.mu.Lock(); defer q.mu.Unlock(); return q.problem }
func (q *resolvedIngestionQueue) next() (blockView, bool, error) {
	q.mu.Lock()
	if q.problem != nil {
		err := q.problem
		q.mu.Unlock()
		return blockView{}, false, err
	}
	if len(q.blocks) == 0 {
		q.mu.Unlock()
		return blockView{}, false, nil
	}
	entry := q.blocks[0]
	q.blocks[0] = resolvedQueuedBlock{}
	q.blocks = q.blocks[1:]
	if entry.block != nil {
		q.residentBlocks--
		q.residentBytes -= entry.bytes
	}
	q.mu.Unlock()
	if entry.block != nil {
		q.mu.Lock()
		q.queuedBytes -= entry.bytes
		q.mu.Unlock()
		return *entry.block, true, nil
	}
	file, err := os.Open(entry.path)
	if err != nil {
		return blockView{}, false, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() != entry.bytes || info.Size() > resolvedSpoolRecordBytes {
		_ = file.Close()
		return blockView{}, false, fmt.Errorf("deferred source spool extent changed")
	}
	encoded := make([]byte, int(entry.bytes))
	_, err = file.ReadAt(encoded, 0)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return blockView{}, false, err
	}
	if sha256.Sum256(encoded) != entry.digest {
		return blockView{}, false, fmt.Errorf("deferred source spool checksum mismatch")
	}
	var stored resolvedSpoolBlock
	if err = json.Unmarshal(encoded, &stored); err != nil {
		return blockView{}, false, err
	}
	block := stored.Block
	if block.Height != entry.height || block.Hash != entry.hash || !integrityVerified(block) || block.TransactionCount != uint64(len(block.Transactions)) {
		return blockView{}, false, fmt.Errorf("deferred source spool identity changed")
	}
	block.sourceTiming = entry.timing
	block.verifiedRaw = stored.Raw
	if entry.rawProof != nil {
		proof := *entry.rawProof
		if proof.Hash != block.Hash || proof.Height != block.Height || proof.Transactions != block.TransactionCount || len(stored.Raw) != proof.Bytes || len(stored.Raw) < 81 || sha256.Sum256(stored.Raw) != proof.SHA || !bytes.Equal(stored.Raw[:80], proof.Header[:]) {
			return blockView{}, false, fmt.Errorf("deferred source raw receipt differs")
		}
		block.verifiedRawProof = &proof
	}
	for i := range block.Transactions {
		value, err := displayHashRaw(block.Transactions[i].TxID)
		if err != nil {
			return blockView{}, false, err
		}
		block.Transactions[i]._txHash = value
	}
	block.inscriptionExtraction = stored.Extraction
	if entry.inventory != nil {
		if stored.Extraction == nil {
			return blockView{}, false, fmt.Errorf("deferred source lost its inventory")
		}
		receipt := *entry.inventory
		stored.Extraction.inventoryReceipt = &receipt
		if err = validateCompleteInscriptionInventory(block, stored.Extraction); err != nil {
			return blockView{}, false, err
		}
	}
	if err = os.Remove(entry.path); err != nil {
		return blockView{}, false, err
	}
	q.mu.Lock()
	q.queuedBytes -= entry.bytes
	q.mu.Unlock()
	return block, true, nil
}
func (q *resolvedIngestionQueue) addResults(rows ...indexBrowseResult) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, row := range rows {
		if len(q.results) < resolvedResultExamples {
			q.results = append(q.results, row)
		} else if row.State == "error" || row.State == "unavailable" {
			q.results[len(q.results)-1] = row
		} else if q.results[len(q.results)-1].State != "error" && q.results[len(q.results)-1].State != "unavailable" {
			q.results[len(q.results)-1] = indexBrowseResult{"resolved", "summary", "Additional result examples omitted; verified source delivery continues."}
		}
	}
}
func (q *resolvedIngestionQueue) close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return q.problem
	}
	q.closed = true
	q.blocks = nil
	q.seen = nil
	q.residentBlocks = 0
	q.residentBytes = 0
	q.queuedBytes = 0
	if q.spoolDir != "" {
		absolute, err := filepath.Abs(q.spoolDir)
		if err != nil {
			return err
		}
		parent, err := filepath.Abs(os.TempDir())
		if err != nil {
			return err
		}
		if filepath.Dir(absolute) != parent || !strings.HasPrefix(filepath.Base(absolute), "gateway-source-fanout-") {
			return fmt.Errorf("refusing unexpected deferred source cleanup path")
		}
		if err = os.RemoveAll(absolute); err != nil {
			return err
		}
	}
	return nil
}
