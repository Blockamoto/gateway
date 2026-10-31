package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeHeaderAndBlockSourcesOwnExactEvidence(t *testing.T) {
	ctx := context.Background()
	raw := testGenesisBlockPayload(t)
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "source event fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	event, err := recipeBlockSourceEvent(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	if event["kind"].Text != "block" || event["transaction_count"].Uint != 1 || event["time"].Uint != 1231006505 || event["nonce"].Uint != 2083236893 || event["body_sha256"].Text != recipeStoreHash(raw) || !bytes.Equal(event["raw_block"].Bytes, raw) {
		t.Fatal(event)
	}
	// The existing byte receipt rejects altered decoded source metadata.
	changed := block
	changed.TransactionCount = 999
	if _, err = recipeBlockSourceEvent(ctx, changed); err == nil {
		t.Fatal("altered decoded inventory accepted")
	}
	header := append([]byte(nil), raw[:80]...)
	headerEvent, err := recipeHeaderSourceEvent(ctx, 0, genesisHashDisplay, header)
	if err != nil {
		t.Fatal(err)
	}
	header[0] ^= 1
	if bytes.Equal(headerEvent["raw_header"].Bytes, header) {
		t.Fatal("header event borrowed mutable provider bytes")
	}
	for _, input := range []struct {
		height int64
		hash   string
		header []byte
	}{{-1, genesisHashDisplay, raw[:80]}, {0, testID(880111), raw[:80]}, {0, genesisHashDisplay, raw[:79]}} {
		if _, err = recipeHeaderSourceEvent(ctx, input.height, input.hash, input.header); err == nil {
			t.Fatal("invalid header source admitted")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = recipeHeaderSourceEvent(cancelled, 0, genesisHashDisplay, raw[:80]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRecipeHeaderPublicRunUsesOnlySelectedHeaders(t *testing.T) {
	ctx := context.Background()
	a, blocks := transactionRunFixture(t)
	raw, _, err := exactVerifiedBlockPayload(blocks[1])
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt(raw[:80], 80)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(status *appStatus) {
		status.HeaderCount = 2
		status.HeaderHeight = 1
		status.TipHash = blocks[1].Hash
	})
	data, err := os.ReadFile(filepath.Join("recipes", "builtins", "headers.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseRecipe(data)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = "my-header-history", "My header history"
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		return blockView{}, fmt.Errorf("header-only recipe attempted body acquisition")
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 2 || done.Run.Counts.DecodedBlocks != 0 || done.Run.Counts.InscriptionExtractions != 0 {
		t.Fatal(done.Run)
	}
	for height := int64(0); height <= 1; height++ {
		out, _, err := a.readRecipeOutput(ctx, saved.Recipe.ID, saved.Revision, saved.SemanticHash, height, 0, 10, "")
		if err != nil || out["state"] != "covered" {
			t.Fatal(out, err)
		}
		rows := out["rows"].([]recipeStoreRow)
		if len(rows) != 1 || !bytes.Contains(rows[0].Value, []byte(blocks[height].Hash)) {
			t.Fatal(rows)
		}
	}
}

func TestRecipeRawPublicSavedOwnsCompleteBytes(t *testing.T) {
	ctx := context.Background()
	a, blocks := transactionRunFixture(t)
	data, err := os.ReadFile(filepath.Join("recipes", "builtins", "raw-blocks.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseRecipe(data)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = "my-raw-blocks", "My raw blocks"
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return blocks[target.Height], nil
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: saved.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, saved.Recipe.ID, saved.Revision, saved.SemanticHash, 0, 0, 10, "")
	if err != nil || out["state"] != "covered" {
		t.Fatal(out, err)
	}
	rows := out["rows"].([]recipeStoreRow)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	var row map[string]json.RawMessage
	if err = json.Unmarshal(rows[0].Value, &row); err != nil {
		t.Fatal(err)
	}
	raw, _, err := exactVerifiedBlockPayload(blocks[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(row["raw_block"], []byte(hex.EncodeToString(raw))) {
		t.Fatal("saved body differs from verified bytes")
	}
}

func TestRecipeHeaderAndBlockDocumentsUseCommonInterpreter(t *testing.T) {
	ctx := context.Background()
	raw := testGenesisBlockPayload(t)
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "whole source document fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"headers.json", "raw-blocks.json"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("recipes", "builtins", name))
			if err != nil {
				t.Fatal(err)
			}
			d, err := parseRecipe(data)
			if err != nil {
				t.Fatal(err)
			}
			p, err := compileRecipe(d)
			if err != nil {
				t.Fatal(err)
			}
			event, err := recipeHeaderSourceEvent(ctx, 0, genesisHashDisplay, raw[:80])
			if d.Source == recipeBlockSource {
				event, err = recipeBlockSourceEvent(ctx, block)
			}
			if err != nil {
				t.Fatal(err)
			}
			spool, err := newRecipeSparseSpool(ctx, t.TempDir(), p)
			if err != nil {
				t.Fatal(err)
			}
			defer spool.Close()
			if err = p.evaluateOrderedSparseEventsInto(ctx, func(sink func(map[string]recipeOrderedValue) error) error { return sink(event) }, spool.Add); err != nil {
				t.Fatal(err)
			}
			unit, err := spool.Seal(recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: 0, BlockHash: block.Hash})
			if err != nil || unit.Bytes == 0 {
				t.Fatal(unit, err)
			}
			if err = recipeSparseVerifyFile(ctx, unit.Path, unit.Digest, unit.Bytes); err != nil {
				t.Fatal(err)
			}
			// A header-only program cannot silently request an unauthenticated body.
			if d.Source == recipeBlockSource {
				d.Source = recipeHeaderSource
				if _, err = compileRecipe(d); err == nil {
					t.Fatal("header contract exposed raw body fields")
				}
			}
		})
	}
}
