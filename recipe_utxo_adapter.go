package main

// The host owns the native writer locks, source admission and atomic publisher.
// This adapter stages individual keyed writes, never a whole-index derivation.
import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type recipeUTXOAdapter struct {
	store          *utxoStore
	baseCommit     string
	edit           *utxoEdit
	checkpoint     *utxoCheckpoint
	block          blockView
	facts          map[string]outputFactRef
	publish        func() error
	failed, closed bool
	emitted        map[string]bool
	// Logical counters remain independent while a coin write updates physical
	// counters before the composition performs its corresponding counter write.
	counts  map[string]recipeOrderedValue
	origins map[string]utxoBridge
	coins   map[string]utxoMember
}

func recipeUTXOCoinValue(coin utxoCoin) recipeOrderedValue {
	return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{
		"txid": rovText(coin.TxID), "vout": rovUint(uint64(coin.Vout)), "height": rovUint(uint64(coin.Height)), "block": rovText(coin.BlockHash), "tx_index": rovUint(uint64(coin.TxIndex)), "coinbase": rovBool(coin.Coinbase), "value": rovUint(coin.ValueSats),
		"facts": {Kind: "object", Object: map[string]recipeOrderedValue{"page": rovText(coin.FactRef.Page), "row": rovUint(uint64(coin.FactRef.Row))}},
	}}
}

func recipeUTXODecodeCoin(v recipeOrderedValue) (utxoCoin, error) {
	if recipeOrderedCheck(v, 0) != nil || v.Kind != "object" || len(v.Object) != 8 {
		return utxoCoin{}, fmt.Errorf("invalid ordered Coin value")
	}
	o := v.Object
	for _, name := range []string{"vout", "height", "tx_index", "value"} {
		if o[name].Kind != "uint" {
			return utxoCoin{}, fmt.Errorf("invalid Coin integer")
		}
	}
	if o["txid"].Kind != "text" || !recipeStoreHashValid(o["txid"].Text) || o["block"].Kind != "text" || !recipeStoreHashValid(o["block"].Text) || o["coinbase"].Kind != "bool" || o["vout"].Uint > uint64(^uint32(0)) || o["height"].Uint > uint64(^uint64(0)>>1) || o["tx_index"].Uint > uint64(^uint(0)>>1) || o["value"].Uint > utxoMaxMoney {
		return utxoCoin{}, fmt.Errorf("invalid Coin identity")
	}
	f := o["facts"]
	if f.Kind != "object" || len(f.Object) != 2 || f.Object["page"].Kind != "text" || !recipeStoreHashValid(f.Object["page"].Text) || f.Object["row"].Kind != "uint" || f.Object["row"].Uint > uint64(^uint32(0)) {
		return utxoCoin{}, fmt.Errorf("invalid Coin fact reference")
	}
	coin := utxoCoin{TxID: o["txid"].Text, Vout: uint32(o["vout"].Uint), Height: int64(o["height"].Uint), BlockHash: o["block"].Text, TxIndex: int(o["tx_index"].Uint), Coinbase: o["coinbase"].Bool, ValueSats: o["value"].Uint, FactRef: outputFactRef{Page: f.Object["page"].Text, Row: uint32(f.Object["row"].Uint)}}
	coin.Position = outputCoordinate(int(coin.Vout), coin.TxIndex, coin.Height)
	return coin, nil
}

func recipeUTXOOutpoint(key string) (string, uint32, error) {
	parts := strings.Split(key, ":")
	if len(parts) != 2 || !recipeStoreHashValid(parts[0]) {
		return "", 0, fmt.Errorf("invalid state outpoint")
	}
	n, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || strconv.FormatUint(n, 10) != parts[1] {
		return "", 0, fmt.Errorf("invalid state output index")
	}
	return parts[0], uint32(n), nil
}

func (a *recipeUTXOAdapter) check(ctx context.Context) error {
	if ctx == nil || a == nil || a.store == nil || a.failed || a.closed {
		return errRecipeStateConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current := ""
	if a.store.head != nil {
		current = a.store.head.Checkpoint.Commitment
	}
	if current != a.baseCommit {
		return errRecipeStateConflict
	}
	return nil
}

func (a *recipeUTXOAdapter) physicalCount(txid string) (recipeOrderedValue, error) {
	var bridge utxoBridge
	found, err := a.edit.trees["bridge"].get(utxoBridgeKey(txid), &bridge)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if !found {
		return recipeOrderedValue{Kind: "missing"}, nil
	}
	var tx utxoTransaction
	found, err = a.edit.trees["transactions"].get(utxoTransactionKey(bridge.Height, bridge.TxIndex), &tx)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if !found || tx.TxID != txid || tx.Height != bridge.Height || tx.TxIndex != bridge.TxIndex || tx.BlockHash != bridge.BlockHash || tx.LiveOutputs == 0 {
		return recipeOrderedValue{}, ErrUTXOStateCorrupt
	}
	return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"live": rovUint(tx.LiveOutputs)}}, nil
}

