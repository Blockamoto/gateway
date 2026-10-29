package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestIndexExportMissingDecisionRemainsStableWithinReviewAndReplay(t *testing.T) {
	blockHash := indexDigest("missing decision selected block")
	budget := 1024
	s := &indexExportSession{ctx: context.Background(), q: indexExportRequest{Representation: "conventional"}, anchors: map[int64]string{}, hashAt: func(int64) (string, error) { return blockHash, nil }, rawHeight: -1, recordMissing: true, missingBudget: &budget, missingDecisions: map[string]indexExportMissing{}}
	s.rawRead = func(context.Context, int64, string) (*blockView, error) { return nil, nil }
	if id, err := s.identity("1i0.42", blockHash); err != nil || id != "" {
		t.Fatal(id, err)
	}
	s.rawRead = func(context.Context, int64, string) (*blockView, error) {
		t.Fatal("review revisited resolved source for pinned missing reference")
		return nil, nil
	}
	if id, err := s.identity("1i0.42", blockHash); err != nil || id != "" || s.meta.MissingCount != 2 {
		t.Fatal(id, err, s.meta.MissingCount)
	}
	s.recordMissing, s.replayMissing = false, true
	if id, err := s.identity("1i0.42", blockHash); err != nil || id != "" || s.meta.MissingCount != 3 {
		t.Fatal(id, err, s.meta.MissingCount)
	}
	s.recordMissing, s.replayMissing = true, false
	s.resolvedDecisions = map[string]string{"2i0.42": indexDigest("previous verified tx") + "i0"}
	if err := s.missing("2i0.42", blockHash, "missing"); !errors.Is(err, errIndexExportSnapshotChanged) {
		t.Fatal("verified identity disappeared during review", err)
	}
}

func TestIndexExportDecisionBudgetsAtPopulatedScale(t *testing.T) {
	const count = 458000
	retained, transient := indexExportPinMemoryLimit, indexExportResolvedReviewMemoryLimit
	s := &indexExportSession{recordMissing: true, missingDecisions: map[string]indexExportMissing{}, resolvedDecisions: map[string]string{}, missingBudget: &retained, resolvedBudget: &transient}
	hash := indexDigest("synthetic selected block")
	identity := indexDigest("synthetic verified transaction") + "i0"
	for i := 0; i < count; i++ {
		if err := s.recordResolvedIdentity(fmt.Sprintf("1i0.%d", i), identity); err != nil {
			t.Fatal("resolved decision limit below populated scale", i, err)
		}
		if err := s.missing(fmt.Sprintf("2i0.%d", i), hash, "unavailable"); err != nil {
			t.Fatal("unresolved decision limit below populated scale", i, err)
		}
	}
	if indexExportResolvedReviewMemoryLimit-transient <= 32<<20 || indexExportPinMemoryLimit-retained <= 32<<20 || len(s.resolvedDecisions) != count || len(s.missingDecisions) != count {
		t.Fatal("did not exercise separate populated-size budgets", transient, retained)
	}
	retained = 0
	if err := s.missing("2i0.9999999", hash, "unavailable"); err == nil {
		t.Fatal("retained budget did not fail closed")
	}
	transient = 0
	if err := s.recordResolvedIdentity("1i0.9999999", identity); err == nil {
		t.Fatal("transient budget did not fail closed")
	}
}

func TestIndexExportOutcomeReaderDiagnostic(t *testing.T) {
	if os.Getenv("GATEWAY_EXPORT_READER_DIAGNOSTIC") == "" {
		t.Skip("owned synthetic reader diagnostic")
	}
	dir := filepath.Join(t.TempDir(), "pages")
	tree := newOrdinalTree(dir, "")
	for i := 0; i < 64; i++ {
		if err := tree.put(fmt.Sprintf("%016x", i), bitmapDevelopmentBlock{Height: int64(i), BlockEvaluated: true}); err != nil {
			t.Fatal(err)
		}
	}
	root, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	read := func(reuse bool) time.Duration {
		start := time.Now()
		reader := newOrdinalTree(dir, root)
		for i := 0; i < 1000; i++ {
			if !reuse {
				reader = newOrdinalTree(dir, root)
			}
			var block bitmapDevelopmentBlock
			ok, err := reader.get(fmt.Sprintf("%016x", i%64), &block)
			if err != nil || !ok || block.Height != int64(i%64) {
				t.Fatal(i, ok, block, err)
			}
		}
		return time.Since(start)
	}
	fresh, reused := read(false), read(true)
	t.Logf("1000 synthetic outcome lookups: fresh reader=%s, reused bounded reader=%s; equal results; not a mainnet throughput claim", fresh, reused)
}

