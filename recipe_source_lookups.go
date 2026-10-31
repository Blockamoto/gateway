package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Point reads are declared source facts, distinct from complete saved-parent
// snapshots or same-block scalar exports. No declaration starts an index writer.
type recipeSourceLookup struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	Provider     string `json:"provider"`
	Revision     string `json:"revision"`
	SemanticHash string `json:"semantic_hash"`
	Key          string `json:"key"`
	Type         string `json:"type"`
}

func (d *recipeSourceLookup) UnmarshalJSON(raw []byte) error {
	if err := recipeValidateJSON(raw); err != nil {
		return err
	}
	if _, err := recipeOrderedJSONObj(raw, "name", "source", "provider", "revision", "semantic_hash", "key", "type"); err != nil {
		return err
	}
	type plain recipeSourceLookup
	return json.Unmarshal(raw, (*plain)(d))
}

const recipeInputCreationLookupProfile = "gateway-bitcoin-input-creation-lookups-v1"

func recipeSourceLookupBindings() []recipeSourceLookup {
	result := []recipeSourceLookup{}
	for _, provider := range []string{"cohort", "same_block", "stored", "provider"} {
		d := recipeSourceLookup{Name: provider + "_creation", Source: "bitcoin.input_creation", Provider: provider, Key: "input_ref", Type: "creation_evidence"}
		d.Revision = indexDigest(struct{ Profile, Source, Provider, Key, Type string }{recipeInputCreationLookupProfile, d.Source, d.Provider, d.Key, d.Type})
		d.SemanticHash = indexDigest(struct {
			Revision, Network, Authority string
			Verifier                     int
		}{d.Revision, "bitcoin-mainnet", "checked-positive-flat-state-position-block-hash-v1", blockVerifierVersion})
		result = append(result, d)
	}
	return result
}

func recipeValidateSourceLookups(d recipeDefinition) (map[string]recipeSourceLookup, error) {
	result := map[string]recipeSourceLookup{}
	if len(d.Lookups) > 16 || len(d.Lookups) > 0 && len(d.Exports) > 0 {
		return nil, recipeDiagnosticAt("RECIPE_LOOKUP_CONTRACT", "/lookups", "source lookups are bounded readonly inputs; outgoing exports require a separate checked contract")
	}
	bindings := recipeSourceLookupBindings()
	for i, lookup := range d.Lookups {
		if !recipeRegistryID.MatchString(lookup.Name) {
			return nil, fmt.Errorf("invalid source lookup name at %d", i)
		}
		if _, exists := result[lookup.Name]; exists {
			return nil, fmt.Errorf("duplicate source lookup name %q", lookup.Name)
		}
		matched := false
		for _, binding := range bindings {
			binding.Name = lookup.Name
			if binding == lookup {
				matched = true
				break
			}
		}
		if !matched {
			return nil, recipeDiagnosticAt("RECIPE_LOOKUP_BINDING", fmt.Sprintf("/lookups/%d", i), "source lookup requires an exact installed provider, revision, semantic fingerprint and typed key/value contract")
		}
		result[lookup.Name] = lookup
	}
	return result, nil
}

type recipeInputCreationLookupResult struct {
	creation  txioCreation
	available bool
}
type recipeInputCreationLookups struct {
	ctx               context.Context
	a                 *app
	native            *indexStore
	block             blockView
	declarations      map[string]recipeSourceLookup
	cache             map[recipeStateKey]recipeInputCreationLookupResult
	anchors           map[int64]string
	stored            *indexStore
	storedOpened      bool
	providerChecked   bool
	providerAvailable bool
	operations, bytes int
	closed            bool
}

func newRecipeInputCreationLookups(ctx context.Context, a *app, native *indexStore, block blockView, d recipeDefinition) (*recipeInputCreationLookups, error) {
	if ctx == nil || block.Height < 0 {
		return nil, fmt.Errorf("source lookup requires its owned block and context")
	}
	declarations, err := recipeValidateSourceLookups(d)
	if err != nil {
		return nil, err
	}
	return &recipeInputCreationLookups{ctx: ctx, a: a, native: native, block: block, declarations: declarations, cache: map[recipeStateKey]recipeInputCreationLookupResult{}, anchors: map[int64]string{}}, nil
}

