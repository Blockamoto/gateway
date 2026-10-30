package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const recipeProjectionQueryProfile = "gateway-address-projection-query-v1"

// Parent resolution precedes the authority locks. Its exact retained receipt is
// checked again under that fence. Queries neither create a store nor run work.
func (a *app) readProjectionRecipeOutput(ctx context.Context, prepared resolvedSavedRecipe, height int64, limit int, token, cursor string) (map[string]any, int, error) {
	if ctx == nil || height < 0 || limit < 1 || limit > 1000 || token != "" && !recipeStoreHashValid(token) || cursor != "" && token == "" {
		return nil, 400, fmt.Errorf("invalid projection output page")
	}
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || !recipeStoreHashValid(parts[0]) || !recipeStoreHashValid(parts[1]) {
			return nil, 400, fmt.Errorf("invalid projection member cursor")
		}
	}
	program, err := compileRecipe(prepared.Definition)
	if err != nil {
		return nil, 409, err
	}
	g, err := openRecipeProjectionGeneration(a.dataDir, program)
	if err != nil {
		return nil, 409, err
	}
	parent, parentErr := a.resolveRecipeProjectionParent(ctx, g.identity.Dependency)
	if parentErr == nil {
		parentErr = parent.atHeight(ctx, height)
	}
	if err = a.lockSelectedHeaders051(); err != nil {
		return nil, 409, err
	}
	defer a.headerChainMu.RUnlock()
	header, err := readHeaderAt(a.headersPath, height)
	if err != nil {
		return nil, 409, err
	}
	digest := hash256(header)
	selected := reverseHex(digest[:])
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	view, err := recipeRegistryRead(a.dataDir, prepared.ID, "")
	if err == nil && view.Revision != prepared.Revision {
		view, err = recipeRegistryRead(a.dataDir, prepared.ID, prepared.Revision)
	}
	if err != nil || !view.Validated || view.Revision != prepared.Revision || view.SemanticHash != prepared.SemanticHash {
		return nil, 409, fmt.Errorf("saved projection identity unavailable")
	}
	resolved, err := recipeResolveView(view)
	if err != nil || indexDigest(resolved.Definition) != indexDigest(prepared.Definition) {
		return nil, 409, fmt.Errorf("projection definition changed during output read")
	}
	recipeProjectionMu.Lock()
	defer recipeProjectionMu.Unlock()
	head, _, err := g.inspect(ctx)
	if err != nil {
		return nil, 409, err
	}
	result := map[string]any{"state": "uncovered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": prepared.Revision, "semantic_hash": prepared.SemanticHash}
	var point recipeProjectionPoint
	found := false
	for _, candidate := range head.History {
		if candidate.Height == height {
			point, found = candidate, true
			break
		}
	}
	if !found {
		if token != "" || cursor != "" {
			return nil, 409, fmt.Errorf("projection output snapshot is unavailable")
		}
		if err = ctx.Err(); err != nil {
			return nil, 409, err
		}
		return result, 0, nil
	}
	if parentErr != nil {
		return nil, 409, parentErr
	}
	if point.BlockHash != selected || point.Namespace != parent.point.Namespace {
		return nil, 409, fmt.Errorf("projection snapshot is not bound to the selected source")
	}
	cp, err := g.snapshot(ctx, point)
	if err != nil {
		return nil, 409, err
	}
	if cp.Source.Commitment != parent.point.Checkpoint.Commitment {
		return nil, 409, fmt.Errorf("projection source checkpoint changed")
	}
	unit := indexDigest(struct {
		Profile, Instance, Revision, Semantic string
		Point                                 recipeProjectionPoint
	}{recipeProjectionQueryProfile, prepared.ID, prepared.Revision, prepared.SemanticHash, point})
	if token != "" && token != unit {
		return nil, 409, fmt.Errorf("projection output snapshot changed")
	}
	if err = parent.checkLocked(ctx, a); err != nil {
		return nil, 409, err
	}
	rows, next, err := g.memberPage(ctx, parent, cp, limit, cursor)
	if err != nil {
		return nil, 409, err
	}
	if err = parent.checkLocked(ctx, a); err != nil {
		return nil, 409, err
	}
	result["state"], result["unit"], result["rows"] = "covered", unit, rows
	result["checkpoint"], result["ordering"] = cp.Commitment, "script_key_then_member_key"
	result["included_output_count"] = strconv.FormatUint(cp.OutputCount, 10)
	result["included_value_sats"] = strconv.FormatUint(cp.ValueSats, 10)
	result["excluded_output_count"] = strconv.FormatUint(cp.ExcludedOutputCount, 10)
	result["excluded_value_sats"] = strconv.FormatUint(cp.ExcludedValueSats, 10)
	result["excluded_rows_retained"] = false
	if next != "" {
		result["next_cursor"] = next
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > recipeOutputMaxResponseBytes {
		return nil, 409, fmt.Errorf("projection output page exceeds its response budget (%v)", err)
	}
	if err = ctx.Err(); err != nil {
		return nil, 409, err
	}
	return result, 0, nil
}