// This checked-in Bitcoin block is the authentic Bitmap-origin reveal block.
// Writers and the decoder produce all evidence; no ID lookup is injected.
func TestIndexExportAuthenticRetainedRevealOnlyTranslation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		t.Fatal(err)
	}
	hash := hash256(raw[:80])
	block, err := parseBlockDetailed(792435, reverseHex(hash[:]), raw[:80], raw, "export verified fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	occurrences, err := extractInscriptionOccurrences(block)
	if err != nil || len(occurrences.Occurrences) == 0 {
		t.Fatal("authentic fixture lacks reveals", err)
	}
	known := occurrences.Occurrences[0]
	for _, provider := range []string{"raw", "archive", "cache"} {
		t.Run(provider, func(t *testing.T) {
			a := exportTestApp(t)
			a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
			file, e := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, e = file.WriteAt(raw[:80], block.Height*80)
			_ = file.Close()
			if e != nil {
				t.Fatal(e)
			}
			a.status.HeaderHeight, a.status.HeaderCount = block.Height, block.Height+1
			source, e := openIndexStore(a.dataDir, "inscriptions")
			if e != nil {
				t.Fatal(e)
			}
			source.head.Mode = "lean"
			if e = source.appendBlock(block, block.Height, "ephemeral"); e != nil {
				t.Fatal(e)
			}
			switch provider {
			case "raw":
				retained, e := openIndexStore(a.dataDir, "blocks")
				if e != nil {
					t.Fatal(e)
				}
				if e = retained.appendBlock(block, block.Height, "ephemeral"); e != nil {
					t.Fatal(e)
				}
			case "archive":
				public := block
				public.CacheVisibility = "public"
				if e = a.archivePut(raw, public); e != nil {
					t.Fatal(e)
				}
			case "cache":
				prefix := strconv.FormatInt(block.Height, 10) + "-" + block.Hash
				if e = atomicWriteBytes(filepath.Join(a.dataDir, "blocks", "raw", prefix+".block"), raw); e != nil {
					t.Fatal(e)
				}
				a.cacheIndex.Blocks[block.Hash] = cachedBlockEntry{Height: block.Height, Hash: block.Hash, Prefix: prefix}
			}
			a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
				t.Fatal("export fetched a block")
				return blockView{}, fmt.Errorf("unexpected acquisition")
			}
			before := exportTestFiles(t, a.dataDir)
			session := &indexExportSession{a: a, ctx: context.Background(), q: indexExportRequest{Index: "inscriptions", Representation: "conventional"}, anchors: map[int64]string{}, rawHeight: -1}
			defer session.close()
			if e = session.capture(); e != nil {
				t.Fatal(e)
			}
			id, e := session.identity(inscriptionCoordinate(uint32(known.TxIndex), known.Index, known.BlockHeight), known.BlockHash)
			if e != nil || id != known.ID || session.meta.MissingCount != 0 {
				t.Fatal(provider, id, known.ID, e)
			}
			// Reusing the same reveal position retains just this verified decoded block.
			if _, e = session.identity(inscriptionCoordinate(uint32(known.TxIndex), known.Index, known.BlockHeight), known.BlockHash); e != nil {
				t.Fatal(e)
			}
			exportTestUnchanged(t, a, before)
			if provider == "archive" {
				path, _, e := a.archivePaths(block.Hash)
				if e != nil {
					t.Fatal(e)
				}
				corrupt := append([]byte(nil), raw...)
				corrupt[len(corrupt)-1] ^= 1
				if e = os.WriteFile(path, corrupt, 0600); e != nil {
					t.Fatal(e)
				}
				if _, e = session.readRaw(context.Background(), block.Height, block.Hash); e == nil {
					t.Fatal("corrupt archived reveal bytes were accepted")
				}
			}
			if provider == "cache" {
				entry := a.cacheIndex.Blocks[block.Hash]
				corrupt := append([]byte(nil), raw...)
				corrupt[len(corrupt)-1] ^= 1
				if e = os.WriteFile(filepath.Join(a.dataDir, "blocks", "raw", entry.Prefix+".block"), corrupt, 0600); e != nil {
					t.Fatal(e)
				}
				if _, e = session.readRaw(context.Background(), block.Height, block.Hash); e == nil {
					t.Fatal("unverified cached reveal bytes were accepted")
				}
			}
		})
	}
}