func (r *recipeInputCreationLookups) PutOrdered(context.Context, recipeStateKey, recipeOrderedValue) error {
	return fmt.Errorf("source lookups do not accept writes")
}
func (r *recipeInputCreationLookups) DeleteOrdered(context.Context, recipeStateKey) error {
	return fmt.Errorf("source lookups do not accept deletes")
}
func (r *recipeInputCreationLookups) AbortOrdered() { r.closed = true }

func (r *recipeInputCreationLookups) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if ctx == nil || r.closed {
		return recipeOrderedValue{}, fmt.Errorf("source lookup reader is closed")
	}
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if err := r.ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	declaration, ok := r.declarations[key.Table]
	if !ok {
		return recipeOrderedValue{}, fmt.Errorf("undeclared source lookup %q", key.Table)
	}
	r.operations++
	if r.operations > recipeStateHardOperations {
		return recipeOrderedValue{}, errRecipeStateBudget
	}
	result, cached := r.cache[key]
	if !cached {
		if len(r.cache) >= recipeStateHardTouched {
			return recipeOrderedValue{}, errRecipeStateBudget
		}
		position, err := parseTXIOPosition("input", key.Key)
		if err != nil || position.Height != r.block.Height || position.TxIndex < 1 || position.TxIndex >= len(r.block.Transactions) || position.IOIndex >= len(r.block.Transactions[position.TxIndex].Inputs) || r.block.Transactions[position.TxIndex].Coinbase {
			return recipeOrderedValue{}, fmt.Errorf("source lookup key must identify a spending input in its owned block")
		}
		input := r.block.Transactions[position.TxIndex].Inputs[position.IOIndex]
		if input.Coinbase || !validHash(input.PrevTxID) {
			return recipeOrderedValue{}, fmt.Errorf("invalid source spending input")
		}
		creation, err := r.creation(ctx, declaration.Provider, input, position.TxIndex, position.IOIndex)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		if err = ctx.Err(); err != nil {
			return recipeOrderedValue{}, err
		}
		if creation.Position != "" {
			c, ok := parseBODCoordinate(creation.Position)
			if !ok || c.Kind != coordOutput || c.TxID != "" || creation.Position != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) || uint64(c.IOIndex) != uint64(input.PrevVout) || c.Height > r.block.Height || c.Height == r.block.Height && c.TxIndex >= position.TxIndex || !validHash(creation.BlockHash) {
				return recipeOrderedValue{}, fmt.Errorf("TXIO provider returned invalid creation evidence")
			}
			if old := r.anchors[c.Height]; old != "" && old != creation.BlockHash {
				return recipeOrderedValue{}, fmt.Errorf("creation sources disagree on chain identity")
			}
			r.anchors[c.Height] = creation.BlockHash
			result = recipeInputCreationLookupResult{creation: creation, available: true}
		}
		cost := 128 + len(key.Table) + len(key.Key) + len(creation.Position) + len(creation.BlockHash)
		if cost > recipeStateHardBytes-r.bytes {
			return recipeOrderedValue{}, errRecipeStateBudget
		}
		r.bytes += cost
		r.cache[key] = result
	}
	value := recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"state": rovText("unavailable")}}
	if result.available {
		value.Object["state"] = rovText("available")
		value.Object["position"], value.Object["block_hash"] = rovText(result.creation.Position), rovText(result.creation.BlockHash)
	}
	return value, nil
}

