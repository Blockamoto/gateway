package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
)

// Every possible txid-prefix shard has a receipt, including empty shards. An
// absent file is not evidence of emptiness unless its durable receipt says so.
// Rows bind the shard number and expected byte length with a checksum. Existing
// record CRCs detect damaged contents; the length catches deletion/truncation,
// including truncation at a complete-record boundary.
const graphReceiptMagic = "GWGRPH02"
const graphReceiptRows = 2 * 65536 // tx locations, then spends
const graphReceiptRowSize = 12
const graphReceiptSize = len(graphReceiptMagic) + graphReceiptRows*graphReceiptRowSize

func (a *app) graphReceiptsPath() string { return filepath.Join(a.graphDir(), "shard-lengths.dat") }

func graphReceiptRow(index uint32, size uint64) []byte {
	b := make([]byte, graphReceiptRowSize)
	binary.LittleEndian.PutUint64(b, size)
	var committed [12]byte
	binary.LittleEndian.PutUint32(committed[:4], index)
	copy(committed[4:], b[:8])
	binary.LittleEndian.PutUint32(b[8:], crc32.ChecksumIEEE(committed[:]))
	return b
}

func (a *app) initGraphShardReceipts() error {
	b := make([]byte, graphReceiptSize)
	copy(b, graphReceiptMagic)
	for i := uint32(0); i < graphReceiptRows; i++ {
		copy(b[len(graphReceiptMagic)+int(i)*graphReceiptRowSize:], graphReceiptRow(i, 0))
	}
	return atomicWriteBytes(a.graphReceiptsPath(), b)
}

func graphReceiptIndex(path, kind string) (uint32, error) {
	name := filepath.Base(path)
	if len(name) != 6 || name[2:] != ".bin" {
		return 0, fmt.Errorf("invalid graph shard path")
	}
	prefix := filepath.Base(filepath.Dir(path)) + name[:2]
	x, err := strconv.ParseUint(prefix, 16, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid graph shard path")
	}
	if kind == "spends" {
		x += 65536
	} else if kind != "txloc" {
		return 0, fmt.Errorf("invalid graph shard kind")
	}
	return uint32(x), nil
}

func (a *app) graphExpectedShardSize(path, kind string) (uint64, error) {
	index, err := graphReceiptIndex(path, kind)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(a.graphReceiptsPath())
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() != int64(graphReceiptSize) {
		return 0, fmt.Errorf("missing or damaged graph shard receipts")
	}
	header := make([]byte, len(graphReceiptMagic))
	if _, err := f.ReadAt(header, 0); err != nil || string(header) != graphReceiptMagic {
		return 0, fmt.Errorf("invalid graph shard receipt header")
	}
	b := make([]byte, graphReceiptRowSize)
	if _, err := f.ReadAt(b, int64(len(graphReceiptMagic))+int64(index)*graphReceiptRowSize); err != nil {
		return 0, err
	}
	size := binary.LittleEndian.Uint64(b)
	expected := graphReceiptRow(index, size)
	if binary.LittleEndian.Uint32(b[8:]) != binary.LittleEndian.Uint32(expected[8:]) {
		return 0, fmt.Errorf("damaged graph shard receipt")
	}
	return size, nil
}

// Callers hold graphStoreMu while checking or changing shard files/receipts.
func (a *app) checkGraphShard(path, kind string) error {
	expected, err := a.graphExpectedShardSize(path, kind)
	if err != nil {
		return err
	}
	recordSize := uint64(graphTxRecordSize)
	if kind == "spends" {
		recordSize = graphSpendRecordSize
	}
	if expected%recordSize != 0 {
		return fmt.Errorf("invalid graph shard length receipt")
	}
	st, err := os.Stat(path)
	if os.IsNotExist(err) && expected == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if st.IsDir() || uint64(st.Size()) != expected {
		return fmt.Errorf("graph shard length does not match its receipt; rebuild required")
	}
	return nil
}

func (a *app) appendGraphRecords(path, kind string, records [][]byte) error {
	if err := a.checkGraphShard(path, kind); err != nil {
		return err
	}
	if err := appendRecords(path, records); err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	index, err := graphReceiptIndex(path, kind)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(a.graphReceiptsPath(), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteAt(graphReceiptRow(index, uint64(st.Size())), int64(len(graphReceiptMagic))+int64(index)*graphReceiptRowSize); err != nil {
		return err
	}
	// Data was synced by appendRecords. Commit its expected length before the
	// block marker and coverage can advance. A torn row fails its checksum.
	return f.Sync()
}
