package main

import (
	"encoding/hex"
	"fmt"
)

// Frozen pre-recipe current-address mutation loop, test-only.
func (e *addressStateEditor) legacyChange(member utxoMember, add bool) error {
	f, err := e.memberFact(member)
	if err != nil {
		return err
	}
	_, address, err := addressStateScript(f.ScriptPubKey)
	if err != nil {
		return err
	}
	// Old projections excluded P2PK. Keep their accounting and existing
	// conventional lookups usable until an explicit complete traversal replaces
	// them; an incremental transition cannot backfill old live outputs.
	if e.cp.DestinationVersion == 0 {
		raw, _ := hex.DecodeString(f.ScriptPubKey)
		_, address = describeScript(raw)
	}
	if address == "" {
		if add {
			e.cp.ExcludedOutputCount++
			e.cp.ExcludedValueSats += f.ValueSats
		} else {
			if e.cp.ExcludedOutputCount == 0 || e.cp.ExcludedValueSats < f.ValueSats {
				return fmt.Errorf("%w: excluded totals", ErrAddressStateCorrupt)
			}
			e.cp.ExcludedOutputCount--
			e.cp.ExcludedValueSats -= f.ValueSats
		}
		return nil
	}
	key := addressStateScriptKey(f.ScriptPubKey)
	tree := e.buckets[key]
	if tree == nil {
		var bucket addressStateBucket
		var have bool
		if have, err = e.summary.get(key, &bucket); err != nil {
			return err
		}
		if have {
			if err = validateAddressBucket(bucket); err != nil {
				return err
			}
		}
		e.counts[key] = bucket
		tree = newOrdinalTree(addressStatePages(e.root, e.cp.PageGeneration), bucket.Root).withContext(e.summary.ctx)
		for _, member := range bucket.Inline {
			memberKey, err := addressMemberKey(member)
			if err != nil {
				return err
			}
			if err = tree.put(memberKey, member); err != nil {
				return err
			}
		}
		e.buckets[key] = tree
	}
	bucket := e.counts[key]
	positionKey := utxoPositionKey(f.Height, f.TxIndex, int(f.Vout))
	var existing utxoMember
	present, err := tree.get(positionKey, &existing)
	if err != nil {
		return err
	}
	if add {
		if present {
			return fmt.Errorf("%w: repeated live address output", ErrAddressStateCorrupt)
		}
		if err = tree.put(positionKey, member); err != nil {
			return err
		}
		bucket.Count++
		bucket.ValueSats += f.ValueSats
		e.cp.OutputCount++
		e.cp.ValueSats += f.ValueSats
	} else {
		if !present || existing != member || bucket.Count == 0 || bucket.ValueSats < f.ValueSats || e.cp.OutputCount == 0 || e.cp.ValueSats < f.ValueSats {
			return fmt.Errorf("%w: missing live address output", ErrAddressStateCorrupt)
		}
		tree.remove(positionKey)
		bucket.Count--
		bucket.ValueSats -= f.ValueSats
		e.cp.OutputCount--
		e.cp.ValueSats -= f.ValueSats
	}
	e.counts[key] = bucket
	return nil
}