func (r *recipeInputCreationLookups) creation(ctx context.Context, provider string, in inputView, ti, vin int) (txioCreation, error) {
	switch provider {
	case "cohort":
		if coin, known := runPrevout(ctx, ti, vin); known && coin.TxID == in.PrevTxID && coin.Vout == in.PrevVout {
			return txioCreation{coin.Position, coin.BlockHash}, nil
		}
	case "same_block":
		for j := ti - 1; j >= 0; j-- {
			if r.block.Transactions[j].TxID == in.PrevTxID {
				if int(in.PrevVout) >= len(r.block.Transactions[j].Outputs) {
					return txioCreation{}, fmt.Errorf("previous output is absent from its latest same-block incarnation")
				}
				return txioCreation{outputCoordinate(int(in.PrevVout), j, r.block.Height), r.block.Hash}, nil
			}
		}
	case "stored":
		if r.native != nil {
			return r.native.ownTXIOCreation(ctx, in, r.block.Height)
		}
		if r.a == nil {
			return txioCreation{}, nil
		}
		if !r.storedOpened {
			r.storedOpened = true
			s, err := indexStoreHead(r.a.dataDir, "txo-spender")
			if err != nil {
				return txioCreation{}, err
			}
			r.stored = s
			r.stored.locatorContext = ctx
		}
		return r.stored.ownTXIOCreationAccess(ctx, in, r.block.Height, false)
	case "provider":
		var creation txioCreation
		var err error
		if r.native != nil && r.native.spenderOrigin != nil {
			creation, err = r.native.spenderOrigin(ctx, in, r.block.Height, ti)
		} else if r.a != nil {
			if !r.hasConfiguredEvidence(in.PrevTxID) {
				return txioCreation{}, nil
			}
			var parent satlineTxEvidence
			parent, err = r.a.positionedTXIOParent(context.WithValue(ctx, indexLocatorReadOnlyKey{}, true), in.PrevTxID, r.block.Height)
			if err == nil {
				if parent.Height > r.block.Height || parent.Height == r.block.Height && parent.Tx.Index >= ti || parent.Tx.TxID != in.PrevTxID || int(in.PrevVout) >= len(parent.Tx.Outputs) || !validHash(parent.BlockHash) {
					return txioCreation{}, fmt.Errorf("previous output creation evidence mismatch")
				}
				creation = txioCreation{outputCoordinate(int(in.PrevVout), parent.Tx.Index, parent.Height), parent.BlockHash}
			}
		}
		if err != nil && ctx.Err() != nil {
			return txioCreation{}, ctx.Err()
		}
		return creation, nil
	default:
		return txioCreation{}, fmt.Errorf("unknown source lookup provider")
	}
	return txioCreation{}, nil
}

// Capture wholly absent provider inventories once, on their first use. An
// empty offline profile cannot supply positioned evidence, and repeatedly
// probing every ancestor path for every input is expensive on Windows. Any
// existing inventory or non-absence error retains the regular checked lookup.
func (r *recipeInputCreationLookups) hasConfiguredEvidence(txid string) bool {
	r.a.cacheMu.RLock()
	_, cached := r.a.cacheIndex.Tx[txid]
	r.a.cacheMu.RUnlock()
	if cached {
		return true
	}
	if r.providerChecked {
		return r.providerAvailable
	}
	r.providerChecked = true
	r.a.settingsMu.RLock()
	core := !r.a.settings.CoreDisabled
	r.a.settingsMu.RUnlock()
	if core {
		r.providerAvailable = true
		return true
	}
	for _, path := range []string{
		filepath.Join(r.a.dataDir, "indexes", "tx-locator"),
		filepath.Join(r.a.dataDir, "indexes", "blocks"),
		filepath.Join(r.a.dataDir, "indexes", "browse", "tx-locator"),
		r.a.graphDir(),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			r.providerAvailable = true
			return true
		}
	}
	return false
}

// The caller's publication guard holds selected-header authority. Positive
// point facts are checked again before rows become visible. A single handle
// avoids opening a header file for every distinct creation height.
func (r *recipeInputCreationLookups) checkAnchorsLocked(ctx context.Context) error {
	if r.a == nil || len(r.anchors) == 0 {
		return ctx.Err()
	}
	f, err := os.Open(r.a.headersPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var header [80]byte
	for height, want := range r.anchors {
		if height < 0 || height > ((1<<63-1)-79)/80 {
			return fmt.Errorf("creation source header height is out of range")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = f.ReadAt(header[:], height*80); err != nil {
			if err == io.EOF {
				return fmt.Errorf("creation source selected header is unavailable")
			}
			return err
		}
		digest := hash256(header[:])
		if reverseHex(digest[:]) != want {
			return fmt.Errorf("creation source selected header changed")
		}
	}
	return ctx.Err()
}

func (r *recipeInputCreationLookups) offered(key string, creation txioCreation) bool {
	for name := range r.declarations {
		if value, ok := r.cache[recipeStateKey{Table: name, Key: key}]; ok && value.available && value.creation == creation {
			return true
		}
	}
	return false
}
