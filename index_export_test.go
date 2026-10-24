package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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
