package main

// A checksummed, transaction-framed append journal. A transaction is visible on
// recovery only after its commit frame. Files are synced before attempts start.
// Compaction replaces the journal without touching Core, settings or block data.
import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ledgerMagic053 = "GATEWAY-DISCOVERY-1\n"
const ledgerMaxFrame053 = 4 * 1024 * 1024

type ledgerFrame053 struct {
	Kind       string               `json:"kind"`
	Commit     uint64               `json:"commit"`
	Rows       []bitcoinAddress     `json:"rows,omitempty"`
	Meta       *discoveryLedgerMeta `json:"meta,omitempty"`
	Discovery  *discoveryState      `json:"discovery,omitempty"`
	WorkCursor [6]uint64            `json:"work_cursor,omitempty"`
	RoleCursor int                  `json:"role_cursor,omitempty"`
	RoleTurns  [3]uint64            `json:"role_turns,omitempty"`
}

func (n *bitcoinNetwork) ledgerPath() string {
	return filepath.Join(n.app.dataDir, "discovery-ledger-v1.wal")
}

func writeLedgerFrame053(w io.Writer, f ledgerFrame053) (int64, error) {
	b, e := json.Marshal(f)
	if e != nil {
		return 0, e
	}
	if len(b) > ledgerMaxFrame053 {
		return 0, fmt.Errorf("discovery journal frame exceeds its bound")
	}
	var h [8]byte
	binary.LittleEndian.PutUint32(h[:4], uint32(len(b)))
	binary.LittleEndian.PutUint32(h[4:], crc32.ChecksumIEEE(b))
	if _, e = w.Write(h[:]); e != nil {
		return 0, e
	}
	if _, e = w.Write(b); e != nil {
		return 0, e
	}
	return int64(len(b) + 8), nil
}

func (n *bitcoinNetwork) writeLedgerTransactionLocked(w io.Writer, rows []*bitcoinAddress, commit uint64) (int64, error) {
	total, e := writeLedgerFrame053(w, ledgerFrame053{Kind: "begin", Commit: commit})
	if e != nil {
		return total, e
	}
	for i := 0; i < len(rows); i += 256 {
		end := i + 256
		if end > len(rows) {
			end = len(rows)
		}
		batch := make([]bitcoinAddress, 0, end-i)
		for _, x := range rows[i:end] {
			batch = append(batch, *x)
		}
		size, e := writeLedgerFrame053(w, ledgerFrame053{Kind: "rows", Commit: commit, Rows: batch})
		total += size
		if e != nil {
			return total, e
		}
	}
	meta := n.ledgerMeta
	meta.IntakeLimited = n.ledgerLimited
	meta.LastCommit = n.now()
	size, e := writeLedgerFrame053(w, ledgerFrame053{Kind: "commit", Commit: commit, Meta: &meta, Discovery: &n.discovery, WorkCursor: n.workCursor, RoleCursor: n.roleCursor, RoleTurns: n.roleTurns})
	total += size
	return total, e
}

func (n *bitcoinNetwork) persistLedger() bool {
	n.saveMu.Lock()
	defer n.saveMu.Unlock()
	n.mu.Lock()
	defer n.mu.Unlock()
	// Corruption is not repaired by overwriting the evidence. A transient write
	// failure requires correcting storage and restarting, not silent truncation.
	if n.ledgerError != "" {
		return false
	}
	if !n.ledgerMetaDirty && len(n.ledgerDirty) == 0 && !n.ledgerMeta.LastCommit.IsZero() && n.now().Sub(n.ledgerMeta.LastCommit) < 10*time.Second {
		return true
	}
	if e := os.MkdirAll(n.app.dataDir, 0700); e != nil {
		n.ledgerError = e.Error()
		return false
	}
	rows := make([]*bitcoinAddress, 0, len(n.ledgerDirty))
	for address := range n.ledgerDirty {
		if x := n.ledger[address]; x != nil {
			rows = append(rows, x)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Ordinal < rows[j].Ordinal })
	sizeEstimate := ledgerReserve053(len(rows))
	for _, x := range rows {
		sizeEstimate += x.serializedBytes + 8
	}
	compact := n.ledgerBytes == 0 || n.ledgerBytes+sizeEstimate > n.ledgerMeta.MaxBytes || n.ledgerBytes > 2*n.ledgerCompactBytes+8*1024*1024
	path := n.ledgerPath()
	commit := n.ledgerCommitted + 1
	var e error
	if compact {
		rows = rows[:0]
		for _, addr := range n.ledgerOrder {
			if x := n.ledger[addr]; x != nil {
				rows = append(rows, x)
			}
		}
		// One owned staging filename bounds crash leftovers to one checkpoint.
		// Exclusive creation after removal never follows a preexisting symlink.
		temp := filepath.Join(n.app.dataDir, ".discovery-checkpoint.tmp")
		if info, statErr := os.Lstat(temp); statErr == nil {
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				n.ledgerError = "Discovery checkpoint path is not a file; preserved"
				return false
			}
			if removeErr := os.Remove(temp); removeErr != nil {
				n.ledgerError = removeErr.Error()
				return false
			}
		} else if !os.IsNotExist(statErr) {
			n.ledgerError = statErr.Error()
			return false
		}
		f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			n.ledgerError = err.Error()
			return false
		}
		defer os.Remove(temp)
		writer := bufio.NewWriterSize(f, 128*1024)
		_, e = writer.WriteString(ledgerMagic053)
		if e == nil {
			_, e = n.writeLedgerTransactionLocked(writer, rows, commit)
		}
		if e == nil {
			e = writer.Flush()
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e == nil {
			e = os.Rename(temp, path)
		}
		if e == nil {
			e = syncDirectory(n.app.dataDir)
		}
	} else {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			n.ledgerError = err.Error()
			return false
		}
		start := n.ledgerBytes
		writer := bufio.NewWriterSize(f, 128*1024)
		_, e = n.writeLedgerTransactionLocked(writer, rows, commit)
		if e == nil {
			e = writer.Flush()
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			// Remove our incomplete append; previously committed frames remain.
			if tErr := os.Truncate(path, start); tErr != nil {
				e = fmt.Errorf("%v; rollback: %v", e, tErr)
			}
		}
	}
	if e != nil {
		n.ledgerError = "Discovery persistence failed: " + e.Error()
		n.eventLocked("history_error", "", n.ledgerError)
		return false
	}
	info, e := os.Stat(path)
	if e != nil {
		n.ledgerError = e.Error()
		return false
	}
	n.ledgerBytes = info.Size()
	n.ledgerCommitted = commit
	n.ledgerMeta.LastCommit = n.now()
	n.ledgerDirty = make(map[string]bool)
	n.ledgerMetaDirty = false
	if n.ledgerBytes > n.ledgerMeta.MaxBytes {
		n.ledgerLimited = true
	}
	return true
}