func (a *recipeUTXOAdapter) captureCount(txid string) error {
	if _, ok := a.counts[txid]; ok {
		return nil
	}
	v, err := a.physicalCount(txid)
	if err != nil {
		return err
	}
	a.counts[txid] = v
	var origin utxoBridge
	if _, err = a.edit.trees["bridge"].get(utxoBridgeKey(txid), &origin); err != nil {
		return err
	}
	a.origins[txid] = origin
	return nil
}

// BIP30 temporarily keeps old and new incarnations live while the native
// bridge already selects the new transaction. Resolve untouched outpoints
// through the captured old incarnation; changed slots carry only fact refs.
func (a *recipeUTXOAdapter) member(txid string, vout uint32) (utxoMember, bool, error) {
	key, err := recipeBitcoinOutpoint(txid, vout)
	if err != nil {
		return utxoMember{}, false, err
	}
	if member, found := a.coins[key]; found {
		return member, member.Position != "", nil
	}
	if origin, found := a.origins[txid]; found {
		if origin.BlockHash == "" {
			return utxoMember{}, false, nil
		}
		var member utxoMember
		found, err = a.edit.trees["live"].get(utxoPositionKey(origin.Height, origin.TxIndex, int(vout)), &member)
		return member, found, err
	}
	return a.store.memberForOutpoint(a.edit.trees, txid, vout)
}

func (a *recipeUTXOAdapter) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if err := a.check(ctx); err != nil {
		return recipeOrderedValue{}, err
	}
	if err := recipeStateCheckKey(key); err != nil {
		return recipeOrderedValue{}, err
	}
	switch key.Table {
	case "coins":
		txid, vout, err := recipeUTXOOutpoint(key.Key)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		member, found, err := a.member(txid, vout)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		if !found {
			return recipeOrderedValue{Kind: "missing"}, nil
		}
		coin, err := a.store.coinUsingReader(ctx, a.edit.trees, member, a.edit.facts)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		return recipeUTXOCoinValue(coin), nil
	case "counts":
		if !recipeStoreHashValid(key.Key) {
			return recipeOrderedValue{}, fmt.Errorf("invalid count key")
		}
		if err := a.captureCount(key.Key); err != nil {
			return recipeOrderedValue{}, err
		}
		v := a.counts[key.Key]
		if v.Kind == "missing" {
			return v, nil
		}
		return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"live": v.Object["live"]}}, nil
	default:
		return recipeOrderedValue{}, fmt.Errorf("unsupported native UTXO state table %q", key.Table)
	}
}

func (a *recipeUTXOAdapter) currentCoin(ctx context.Context, expected utxoCoin) (utxoMember, utxoCoin, error) {
	var member utxoMember
	found, err := a.edit.trees["live"].get(utxoPositionKey(expected.Height, expected.TxIndex, int(expected.Vout)), &member)
	if err != nil {
		return member, utxoCoin{}, err
	}
	if !found || member.Position != expected.Position || member.FactRef != expected.FactRef {
		return member, utxoCoin{}, errRecipeStateConflict
	}
	coin, err := a.store.coinUsingReader(ctx, a.edit.trees, member, a.edit.facts)
	if err != nil {
		return member, coin, err
	}
	identity := coin
	identity.ScriptPubKey = ""
	if identity != expected {
		return member, coin, errRecipeStateConflict
	}
	return member, coin, nil
}

func (a *recipeUTXOAdapter) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) (err error) {
	defer func() {
		if err != nil {
			a.AbortOrdered()
		}
	}()
	if err = a.check(ctx); err != nil {
		return err
	}
	if err = recipeStateCheckKey(key); err != nil {
		return err
	}
	if key.Table == "counts" {
		if !recipeStoreHashValid(key.Key) || recipeOrderedCheck(value, 0) != nil || value.Kind != "object" || len(value.Object) != 1 || value.Object["live"].Kind != "uint" || value.Object["live"].Uint == 0 {
			return fmt.Errorf("invalid live count")
		}
		a.counts[key.Key] = recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"live": value.Object["live"]}}
		return nil
	}
	if key.Table != "coins" {
		return fmt.Errorf("unsupported native UTXO state table")
	}
	txid, vout, err := recipeUTXOOutpoint(key.Key)
	if err != nil {
		return err
	}
	coin, err := recipeUTXODecodeCoin(value)
	if err != nil || coin.TxID != txid || coin.Vout != vout || coin.Height != a.block.Height || coin.BlockHash != a.block.Hash || coin.TxIndex < 0 || coin.TxIndex >= len(a.block.Transactions) {
		return fmt.Errorf("Coin creation identity mismatch")
	}
	ref, found := a.facts[coin.Position]
	if !found || ref != coin.FactRef {
		return fmt.Errorf("Coin creation has no admitted mandatory fact")
	}
	fact, err := a.edit.facts.read(ref)
	if err != nil || fact.Height != coin.Height || fact.BlockHash != coin.BlockHash || fact.TxIndex != coin.TxIndex || fact.Vout != coin.Vout || fact.ValueSats != coin.ValueSats || fact.Coinbase != coin.Coinbase || a.block.Transactions[coin.TxIndex].TxID != coin.TxID {
		return fmt.Errorf("Coin creation fact mismatch")
	}
	if err = a.captureCount(txid); err != nil {
		return err
	}
	if _, found, err = a.member(txid, vout); err != nil {
		return err
	}
	if found {
		return fmt.Errorf("Coin creation outpoint already present; delete its prior incarnation first")
	}
	var existing utxoMember
	found, err = a.edit.trees["live"].get(utxoPositionKey(coin.Height, coin.TxIndex, int(coin.Vout)), &existing)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("Coin creation position already present")
	}
	member := utxoMember{Position: coin.Position, FactRef: ref}
	if err = a.store.changeCoin(ctx, a.edit, a.checkpoint, member, txid, true); err != nil {
		return err
	}
	a.coins[key.Key] = member
	return nil
}