func TestIndexExportPartialDistrictPinIgnoresLaterRaw(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		t.Fatal(err)
	}
	hash := hash256(raw[:80])
	block, err := parseBlockDetailed(bitmapDevelopmentStart, reverseHex(hash[:]), raw[:80], raw, "export verified fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	a := exportTestApp(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(raw[:80], block.Height*80); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	a.status.HeaderHeight, a.status.HeaderCount = block.Height, block.Height+1
	district := bitmapTestConsumer(t, a.dataDir, bitmapStandardDistrictID)
	if err = district.Apply(context.Background(), block, nil); err != nil {
		t.Fatal(err)
	}
	if district.head == nil || district.head.WinnerCount == 0 {
		t.Fatal("fixture has no District winner")
	}
	q := indexExportRequest{Index: bitmapStandardDistrictID, Representation: "conventional"}
	meta, err := a.indexExportReview(context.Background(), q)
	if err != nil || !meta.Ready || !meta.Partial || meta.Complete || meta.OmittedWinners == 0 {
		t.Fatal(meta, err)
	}
	q.Snapshot = meta.Snapshot
	pin, err := findIndexExportPin(a, q)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	if err = retained.appendBlock(block, block.Height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(q)
	w := httptest.NewRecorder()
	a.handleIndexExportDownload(w, indexExportHTTPTestRequest("POST", "download", string(body)))
	if w.Code != 200 || w.Header().Get("X-Gateway-Export-Complete") != "false" || w.Header().Get("X-Gateway-Export-Partial") != "true" || w.Body.Len() != int(pin.size) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	digest := sha256.Sum256(w.Body.Bytes())
	if hex.EncodeToString(digest[:]) != pin.digest {
		t.Fatal("new Raw evidence changed reviewed partial bytes")
	}
	q.Snapshot = ""
	fresh, err := a.indexExportReview(context.Background(), q)
	if err != nil || !fresh.Complete || fresh.Partial {
		t.Fatal("new review did not use retained Raw", fresh, err)
	}
}

func TestIndexExportMembershipChunksPreserveEveryAttestation(t *testing.T) {
	const count = 20001
	session := &indexExportSession{ctx: context.Background()}
	view := &indexExportBrowse{s: &indexStore{definition: indexDefinition{ID: "inscriptions"}}, origin: "viewed_block", blocks: map[string]string{}}
	for i := 0; i < count; i++ {
		view.blocks[strconv.Itoa(i*2)] = indexDigest(i)
		session.meta.Coverage = append(session.meta.Coverage, heightInterval{int64(i * 2), int64(i * 2)})
	}
	session.memberships = []*indexExportBrowse{view}
	var body bytes.Buffer
	encoder := &indexExportEncoder{w: &body, digest: sha256.New()}
	if err := session.membershipLines(encoder); err != nil {
		t.Fatal(err)
	}
	members, intervals := 0, 0
	for _, line := range bytes.Split(bytes.TrimSpace(body.Bytes()), []byte{'\n'}) {
		if len(line) > 100000 {
			t.Fatal("membership was accumulated into an unbounded row", len(line))
		}
		var record struct {
			Type    string `json:"type"`
			Members []struct {
				Height     int64  `json:"height"`
				Commitment string `json:"commitment"`
			} `json:"members"`
			Intervals []heightInterval `json:"intervals"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if len(record.Members) > 256 || len(record.Intervals) > 256 {
			t.Fatal("unbounded chunk")
		}
		for _, member := range record.Members {
			if member.Height != int64(members*2) || member.Commitment != indexDigest(members) {
				t.Fatal("membership order/content changed", members)
			}
			members++
		}
		for _, interval := range record.Intervals {
			if interval != (heightInterval{int64(intervals * 2), int64(intervals * 2)}) {
				t.Fatal("coverage omitted or reordered")
			}
			intervals++
		}
	}
	if members != count || intervals != count {
		t.Fatal("export provenance was truncated", members, intervals)
	}
}

func TestIndexExportCancelledSpillRemovesPrivateFile(t *testing.T) {
	root := indexExportHTTPSpoolDir(t)
	a, _, _ := exportTestInscriptionFixture(t, "lean")
	session := &indexExportSession{a: a, ctx: context.Background(), q: indexExportRequest{Index: "inscriptions", Representation: "conventional"}, anchors: map[int64]string{}, rawHeight: -1}
	if err := session.capture(); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	session.ctx = cancelCtx
	if err := session.buildLookupSpill(session.primary); err != context.Canceled {
		t.Fatal("cancelled spill did not stop", err)
	}
	session.close()
	if files, err := os.ReadDir(root); err != nil || len(files) != 0 {
		t.Fatal("cancelled export leaked private spill", files, err)
	}
}