func (n *bitcoinNetwork) restoreLedger() bool {
	f, e := os.Open(n.ledgerPath())
	if os.IsNotExist(e) {
		return false
	}
	if e != nil {
		n.ledgerError = e.Error()
		return true
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		n.ledgerError = e.Error()
		return true
	}
	// Hard reader bound protects against tampered local files before allocating.
	if info.Size() > 2*1024*1024*1024 {
		n.ledgerError = "Discovery journal is over the supported recovery size"
		return true
	}
	reader := bufio.NewReaderSize(f, 128*1024)
	magic := make([]byte, len(ledgerMagic053))
	if _, e = io.ReadFull(reader, magic); e != nil || string(magic) != ledgerMagic053 {
		n.ledgerError = "Discovery journal header is invalid; original file preserved"
		return true
	}
	offset, lastCommit := int64(len(magic)), int64(len(magic))
	var generation uint64
	var pending []bitcoinAddress
	active := false
	truncated := false
	for {
		var h [8]byte
		got, err := io.ReadFull(reader, h[:])
		if err == io.EOF && got == 0 {
			if active {
				truncated = true
			}
			break
		}
		if err != nil {
			truncated = true
			break
		}
		length := binary.LittleEndian.Uint32(h[:4])
		sum := binary.LittleEndian.Uint32(h[4:])
		if length == 0 || length > ledgerMaxFrame053 {
			n.ledgerError = "Invalid discovery journal frame size; original file preserved"
			return true
		}
		body := make([]byte, length)
		if _, err = io.ReadFull(reader, body); err != nil {
			truncated = true
			break
		}
		offset += 8 + int64(length)
		if crc32.ChecksumIEEE(body) != sum {
			n.ledgerError = "Discovery journal checksum mismatch; original file preserved"
			return true
		}
		var frame ledgerFrame053
		if json.Unmarshal(body, &frame) != nil {
			n.ledgerError = "Invalid discovery journal JSON; original file preserved"
			return true
		}
		switch frame.Kind {
		case "begin":
			if active || frame.Commit <= n.ledgerCommitted {
				n.ledgerError = "Invalid discovery transaction sequence"
				return true
			}
			generation = frame.Commit
			active = true
			pending = nil
		case "rows":
			if !active || frame.Commit != generation || len(frame.Rows) > 256 || len(pending)+len(frame.Rows) > maxLedgerRecords {
				n.ledgerError = "Invalid discovery row transaction"
				return true
			}
			pending = append(pending, frame.Rows...)
		case "commit":
			if !active || frame.Commit != generation || frame.Meta == nil || frame.Discovery == nil || frame.Meta.Version != 1 || frame.Meta.MaxRecords < 1 || frame.Meta.MaxRecords > maxLedgerRecords || frame.Meta.MaxBytes < 8192 || frame.Meta.MaxBytes > 1024*1024*1024 {
				n.ledgerError = "Invalid discovery commit metadata"
				return true
			}
			for _, row := range pending {
				if row.Address == "" || row.Ordinal == 0 || len(row.Address) > 1100 || len(row.Suppliers) > 4 || len(row.Source) > 320 {
					n.ledgerError = "Invalid discovery record"
					return true
				}
				x := row
				if old := n.ledger[row.Address]; old != nil {
					n.ledgerCompactBytes -= old.serializedBytes
				}
				b, _ := json.Marshal(&x)
				x.serializedBytes = int64(len(b) + 2)
				n.ledgerCompactBytes += x.serializedBytes
				n.ledger[row.Address] = &x
				if len(n.ledger) > maxLedgerRecords {
					n.ledgerError = "Discovery record count exceeds recovery limit"
					return true
				}
				if x.Ordinal > n.ledgerNextOrdinal {
					n.ledgerNextOrdinal = x.Ordinal
				}
			}
			n.ledgerMeta = *frame.Meta
			n.discovery = *frame.Discovery
			n.workCursor = frame.WorkCursor
			n.roleTurns = frame.RoleTurns
			n.roleCursor = frame.RoleCursor % 3
			n.ledgerCommitted = frame.Commit
			lastCommit = offset
			active = false
			pending = nil
		default:
			n.ledgerError = "Unknown discovery journal frame"
			return true
		}
	}
	if n.ledgerCommitted == 0 {
		n.ledgerError = "No committed discovery checkpoint; original file preserved"
		return true
	}
	if truncated {
		f.Close()
		if e = os.Truncate(n.ledgerPath(), lastCommit); e != nil {
			n.ledgerError = "Could not recover incomplete discovery tail: " + e.Error()
			return true
		}
		n.ledgerWarning = "Recovered last committed discovery transaction; incomplete trailing write discarded. Uncommitted observations may be absent."
	}
	n.ledgerBytes = lastCommit
	n.ledgerOrder = make([]string, 0, len(n.ledger))
	for addr := range n.ledger {
		n.ledgerOrder = append(n.ledgerOrder, addr)
	}
	sort.Slice(n.ledgerOrder, func(i, j int) bool { return n.ledger[n.ledgerOrder[i]].Ordinal < n.ledger[n.ledgerOrder[j]].Ordinal })
	// Ordinals are compact monotonic indexes assigned only on new identity.
	for i, addr := range n.ledgerOrder {
		if n.ledger[addr].Ordinal != uint64(i+1) {
			n.ledgerError = "Non-contiguous discovery identity order"
			return true
		}
	}
	n.ledgerLimited = n.ledgerMeta.IntakeLimited || n.ledgerCompactBytes+ledgerReserve053(len(n.ledger)) > n.ledgerMeta.MaxBytes
	return true
}

