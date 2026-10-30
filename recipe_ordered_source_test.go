package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func recipeVerifiedGenesis(t *testing.T) blockView {
	t.Helper()
	header, e := hex.DecodeString(genesisHeaderHex)
	if e != nil {
		t.Fatal(e)
	}
	b, e := parseBlockDetailed(0, genesisHashDisplay, header, testGenesisBlockPayload(t), "fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestRecipeVerifiedSourceGenesisPagesAndReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	block := recipeVerifiedGenesis(t)
	// The public decoded view is mutable. Source reconstruction must use the
	// independently verified raw bytes, not this modified output value.
	block.Transactions[0].Outputs[0].ValueSats = 1
	source, err := recipeVerifiedSourceBuild(ctx, root, block)
	if err != nil {
		t.Fatal(err)
	}
	if !source.receipt.Complete || source.receipt.EventCount != 4 || source.receipt.PageCount != 1 {
		t.Fatal(source.receipt)
	}
	reopened, err := recipeVerifiedSourceOpen(ctx, root, source.id)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reopened.page(ctx, 0, 2)
	if err != nil || len(first.Events) != 2 || first.Next == nil || *first.Next != 2 {
		t.Fatal(first, err)
	}
	if first.Events[0]["kind"].Text != "tx_begin" || first.Events[1]["kind"].Text != "outputs_begin" {
		t.Fatal(first.Events)
	}
	second, err := reopened.page(ctx, *first.Next, 2)
	if err != nil || len(second.Events) != 2 || second.Next != nil {
		t.Fatal(second, err)
	}
	if second.Events[0]["kind"].Text != "output" || second.Events[0]["value"].Uint != 5_000_000_000 || second.Events[1]["kind"].Text != "tx_end" {
		t.Fatal(second.Events)
	}
	empty, err := reopened.page(ctx, 4, 1)
	if err != nil || len(empty.Events) != 0 || empty.Next != nil {
		t.Fatal(empty, err)
	}
	if _, err = reopened.page(ctx, 5, 1); err == nil {
		t.Fatal("out of range accepted")
	}
	cancel, cancelFn := context.WithCancel(ctx)
	cancelFn()
	if _, err = reopened.page(cancel, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestRecipeVerifiedSourceRejectsTamperingAndIncompleteReceipt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	block := recipeVerifiedGenesis(t)
	source, err := recipeVerifiedSourceBuild(ctx, root, block)
	if err != nil {
		t.Fatal(err)
	}
	tree := recipeVerifiedTree(ctx, source.dir, source.receipt.IndexRoot)
	var hash string
	if ok, e := tree.get(recipeVerifiedIndexKey(0), &hash); e != nil || !ok {
		t.Fatal(ok, e)
	}
	path := filepath.Join(source.dir, "event-pages", hash+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = source.page(ctx, 0, 1); err == nil {
		t.Fatal("modified page accepted")
	}
	if _, err = recipeVerifiedSourceOpen(ctx, root, source.id); err == nil {
		t.Fatal("modified inventory accepted on reopen")
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	forged := source.receipt
	forged.EventCount--
	forgedBytes, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	forgedID := recipeStoreHash(forgedBytes)
	if err = recipeStorePutImmutable(filepath.Join(source.dir, "receipts"), forgedID, forgedBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = recipeVerifiedSourceOpen(ctx, root, forgedID); err == nil {
		t.Fatal("forged incomplete event inventory accepted")
	}
	rawPath := filepath.Join(source.dir, "raw", source.receipt.RawHash+".json")
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if err = os.WriteFile(rawPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = recipeVerifiedSourceOpen(ctx, root, source.id); err == nil {
		t.Fatal("modified verified bytes accepted")
	}
}
func TestRecipeVerifiedWalkMatchesOrderedTransactions(t *testing.T) {
	genesis := utxoFixtureBlock(0, "", utxoFixtureCoinbase(68000, 50))
	cb := utxoFixtureCoinbase(68001, 10, 20)
	spend := utxoFixtureSpend(68002, []inputView{{PrevTxID: cb.TxID, PrevVout: 1}}, 19)
	block := utxoFixtureBlock(1, genesis.Hash, cb, spend)
	expected, err := recipeOrderedBitcoinEvents(context.Background(), block)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]recipeOrderedValue
	err = recipeVerifiedWalk(context.Background(), block, func(e map[string]recipeOrderedValue) error { got = append(got, e); return nil })
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("event ordering differs: %v", err)
	}
	kinds := []string{}
	for _, e := range got {
		kinds = append(kinds, e["kind"].Text)
	}
	want := []string{"tx_begin", "outputs_begin", "output", "output", "tx_end", "tx_begin", "input", "outputs_begin", "output", "tx_end"}
	if !reflect.DeepEqual(kinds, want) || got[6]["prev_txid"].Text != cb.TxID || got[6]["prev_vout"].Uint != 1 {
		t.Fatal(kinds, got[6])
	}
	_, err = json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecipeVerifiedSourceFeedsResumableUTXO(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	block := recipeVerifiedGenesis(t)
	built, err := recipeVerifiedSourceBuild(ctx, root, block)
	if err != nil {
		t.Fatal(err)
	}
	source, err := recipeVerifiedSourceOpen(ctx, root, built.id)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := recipeResumeVerifiedSource(source)
	if err != nil {
		t.Fatal(err)
	}
	program := recipeOrderedUTXOExample()
	id := durableTestID()
	id.Genesis = genesisHashDisplay
	id.Semantic = recipeResumeProgramID(program)
	stage, err := recipeStageOpen(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.create(); err != nil {
		t.Fatal(err)
	}
	work, err := stage.begin(ctx, 0, genesisHashDisplay, "", source.id, source.receipt.EventCount)
	if err != nil {
		t.Fatal(err)
	}
	work, done, err := recipeResumeSlice(ctx, stage, work.ID, work.Sequence, program, binding, 1)
	if err != nil || done {
		t.Fatal(work, done, err)
	}
	var pageHash string
	if found, e := recipeVerifiedTree(ctx, source.dir, source.receipt.IndexRoot).get(recipeVerifiedIndexKey(0), &pageHash); e != nil || !found {
		t.Fatal(found, e)
	}
	pagePath := filepath.Join(source.dir, "event-pages", pageHash+".json")
	originalPage, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(pagePath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = recipeResumeSlice(ctx, stage, work.ID, work.Sequence, program, binding, 1); err == nil {
		t.Fatal("corrupted event page advanced continuation")
	}
	after, err := stage.checkpoint(work.ID)
	if err != nil || after.Sequence != work.Sequence || after.Cursor != work.Cursor {
		t.Fatal("failed source proof advanced stage", after, err)
	}
	if err = os.WriteFile(pagePath, originalPage, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; !done; i++ {
		if i > 50 {
			t.Fatal("verified source did not finish")
		}
		stageCopy := *stage
		stage = &stageCopy
		work, done, err = recipeResumeSlice(ctx, stage, work.ID, work.Sequence, program, binding, 10)
		if err != nil {
			t.Fatal(err)
		}
	}
	if work.Cursor != source.receipt.EventCount {
		t.Fatal("wrong authenticated cursor", work.Cursor)
	}
	stageTestComplete(t, stage, work)
	key, err := recipeBitcoinOutpoint(block.Transactions[0].TxID, 0)
	if err != nil {
		t.Fatal(err)
	}
	value, err := stage.read(ctx, recipeStateKey{Table: "coins", Key: key})
	if err != nil || value.Present {
		t.Fatal("genesis output retained", value, err)
	}
}

// Bitcoin block 170 (490 raw bytes), fetched from the mempool.space raw block API.
// SHA-256: 32b4d091e14125788d35a2dfd2ed559994262e8f34c0edfdb510f605900c661e.
const recipeVerifiedBlock170Hex = "0100000055bd840a78798ad0da853f68974f3d183e2bd1db6a842c1feecf222a00000000ff104ccb05421ab93e63f8c3ce5c2c2e9dbb37de2764b3a3175c8166562cac7d51b96a49ffff001d283e9e700201000000010000000000000000000000000000000000000000000000000000000000000000ffffffff0704ffff001d0102ffffffff0100f2052a01000000434104d46c4968bde02899d2aa0963367c7a6ce34eec332b32e42e5f3407e052d64ac625da6f0718e7b302140434bd725706957c092db53805b821a85b23a7ac61725bac000000000100000001c997a5e56e104102fa209c6a852dd90660a20b2d9c352423edce25857fcd3704000000004847304402204e45e16932b8af514961a1d3a1a25fdf3f4f7732e9d624c6c61548ab5fb8cd410220181522ec8eca07de4860a4acdd12909d831cc56cbbac4622082221a8768d1d0901ffffffff0200ca9a3b00000000434104ae1a62fe09c5f51b13905f07f06b99a2f7159b2225f374cd378d71302fa28414e7aab37397f554a7df5f142c21c1b7303b8a0626f1baded5c72a704f7e6cd84cac00286bee0000000043410411db93e1dcdb8a016b49840f8c53bc1eb68a382e97b1482ecad7b148a6909a5cb2e0eaddfb84ccf9744464f82e160bfa9b8b64f9d4c03f999b8643f656b412a3ac00000000"

func TestRecipeVerifiedSourceRealTransactionOrder(t *testing.T) {
	ctx := context.Background()
	raw, err := hex.DecodeString(recipeVerifiedBlock170Hex)
	if err != nil {
		t.Fatal(err)
	}
	const hash = "00000000d1145790a8694403d4063f323d499e655c83426834d4ce2f8dd4a2ee"
	block, err := parseBlockDetailed(170, hash, nil, raw, "verified fixture", false)
	if err != nil || !integrityVerified(block) {
		t.Fatal("real block verification", err)
	}
	root := t.TempDir()
	source, err := recipeVerifiedSourceBuild(ctx, root, block)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := recipeVerifiedSourceOpen(ctx, root, source.id)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reopened.page(ctx, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, event := range page.Events {
		kinds = append(kinds, event["kind"].Text)
	}
	want := []string{"tx_begin", "outputs_begin", "output", "tx_end", "tx_begin", "input", "outputs_begin", "output", "output", "tx_end"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatal(kinds)
	}
	if page.Events[4]["txid"].Text != "f4184fc596403b9d638783cf57adfe4c75c605f6356fbc91338530e9831e9e16" || page.Events[5]["prev_vout"].Uint != 0 || page.Events[7]["value"].Uint != 1_000_000_000 || page.Events[8]["value"].Uint != 4_000_000_000 {
		t.Fatal("verified transaction facts differ", page.Events)
	}
}

func TestRecipeVerifiedSourcePageBoundary(t *testing.T) {
	ctx := context.Background()
	dir, err := recipeVerifiedSourceDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := recipeVerifiedTree(ctx, dir, "")
	for number := uint64(0); number < 2; number++ {
		count := recipeVerifiedPageEvents
		if number == 1 {
			count = 6
		}
		page := recipeVerifiedEventPage{Schema: 1, Number: number, Start: number * recipeVerifiedPageEvents, Events: make([]json.RawMessage, 0, count)}
		for i := 0; i < count; i++ {
			data, e := json.Marshal(map[string]recipeOrderedValue{"n": rovUint(page.Start + uint64(i))})
			if e != nil {
				t.Fatal(e)
			}
			page.Events = append(page.Events, data)
		}
		data, e := json.Marshal(page)
		if e != nil {
			t.Fatal(e)
		}
		hash := recipeStoreHash(data)
		if e = recipeStorePutImmutable(filepath.Join(dir, "event-pages"), hash, data); e != nil {
			t.Fatal(e)
		}
		if e = tree.put(recipeVerifiedIndexKey(number), hash); e != nil {
			t.Fatal(e)
		}
	}
	index, err := tree.flush()
	if err != nil {
		t.Fatal(err)
	}
	source := &recipeVerifiedSource{dir: dir, id: recipeTestHash("a"), receipt: recipeVerifiedSourceReceipt{IndexRoot: index, EventCount: 70, PageCount: 2}}
	first, err := source.page(ctx, 62, 8)
	if err != nil || len(first.Events) != 2 || first.Events[0]["n"].Uint != 62 || first.Next == nil || *first.Next != 64 {
		t.Fatal(first, err)
	}
	next, err := source.page(ctx, *first.Next, 8)
	if err != nil || len(next.Events) != 6 || next.Events[5]["n"].Uint != 69 || next.Next != nil {
		t.Fatal(next, err)
	}
}
