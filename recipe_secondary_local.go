package main

import (
	"context"
	"fmt"
	"os"
)

// Called with headerChainMu held; the header file must contain an integral
// prefix and the requested height must fit its current selected extent.
func (a *app) recipeSecondaryCheckHeaderExtent(height int64) error {
	if height < 0 || height > recipeSecondaryMaxHeaderHeight {
		return fmt.Errorf("invalid secondary prefix height")
	}
	info, e := os.Stat(a.headersPath)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size()%80 != 0 || height >= info.Size()/80 {
		return fmt.Errorf("secondary prefix outside selected header extent")
	}
	return nil
}

// recipeSecondarySelectedHash checks an anchor under a selected-prefix fence.
// It is an observation, not a publication lease; the final local guard repeats
// the prefix and exact registry-revision checks while holding both locks.
func (a *app) recipeSecondarySelectedHash(ctx context.Context, prefixHeight int64, prefixHash string, height int64) (string, error) {
	if a == nil || ctx == nil || height < 0 || prefixHeight < height || !recipeStoreHashValid(prefixHash) {
		return "", fmt.Errorf("invalid secondary selected anchor")
	}
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if e := a.lockSelectedHeaders051(); e != nil {
		return "", e
	}
	defer a.headerChainMu.RUnlock()
	if e := a.recipeSecondaryCheckHeaderExtent(prefixHeight); e != nil {
		return "", e
	}
	prefix, e := readHeaderAt(a.headersPath, prefixHeight)
	if e != nil {
		return "", e
	}
	ph := hash256(prefix)
	if reverseHex(ph[:]) != prefixHash {
		return "", fmt.Errorf("secondary selected prefix changed")
	}
	header, e := readHeaderAt(a.headersPath, height)
	if e != nil {
		return "", e
	}
	h := hash256(header)
	if e = ctx.Err(); e != nil {
		return "", e
	}
	return reverseHex(h[:]), nil
}
func (a *app) recipeSecondaryLocalGuard(ctx context.Context) recipeSecondaryGuard {
	return func(r recipeSecondaryReceipt, publish func() error) error {
		if a == nil || ctx == nil || publish == nil {
			return fmt.Errorf("invalid secondary local guard")
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := a.lockSelectedHeaders051(); e != nil {
			return e
		}
		defer a.headerChainMu.RUnlock()
		if e := a.recipeSecondaryCheckHeaderExtent(r.PrefixHeight); e != nil {
			return e
		}
		header, e := readHeaderAt(a.headersPath, r.PrefixHeight)
		if e != nil {
			return e
		}
		h := hash256(header)
		if reverseHex(h[:]) != r.PrefixHash {
			return fmt.Errorf("secondary selected prefix changed")
		}
		if len(r.Coverage) > recipeSecondaryMaxUnits {
			return fmt.Errorf("secondary coverage exceeds limit")
		}
		for i, anchor := range r.Coverage {
			if e = ctx.Err(); e != nil {
				return e
			}
			if anchor.Height < r.From || anchor.Height > r.To || anchor.Height > r.PrefixHeight || i > 0 && anchor.Height <= r.Coverage[i-1].Height {
				return fmt.Errorf("invalid secondary coverage anchor")
			}
			actual, e := readHeaderAt(a.headersPath, anchor.Height)
			if e != nil {
				return e
			}
			digest := hash256(actual)
			if reverseHex(digest[:]) != anchor.BlockHash {
				return fmt.Errorf("secondary coverage anchor is not selected")
			}
		}
		recipeRegistryMu.Lock()
		defer recipeRegistryMu.Unlock()
		view, e := recipeRegistryRead(a.dataDir, r.Instance, r.DefinitionRevision)
		if e != nil || !view.Validated || view.Revision != r.DefinitionRevision || view.SemanticHash != r.Fingerprint {
			return fmt.Errorf("secondary saved recipe revision unavailable")
		}
		primary := ""
		for _, table := range view.Recipe.Tables {
			if table.Name == r.Descriptor.Table {
				primary = table.Key
				break
			}
		}
		if primary == "" || primary != r.PrimaryColumn {
			return fmt.Errorf("secondary primary column differs from saved recipe")
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		return publish()
	}
}