func (a *recipeUTXOAdapter) DeleteOrdered(ctx context.Context, key recipeStateKey) (err error) {
	defer func() {
		if err != nil {
			a.AbortOrdered()
		}
	}()
	if err = a.check(ctx); err != nil {
		return err
	}
	if err = recipeStateCheckKey(key); err != nil {
		return err
	}
	if key.Table == "counts" {
		if !recipeStoreHashValid(key.Key) {
			return fmt.Errorf("invalid count key")
		}
		a.counts[key.Key] = recipeOrderedValue{Kind: "missing"}
		return nil
	}
	if key.Table != "coins" {
		return fmt.Errorf("unsupported native UTXO state table")
	}
	txid, vout, err := recipeUTXOOutpoint(key.Key)
	if err != nil {
		return err
	}
	if err = a.captureCount(txid); err != nil {
		return err
	}
	member, found, err := a.member(txid, vout)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err = a.store.changeCoin(ctx, a.edit, a.checkpoint, member, txid, false); err != nil {
		return err
	}
	a.coins[key.Key] = utxoMember{}
	return nil
}

func (a *recipeUTXOAdapter) CommitOrdered(ctx context.Context) error {
	if err := a.check(ctx); err != nil {
		return err
	}
	if a.publish == nil {
		return errRecipeStateConflict
	}
	for txid, expected := range a.counts {
		if err := a.check(ctx); err != nil {
			return err
		}
		actual, err := a.physicalCount(txid)
		if err != nil {
			return err
		}
		if actual.Kind != expected.Kind || actual.Kind != "missing" && actual.Object["live"].Uint != expected.Object["live"].Uint {
			return fmt.Errorf("logical and physical live counters differ")
		}
		if origin := a.origins[txid]; origin.BlockHash != "" {
			var bridge utxoBridge
			found, err := a.edit.trees["bridge"].get(utxoBridgeKey(txid), &bridge)
			if err != nil {
				return err
			}
			if !found || bridge != origin {
				var prior utxoTransaction
				stillLive, err := a.edit.trees["transactions"].get(utxoTransactionKey(origin.Height, origin.TxIndex), &prior)
				if err != nil {
					return err
				}
				if stillLive {
					return fmt.Errorf("Coin contract left an inaccessible older transaction incarnation")
				}
			}
		}
	}
	a.closed = true // publication may have an uncertain filesystem outcome
	return a.publish()
}

func (a *recipeUTXOAdapter) AbortOrdered() {
	if a != nil && !a.closed {
		a.failed = true
	}
}

func (a *recipeUTXOAdapter) emit(ctx context.Context, table, key string, value recipeOrderedValue) error {
	if err := a.check(ctx); err != nil {
		return err
	}
	if table != "prevouts" || value.Kind != "object" || len(value.Object) != 3 || a.emitted[key] {
		return fmt.Errorf("invalid native prevout proposal")
	}
	o := value.Object
	if o["tx_index"].Kind != "uint" || o["input_index"].Kind != "uint" || o["tx_index"].Uint >= uint64(len(a.block.Transactions)) {
		return fmt.Errorf("invalid prevout position")
	}
	ti, vi := int(o["tx_index"].Uint), o["input_index"].Uint
	if ti == 0 || vi >= uint64(len(a.block.Transactions[ti].Inputs)) || key != inputCoordinate(int(vi), ti, a.block.Height) {
		return fmt.Errorf("invalid prevout input")
	}
	coin, err := recipeUTXODecodeCoin(o["coin"])
	if err != nil {
		return err
	}
	in := a.block.Transactions[ti].Inputs[vi]
	if in.PrevTxID != coin.TxID || in.PrevVout != coin.Vout {
		return fmt.Errorf("prevout source mismatch")
	}
	_, actual, err := a.currentCoin(ctx, coin)
	if err != nil {
		return err
	}
	a.emitted[key] = true
	// The cohort can reuse authenticated creation facts, but a fact reference
	// owned by a custom generation must never look like a native-profile pin.
	if a.store.privateFacts {
		actual.FactRef = outputFactRef{}
	}
	runRememberPrevout(ctx, ti, int(vi), actual)
	return nil
}
