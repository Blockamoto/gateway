package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func recipeSpoolTestProgram(t *testing.T, policy string) *recipeProgram {
	t.Helper()
	d := locatorRecipeProgram(t).definition
	for i := range d.Tables {
		d.Tables[i].OnConflict = policy
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func recipeSpoolTestRow(p *recipeProgram, key string, index int) recipeRowProposal {
	return recipeRowProposal{Table: p.definition.Tables[0].Name, Key: key, Value: map[string]any{"txid": key, "ref": txCoordinate(index, 0), "block_hash": genesisHashDisplay, "height": json.Number("0"), "tx_index": json.Number(strconv.Itoa(index))}}
}
func recipeSpoolTestHeader(p *recipeProgram) recipeStoreUnit {
	return recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}
}

func TestRecipeSparseSpoolCanonicalBytesWithMemoryAndScratchPages(t *testing.T) {
	// Synthetic physical-row fixture; independent source/admission authority
	// is required before any production head can publish such staged bytes.
	ctx := context.Background()
	for _, spill := range []bool{false, true} {
		t.Run(fmt.Sprint(spill), func(t *testing.T) {
			p := recipeSpoolTestProgram(t, "")
			base := t.TempDir()
			if err := os.WriteFile(filepath.Join(base, "keep"), []byte("outside staging"), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := newRecipeSparseSpool(ctx, base, p)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if spill {
				s.memoryRows, s.flushRows, s.sorter.batchRows, s.sorter.fanIn = 2, 3, 3, 2
			}
			header := recipeSpoolTestHeader(p)
			legacy := header
			for i := 36; i >= 0; i-- {
				row := recipeSpoolTestRow(p, testID(uint64(81000+i)), i)
				if err := s.Add(row); err != nil {
					t.Fatal(err)
				}
				key, _ := json.Marshal([]string{row.Table, row.Key})
				value, _ := json.Marshal(row.Value)
				legacy.Rows = append(legacy.Rows, recipeStoreRow{Key: string(key), Value: value})
			}
			sort.Slice(legacy.Rows, func(i, j int) bool { return legacy.Rows[i].Key < legacy.Rows[j].Key })
			want, _ := json.Marshal(legacy)
			utxoWriterMu.Lock()
			utxoStateMu.Lock()
			staged, err := s.Seal(header)
			utxoStateMu.Unlock()
			utxoWriterMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(staged.Path)
			if err != nil || !bytes.Equal(got, want) || staged.Digest != recipeStoreHash(want) || staged.Bytes != int64(len(want)) || staged.Rows != 37 {
				t.Fatal("streamed unit differs from canonical legacy bytes", staged, err)
			}
			if spill && (s.tree == nil || !s.tree.temporary) {
				t.Fatal("larger key membership did not use isolated native scratch pages")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if keep, err := os.ReadFile(filepath.Join(base, "keep")); err != nil || string(keep) != "outside staging" {
				t.Fatal("cleanup changed unowned files", err)
			}
			if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
				t.Fatal("spool cleanup leaked staged data", err)
			}
		})
	}
}

func TestRecipeSparseSpoolIdenticalRowsAreExactAndOwned(t *testing.T) {
	ctx := context.Background()
	p := recipeSpoolTestProgram(t, "identical")
	s, err := newRecipeSparseSpool(ctx, t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.memoryRows = 1
	row := recipeSpoolTestRow(p, "same", 0)
	if err := s.Add(row); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(row); err != nil {
		t.Fatal("identical repeated row rejected", err)
	}
	if err := s.Add(recipeSpoolTestRow(p, "other", 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(row); err != nil {
		t.Fatal("scratch lookup changed identical semantics", err)
	}
	if s.count != 2 {
		t.Fatal("duplicates increased durable row count", s.count)
	}
	row.Value["ref"] = "mutated after staging"
	if err := s.Add(row); !errors.Is(err, errRecipeSparseRowConflict) {
		t.Fatal("changed value matched an owned prior row", err)
	}
	if _, err := s.Seal(recipeSpoolTestHeader(p)); err == nil {
		t.Fatal("failed spool could seal partial proposals")
	}
}

func TestRecipeSparseSpoolRejectsChangedBytesKeysAndCancellation(t *testing.T) {
	for _, mode := range []string{"body", "long-key", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := recipeSpoolTestProgram(t, "")
			s, err := newRecipeSparseSpool(ctx, t.TempDir(), p)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			key := strings.Repeat("q", 9000) + "suffix"
			if err := s.Add(recipeSpoolTestRow(p, key, 0)); err != nil {
				t.Fatal(err)
			}
			if err := s.keyWriter.Flush(); err != nil {
				t.Fatal(err)
			}
			if err := s.rowWriter.Flush(); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "body":
				if _, err := s.rows.WriteAt([]byte("Z"), s.rowBytes-5); err != nil {
					t.Fatal(err)
				}
			case "long-key":
				if _, err := s.keys.WriteAt([]byte("Z"), 100); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			if _, err := s.Seal(recipeSpoolTestHeader(p)); err == nil {
				t.Fatal("changed/canceled stage sealed", mode)
			}
		})
	}
}

func TestRecipeSparseSpoolRealLargeUnitPreservesFrozenDigest(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	d := locatorRecipeProgram(t).definition
	d.ID, d.Name, d.Origin = "large-output-audit", "Large output audit", block.Height
	annotation, _ := json.Marshal(strings.Repeat("a", 16<<10))
	d.Steps[len(d.Steps)-1].Row["annotation"] = recipeExpr{Literal: annotation}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	source, err := recipeTransactionSourceFromBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	s, err := newRecipeSparseSpool(ctx, t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := p.evaluateTransactionSourceInto(ctx, block, source, nil, s.Add, s); err != nil {
		t.Fatal(err)
	}
	header := recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: block.Height, BlockHash: block.Hash, Rows: []recipeStoreRow{}}
	staged, err := s.Seal(header)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Bytes != 52463224 || staged.Rows != 3139 || staged.Digest != "60d405ebf28a790218357d8196666a5d53587d539736a3cbb59028519ddce862" {
		t.Fatal("large streamed unit changed frozen legacy bytes", staged)
	}
	file, err := os.Open(staged.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := ioCopyRecipeSpoolTest(ctx, sum, file); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sum.Sum(nil)) != staged.Digest {
		t.Fatal("sealed file bytes differ from its recorded digest")
	}
	store := recipeTestOpen(t, t.TempDir(), d.ID, p.semanticHash())
	if err := store.publishSparseStaged(ctx, staged, 0, recipeTestGuard, nil); err != nil {
		t.Fatal(err)
	}
	ref := recipeStoreRef{BlockHash: block.Hash, Unit: staged.Digest}
	view, err := store.openSparseView(ctx, ref, block.Height)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if view.info.Rows != 3139 {
		t.Fatal("reopened large unit lost rows", view.info.Rows)
	}
	row, err := view.Row(3138, recipeOutputMaxResponseBytes)
	if err != nil || row.Key == "" {
		t.Fatal("large unit last page unavailable", err)
	}
}

func ioCopyRecipeSpoolTest(ctx context.Context, w io.Writer, r io.Reader) (int64, error) {
	return io.CopyBuffer(recipeJSONContextWriter{ctx: ctx, w: w}, r, make([]byte, 64<<10))
}
