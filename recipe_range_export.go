package main

// Generic sparse row export consumes committed receipts only. The caller must
// discard every staged byte on error and expose an attachment only after the
// final header/registry/complete-range receipt check succeeds.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"./internal/updateapply"
)

type recipeRangeExportMetadata struct {
	Snapshot                      string
	Rows                          uint64
	CoveredUnits, UncoveredBlocks int64
}

// Walk immutable receipt keys with radix pruning and constant-depth traversal.
// Payloads and coverage intervals are emitted incrementally, never collected
// into a whole-range list. Context cancellation bounds an arbitrarily long run.
func (s *recipeStore) eachRangeRef(ctx context.Context, head recipeStoreHead, from, to int64, emit func(int64, recipeStoreRef) error) error {
	if ctx == nil || emit == nil || from < 0 || to < from {
		return fmt.Errorf("invalid sparse receipt walk")
	}
	low, high := recipeStoreHeightKey(from), recipeStoreHeightKey(to)
	tree := s.tree(ctx, head)
	var walk func(string, string) error
	walk = func(hash, prefix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if hash == "" {
			return nil
		}
		if len(prefix) > 16 {
			return fmt.Errorf("invalid sparse receipt tree depth")
		}
		if prefix+strings.Repeat("f", 16-len(prefix)) < low || prefix+strings.Repeat("0", 16-len(prefix)) > high {
			return nil
		}
		page, err := tree.read(hash)
		if err != nil {
			return err
		}
		if len(page.Children) > 0 {
			if len(prefix) == 16 {
				return fmt.Errorf("sparse receipt branch is too deep")
			}
			for i, child := range page.Children {
				if err = walk(child, prefix+"0123456789abcdef"[i:i+1]); err != nil {
					return err
				}
			}
			return nil
		}
		for _, entry := range page.Entries {
			if len(entry.Key) != 16 || !strings.HasPrefix(entry.Key, prefix) {
				return fmt.Errorf("invalid sparse receipt key")
			}
			if entry.Key < low || entry.Key > high {
				continue
			}
			height, e := strconv.ParseUint(entry.Key, 16, 64)
			if e != nil || height > uint64(^uint64(0)>>1) {
				return fmt.Errorf("invalid sparse receipt height")
			}
			var ref recipeStoreRef
			if e = json.Unmarshal(entry.Value, &ref); e != nil || !recipeStoreHashValid(ref.Unit) || !recipeStoreHashValid(ref.BlockHash) {
				return fmt.Errorf("invalid sparse receipt reference")
			}
			if e = emit(int64(height), ref); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(head.Root, "")
}

type recipeRangeExportEvidence struct {
	plan  *recipeLineagePlan
	heads map[recipeGraphIdentity]recipeStoreHead
	trees map[recipeGraphIdentity]*ordinalTree
}

// Verify every declared ancestor against the same pinned receipt heads and
// selected block. The root payload is already an owned verified view. Only
// tiny info/ref records survive each recursive read, including diamond DAGs.
func (e *recipeRangeExportEvidence) checkUnit(ctx context.Context, height int64, hash string, root recipeSparseUnitInfo, rootRef recipeStoreRef) error {
	loaded := map[recipeGraphIdentity]string{}
	visiting := map[recipeGraphIdentity]bool{}
	var visit func(recipeGraphIdentity, string) error
	visit = func(id recipeGraphIdentity, expected string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if prior, found := loaded[id]; found {
			if prior != expected {
				return fmt.Errorf("conflicting export ancestor receipt")
			}
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("cycle in export ancestry")
		}
		visiting[id] = true
		defer delete(visiting, id)
		node, found := e.plan.nodes[id]
		if !found || height < node.definition.Origin {
			return fmt.Errorf("export ancestor unavailable at requested height")
		}
		info, ref := root, rootRef
		if id != e.plan.root {
			var err error
			found, err = e.trees[id].get(recipeStoreHeightKey(height), &ref)
			if err != nil || !found {
				return fmt.Errorf("export ancestor receipt unavailable: %s", id.Instance)
			}
			if ref.BlockHash != hash || ref.Unit != expected {
				return fmt.Errorf("export ancestor receipt differs: %s", id.Instance)
			}
			info, err = node.store.readSparseInfo(ctx, ref, height)
			if err != nil {
				return err
			}
		}
		if ref.BlockHash != hash || ref.Unit != expected || info.BlockHash != hash || info.Height != height || len(info.Lineage) != len(node.definition.Dependencies) {
			return fmt.Errorf("incomplete export unit lineage")
		}
		receipts := map[string]recipeDependencyReceipt{}
		for _, r := range info.Lineage {
			receipts[r.Name] = r
		}
		for _, dep := range node.definition.Dependencies {
			r, ok := receipts[dep.Name]
			if !ok || r.Instance != dep.Instance || r.SemanticHash != dep.SemanticHash || r.Height != height || r.BlockHash != hash || !recipeStoreHashValid(r.Unit) {
				return fmt.Errorf("export lineage differs from its saved declaration")
			}
			if err := visit(recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}, r.Unit); err != nil {
				return err
			}
		}
		loaded[id] = ref.Unit
		return nil
	}
	return visit(e.plan.root, rootRef.Unit)
}

func (a *app) writeRecipeRangeExport(ctx context.Context, root resolvedSavedRecipe, from, to int64, destination io.Writer) (meta recipeRangeExportMetadata, err error) {
	if ctx == nil || destination == nil || from < 0 || to < from || to > (int64(^uint64(0)>>1)/80)-1 || root.History != "sparse" || !recipeSparseSource(root.Source) {
		return meta, fmt.Errorf("range export needs a sparse recipe and a valid inclusive header range")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	plan, err := a.prepareRecipeLineage(ctx, root)
	if err != nil {
		return meta, err
	}
	// One exact immutable header fragment is shared by every row/unit proof.
	// It is temporary export working data, never a profile checkpoint.
	headers, err := os.CreateTemp("", "gateway-recipe-export-headers-*")
	if err != nil {
		return meta, err
	}
	defer func() { _ = headers.Close(); _ = os.Remove(headers.Name()) }()
	var snapshot recipeDAGRangeSnapshot
	var heads map[recipeGraphIdentity]recipeStoreHead
	err = func() error {
		if err := a.lockSelectedHeaders051(); err != nil {
			return err
		}
		defer a.headerChainMu.RUnlock()
		recipeRegistryMu.Lock()
		defer recipeRegistryMu.Unlock()
		if _, err := recipeRegistryRead(a.dataDir, root.ID, ""); err != nil {
			return err
		}
		if err := a.recipeCheckLineageRegistryLocked(ctx, plan, recipeAdmissionPlan{Index: root.ID, Revision: root.Revision, SemanticHash: root.SemanticHash}); err != nil {
			return err
		}
		recipeStoreMutex.Lock()
		var err error
		snapshot, err = plan.captureRangeSnapshotLocked(ctx, from, to)
		if err == nil {
			heads, err = plan.checkRangeSnapshotLocked(ctx, snapshot, from, to)
		}
		recipeStoreMutex.Unlock()
		if err != nil {
			return err
		}
		if err = updateapply.CheckPath(a.headersPath); err != nil {
			return err
		}
		source, err := os.Open(a.headersPath)
		if err != nil {
			return err
		}
		defer source.Close()
		stat, err := source.Stat()
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() || stat.Size() < (to+1)*80 {
			return fmt.Errorf("selected local headers do not cover export range")
		}
		hash := sha256.New()
		size := (to - from + 1) * 80
		n, err := io.CopyBuffer(recipeJSONContextWriter{ctx: ctx, w: io.MultiWriter(headers, hash)}, io.NewSectionReader(source, from*80, size), make([]byte, 64<<10))
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("selected header export fragment changed extent")
		}
		snapshot.Headers = hex.EncodeToString(hash.Sum(nil))
		return nil
	}()
	if err != nil {
		return meta, err
	}
	meta.Snapshot, err = snapshot.token()
	if err != nil {
		return meta, err
	}
	evidence := &recipeRangeExportEvidence{plan: plan, heads: heads, trees: map[recipeGraphIdentity]*ordinalTree{}}
	for id, node := range plan.nodes {
		evidence.trees[id] = node.store.tree(ctx, heads[id])
	}
	out := recipeJSONContextWriter{ctx: ctx, w: destination}
	encoder := json.NewEncoder(out)
	if err = encoder.Encode(map[string]any{"type": "manifest", "format": "gateway-recipe-rows", "version": 1, "instance": root.ID, "revision": root.Revision, "semantic_hash": root.SemanticHash, "from": from, "to": to, "authority": "selected_local_header_range_as_of_export", "coverage": "sparse_committed_only", "snapshot": meta.Snapshot}); err != nil {
		return meta, err
	}
	next := from
	gap := func(first, last int64) error {
		if first > last {
			return nil
		}
		meta.UncoveredBlocks += last - first + 1
		return encoder.Encode(map[string]any{"type": "uncovered", "from": first, "to": last})
	}
	store := plan.nodes[plan.root].store
	err = store.eachRangeRef(ctx, heads[plan.root], from, to, func(height int64, ref recipeStoreRef) error {
		if height < next {
			return fmt.Errorf("export receipt ordering differs")
		}
		if err := gap(next, height-1); err != nil {
			return err
		}
		var header [80]byte
		if _, err := headers.ReadAt(header[:], (height-from)*80); err != nil {
			return err
		}
		digest := hash256(header[:])
		selected := reverseHex(digest[:])
		if selected != ref.BlockHash || height < root.Origin {
			return fmt.Errorf("export receipt differs from its selected local header")
		}
		view, err := store.openSparseView(ctx, ref, height)
		if err != nil {
			return err
		}
		defer view.Close()
		if err = evidence.checkUnit(ctx, height, selected, view.info, ref); err != nil {
			return err
		}
		if err = encoder.Encode(map[string]any{"type": "unit", "height": height, "block_hash": selected, "unit": ref.Unit, "row_count": view.info.Rows, "lineage": view.info.Lineage}); err != nil {
			return err
		}
		buffer := make([]byte, 64<<10)
		for index := uint64(0); index < view.info.Rows; index++ {
			span, err := view.RowSpan(index)
			if err != nil {
				return err
			}
			prefix := fmt.Sprintf(`{"type":"row","height":%d,"unit":%q,"row":`, height, ref.Unit)
			if _, err = out.Write([]byte(prefix)); err != nil {
				return err
			}
			if _, err = io.CopyBuffer(out, io.NewSectionReader(view.file, span.Row.Offset, span.Row.Bytes), buffer); err != nil {
				return err
			}
			if _, err = out.Write([]byte("}\n")); err != nil {
				return err
			}
			if meta.Rows == ^uint64(0) {
				return fmt.Errorf("export row count overflow")
			}
			meta.Rows++
		}
		meta.CoveredUnits++
		next = height + 1
		return ctx.Err()
	})
	if err != nil {
		return meta, err
	}
	if err = gap(next, to); err != nil {
		return meta, err
	}
	// Every ancestor's entire interval is rechecked, including units already
	// emitted. Appends outside the interval remain harmless. This is a local
	// selected-header snapshot, not an independent consensus assertion.
	err = func() error {
		if err := a.lockSelectedHeaders051(); err != nil {
			return err
		}
		defer a.headerChainMu.RUnlock()
		recipeRegistryMu.Lock()
		defer recipeRegistryMu.Unlock()
		if _, err := recipeRegistryRead(a.dataDir, root.ID, ""); err != nil {
			return err
		}
		if err := a.recipeCheckLineageRegistryLocked(ctx, plan, recipeAdmissionPlan{Index: root.ID, Revision: root.Revision, SemanticHash: root.SemanticHash}); err != nil {
			return err
		}
		current, err := a.recipeHeaderRangeHashLocked(ctx, from, to)
		if err != nil {
			return err
		}
		if current != snapshot.Headers {
			return fmt.Errorf("selected local headers changed during export")
		}
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		_, err = plan.checkRangeSnapshotLocked(ctx, snapshot, from, to)
		return err
	}()
	if err != nil {
		return meta, err
	}
	err = encoder.Encode(map[string]any{"type": "complete", "rows": meta.Rows, "covered_units": meta.CoveredUnits, "uncovered_blocks": meta.UncoveredBlocks, "snapshot": meta.Snapshot})
	return meta, err
}