func (g *recipeProjectionGeneration) memberPage(ctx context.Context, parent *recipeProjectionParent, cp addressStateCheckpoint, limit int, cursor string) ([]recipeStoreRow, string, error) {
	scriptAfter, memberAfter := "", ""
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || !recipeStoreHashValid(parts[0]) || !recipeStoreHashValid(parts[1]) {
			return nil, "", fmt.Errorf("invalid projection member cursor")
		}
		scriptAfter, memberAfter = parts[0], parts[1]
	}
	rows := []recipeStoreRow{}
	next, more, bytesLeft := "", false, recipeOutputMaxResponseBytes-8192
	stop := errors.New("projection page complete")
	dir := addressStatePages(g.profileRoot, cp.PageGeneration)
	summary := newOrdinalTree(dir, cp.Root).withContext(ctx)
	live := parent.store.trees(ctx, cp.Source)["live"]
	facts := newUTXOFactReader(ctx, g.profileRoot)
	visitBucket := func(kv ordinalKV, after string) error {
		var bucket addressStateBucket
		if !validHash(kv.Key) || json.Unmarshal(kv.Value, &bucket) != nil || validateAddressBucket(bucket) != nil || bucket.Count > cp.OutputCount || bucket.ValueSats > cp.ValueSats {
			return fmt.Errorf("invalid projection summary bucket")
		}
		tree := newOrdinalTree(dir, bucket.Root).withContext(ctx)
		if after != "" {
			present := false
			if len(bucket.Inline) > 0 {
				for _, member := range bucket.Inline {
					key, err := addressMemberKey(member)
					if err != nil {
						return err
					}
					present = present || key == after
				}
			} else {
				var member utxoMember
				var err error
				present, err = tree.get(after, &member)
				if err != nil {
					return err
				}
			}
			if !present {
				return fmt.Errorf("projection cursor member is absent")
			}
		}
		return addressWalkBucketAfter(tree, bucket, after, func(row ordinalKV) error {
			if len(rows) >= limit {
				more = true
				return stop
			}
			var member, bound utxoMember
			if err := json.Unmarshal(row.Value, &member); err != nil {
				return err
			}
			found, err := live.get(row.Key, &bound)
			if err != nil || !found || bound != member {
				return fmt.Errorf("projection member differs from source coin (%v)", err)
			}
			fact, err := facts.read(member.FactRef)
			if err != nil {
				return err
			}
			if row.Key != utxoPositionKey(fact.Height, fact.TxIndex, int(fact.Vout)) || kv.Key != addressStateScriptKey(fact.ScriptPubKey) {
				return fmt.Errorf("projection member identity differs")
			}
			proposal, err := recipeAddressProjectionMember(ctx, g.program, member, fact, true)
			if err != nil || proposal.Table != "members" {
				return fmt.Errorf("stored member differs from projection JSON (%v)", err)
			}
			key, err := json.Marshal([]string{proposal.Table, proposal.Key})
			if err != nil {
				return err
			}
			value, err := json.Marshal(proposal.Value)
			if err != nil {
				return err
			}
			output := recipeStoreRow{Key: string(key), Value: value}
			wire, err := json.Marshal(output)
			if err != nil {
				return err
			}
			if len(wire)+1 > bytesLeft {
				if len(rows) == 0 {
					return fmt.Errorf("projection member exceeds its page budget")
				}
				more = true
				return stop
			}
			bytesLeft -= len(wire) + 1
			rows = append(rows, output)
			next = kv.Key + ":" + row.Key
			return ctx.Err()
		})
	}
	if scriptAfter != "" {
		var bucket json.RawMessage
		found, err := summary.get(scriptAfter, &bucket)
		if err != nil || !found {
			return nil, "", fmt.Errorf("projection cursor summary is absent (%v)", err)
		}
		if err = visitBucket(ordinalKV{Key: scriptAfter, Value: bucket}, memberAfter); err != nil {
			if errors.Is(err, stop) {
				return rows, next, nil
			}
			return nil, "", err
		}
	}
	err := summary.walkAfter(scriptAfter, func(kv ordinalKV) error { return visitBucket(kv, "") })
	if err != nil && !errors.Is(err, stop) {
		return nil, "", err
	}
	if !more {
		next = ""
	}
	return rows, next, ctx.Err()
}