func (n *bitcoinNetwork) importLegacyLedger() {
	path := filepath.Join(n.app.dataDir, "bitcoin-peers.json")
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return
	}
	if e != nil {
		n.ledgerError = "Legacy address import: " + e.Error()
		return
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if e != nil || len(b) > 8*1024*1024 {
		n.ledgerError = "Legacy address book exceeds import bound"
		return
	}
	var rows []bitcoinAddress
	if json.Unmarshal(b, &rows) != nil {
		n.ledgerError = "Legacy address book could not be decoded; not replaced"
		return
	}
	for _, row := range rows {
		if len(n.ledger) >= maxBitcoinAddresses {
			break
		}
		addr, key, _, e := canonicalDiscoveryEndpoint(advertisedPeer{Addr: row.Address})
		if e != nil {
			continue
		}
		if n.ledger[addr] != nil {
			continue
		}
		host, _, _ := net.SplitHostPort(addr)
		explicit := strings.HasPrefix(row.Source, "manual") || row.Source == "local_core_bitcoin" || row.Source == "lan_udp"
		if !explicit && !publicBitcoinIP(net.ParseIP(host)) {
			row.Exclusion = "private_or_disallowed_gossip"
		}
		row.Address = addr
		row.Key = key
		row.Imported = true
		n.ledgerNextOrdinal++
		row.Ordinal = n.ledgerNextOrdinal
		row.EverHint = row.Services&gatewayExperimentalServiceBit != 0
		row.GossipServices = row.Services
		if !row.LastAttempt.IsZero() {
			row.Attempts = 1
			row.FirstAttempt = row.LastAttempt
			row.LastOutcome = "imported_attempt_count_lower_bound"
		}
		if len(row.Suppliers) > 4 {
			row.Suppliers = row.Suppliers[:4]
		}
		row.Observations = 0 // Old counters cannot reconstruct observations.
		if row.FirstSeen.IsZero() {
			row.FirstSeen = n.now()
		}
		x := row
		n.ledger[addr] = &x
		n.ledgerOrder = append(n.ledgerOrder, addr)
		n.ledgerMeta.Imported++
		n.touchLocked(&x)
	}
	n.ledgerWarning = "Imported the retained v0.5.2 address records. Previously evicted endpoints and exact old attempt totals are unavailable."
}

// Used by tests to append an incomplete transaction without touching any real
// peer. Encoding remains the same in tests and production.
func ledgerFrameBytes053(f ledgerFrame053) []byte {
	var b bytes.Buffer
	_, _ = writeLedgerFrame053(&b, f)
	return b.Bytes()
}
