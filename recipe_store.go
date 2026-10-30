package main

// Recipe block receipts are a new, isolated storage generation. They do not
// assert compatibility with any existing index checkpoint or authenticate the
// selected chain. A caller supplies that authority and holds it during guards.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"./internal/updateapply"
)

const recipeStoreUnitMaxBytes = 16 << 20
const recipeStoreHeadMaxBytes = 16 << 10

// The caller must invoke publish exactly once while holding its selected-chain,
// admission and consent authority; an error before invocation leaves the head
// unchanged. The guard must synchronously call publish, and must not release
// that authority before publish returns or invoke publish after returning.
// This is a trusted internal contract: the wrapper rejects a callback starting
// after return but cannot prove that an already-running callback was synchronous
// or that a guard continued holding authority while it ran.
type recipeStoreGuard func(publish func() error) error

type recipeStoreRow struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}
type recipeStoreUnit struct {
	Schema      int                       `json:"schema"`
	Fingerprint string                    `json:"fingerprint"`
	Height      int64                     `json:"height"`
	BlockHash   string                    `json:"block_hash"`
	Rows        []recipeStoreRow          `json:"rows"`
	Lineage     []recipeDependencyReceipt `json:"lineage,omitempty"`
}
type recipeStoreRef struct {
	BlockHash string `json:"block_hash"`
	Unit      string `json:"unit"`
}
type recipeStoreHead struct {
	Schema      int    `json:"schema"`
	Instance    string `json:"instance"`
	Fingerprint string `json:"fingerprint"`
	Revision    uint64 `json:"revision"`
	Root        string `json:"root"`
	Checksum    string `json:"checksum"`
}
type recipeStore struct{ dir, instance, fingerprint string }

// In-process serialization covers all handles, including reopened handles.
// The embedding application must enforce its existing single-process profile
// ownership; this is not a cross-process lock.
var recipeStoreMutex sync.Mutex

func recipeStoreHash(b []byte) string    { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func recipeStoreHashValid(s string) bool { return s == strings.ToLower(s) && validHash(s) }
func recipeStoreHeadDigest(h recipeStoreHead) string {
	h.Checksum = ""
	b, _ := json.Marshal(h)
	return recipeStoreHash(b)
}
func recipeStoreHeightKey(height int64) string { return fmt.Sprintf("%016x", uint64(height)) }

// Instance is hashed into the path, so user-selected IDs cannot escape the
// profile. Separate semantic fingerprints get separate immutable generations.
func openRecipeStore(root, instance, fingerprint string) (*recipeStore, error) {
	if instance == "" || len(instance) > 1024 || !recipeStoreHashValid(fingerprint) {
		return nil, fmt.Errorf("invalid recipe instance or fingerprint")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(absolute, "indexes", "recipes-v1", recipeStoreHash([]byte(instance)), fingerprint)
	if err := updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	return &recipeStore{dir: dir, instance: instance, fingerprint: fingerprint}, nil
}

func recipeStoreReadBounded(path string, limit int64) ([]byte, error) {
	if err := updateapply.CheckPath(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("recipe store file exceeds bound or is not regular")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != info.Size() || int64(len(b)) > limit {
		return nil, fmt.Errorf("recipe store file extent changed")
	}
	return b, nil
}
func (s *recipeStore) head() (recipeStoreHead, error) {
	h := recipeStoreHead{Schema: 1, Instance: s.instance, Fingerprint: s.fingerprint}
	marker, markerErr := recipeStoreReadBounded(filepath.Join(s.dir, "initialized.json"), recipeStoreHeadMaxBytes)
	b, err := recipeStoreReadBounded(filepath.Join(s.dir, "head.json"), recipeStoreHeadMaxBytes)
	if os.IsNotExist(markerErr) && os.IsNotExist(err) {
		if _, historyErr := os.Stat(filepath.Join(s.dir, "heads")); historyErr == nil {
			return h, fmt.Errorf("recipe generation marker and head missing; history exists")
		} else if !os.IsNotExist(historyErr) {
			return h, historyErr
		}
		return h, nil
	}
	if markerErr != nil {
		return h, fmt.Errorf("recipe generation marker unavailable: %w", markerErr)
	}
	if !bytes.Equal(marker, s.markerBytes()) {
		return h, fmt.Errorf("recipe generation marker is damaged")
	}
	if os.IsNotExist(err) {
		return h, fmt.Errorf("initialized recipe generation is missing its head; recovery required")
	}
	if err != nil {
		return h, err
	}
	if err = json.Unmarshal(b, &h); err != nil {
		return h, err
	}
	if h.Schema != 1 || h.Instance != s.instance || h.Fingerprint != s.fingerprint || h.Revision == 0 || h.Root != "" && !recipeStoreHashValid(h.Root) || h.Checksum != recipeStoreHeadDigest(h) {
		return h, fmt.Errorf("invalid recipe store head")
	}
	history, err := recipeStoreReadBounded(filepath.Join(s.dir, "heads", recipeStoreHash(b)+".json"), recipeStoreHeadMaxBytes)
	if err != nil || !bytes.Equal(history, b) {
		return h, fmt.Errorf("recipe head history is missing or damaged")
	}
	return h, nil
}

func (s *recipeStore) markerBytes() []byte {
	b, _ := json.Marshal(struct {
		Schema      int    `json:"schema"`
		Instance    string `json:"instance"`
		Fingerprint string `json:"fingerprint"`
	}{1, s.instance, s.fingerprint})
	return b
}

// The marker is written before the empty head. Interruption between these
// steps requires explicit recovery; it never looks like fresh coverage.
func (s *recipeStore) initialize(h recipeStoreHead) (recipeStoreHead, error) {
	if h.Revision != 0 {
		return h, nil
	}
	if err := updateapply.CheckPath(filepath.Join(s.dir, "initialized.json")); err != nil {
		return h, err
	}
	if err := atomicWriteBytes(filepath.Join(s.dir, "initialized.json"), s.markerBytes()); err != nil {
		return h, err
	}
	if err := s.publishHead(h, ""); err != nil {
		return h, err
	}
	return s.head()
}
func recipeStoreValidateUnit(u recipeStoreUnit, fingerprint string) error {
	if u.Schema != 1 || u.Fingerprint != fingerprint || u.Height < 0 || !recipeStoreHashValid(u.BlockHash) || u.Rows == nil {
		return fmt.Errorf("invalid recipe block unit")
	}
	keys := make(map[string]bool, len(u.Rows))
	for _, row := range u.Rows {
		if row.Key == "" || len(row.Key) > 4096 || keys[row.Key] || !json.Valid(row.Value) {
			return fmt.Errorf("invalid or duplicate recipe row key or value")
		}
		keys[row.Key] = true
	}
	lineages := map[string]bool{}
	for _, lineage := range u.Lineage {
		if !recipeRegistryID.MatchString(lineage.Name) || lineages[lineage.Name] || !recipeRegistryID.MatchString(lineage.Instance) || !recipeStoreHashValid(lineage.SemanticHash) || lineage.Height != u.Height || lineage.BlockHash != u.BlockHash || !recipeStoreHashValid(lineage.Unit) {
			return fmt.Errorf("invalid recipe dependency lineage")
		}
		lineages[lineage.Name] = true
	}
	return nil
}
func (s *recipeStore) readUnit(ref recipeStoreRef, height int64) (recipeStoreUnit, error) {
	var u recipeStoreUnit
	if !recipeStoreHashValid(ref.Unit) || !recipeStoreHashValid(ref.BlockHash) {
		return u, fmt.Errorf("invalid recipe unit reference")
	}
	b, err := recipeStoreReadBounded(filepath.Join(s.dir, "units", ref.Unit+".json"), recipeStoreUnitMaxBytes)
	if err != nil {
		return u, err
	}
	if recipeStoreHash(b) != ref.Unit {
		return u, fmt.Errorf("recipe unit checksum mismatch")
	}
	if err = json.Unmarshal(b, &u); err != nil {
		return u, err
	}
	if err = recipeStoreValidateUnit(u, s.fingerprint); err != nil || u.Height != height || u.BlockHash != ref.BlockHash {
		return u, fmt.Errorf("recipe unit does not match its reference")
	}
	return u, nil
}

// Immutable content is published using a flushed temporary file and a hard
// link, which cannot replace an existing object. Existing bytes are verified;
// corruption is never repaired by overwriting it.
func recipeStorePutImmutable(dir, digest string, b []byte) error {
	if err := updateapply.CheckPath(filepath.Join(dir, digest+".json")); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, digest+".json")
	existing, err := recipeStoreReadBounded(path, recipeStoreUnitMaxBytes)
	if err == nil {
		if !bytes.Equal(existing, b) {
			return fmt.Errorf("existing recipe unit is corrupt")
		}
		return syncDirectory(dir)
	}
	if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".recipe-unit-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(tmp, path); err != nil {
		// Another writer may have installed this exact content; verify it.
		existing, readErr := recipeStoreReadBounded(path, recipeStoreUnitMaxBytes)
		if readErr != nil || !bytes.Equal(existing, b) {
			return fmt.Errorf("publish immutable recipe unit: %w", err)
		}
	}
	return syncDirectory(dir)
}
func recipeStoreGuarded(guard recipeStoreGuard, publish func() error) error {
	if guard == nil {
		return fmt.Errorf("recipe publication requires authority guard")
	}
	var state sync.Mutex
	called, closed := false, false
	var publishErr error
	err := guard(func() error {
		state.Lock()
		defer state.Unlock()
		if closed {
			return fmt.Errorf("recipe guard invoked publication after return")
		}
		if called {
			publishErr = fmt.Errorf("recipe guard invoked publication twice")
			return publishErr
		}
		called = true
		publishErr = publish()
		return publishErr
	})
	state.Lock()
	defer state.Unlock()
	closed = true
	if publishErr != nil {
		return publishErr
	}
	if err != nil {
		return err
	}
	if !called {
		return fmt.Errorf("recipe guard did not invoke publication")
	}
	return nil
}
func (s *recipeStore) tree(ctx context.Context, h recipeStoreHead) *ordinalTree {
	t := newOrdinalTree(filepath.Join(s.dir, "pages"), h.Root).withContext(ctx)
	t.checkPath = updateapply.CheckPath
	return t
}
func (s *recipeStore) publishHead(h recipeStoreHead, root string) error {
	if h.Revision == ^uint64(0) {
		return fmt.Errorf("recipe revision overflow")
	}
	h.Root = root
	h.Revision++
	h.Checksum = recipeStoreHeadDigest(h)
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if err = recipeStorePutImmutable(filepath.Join(s.dir, "heads"), recipeStoreHash(b), b); err != nil {
		return err
	}
	if err = updateapply.CheckPath(filepath.Join(s.dir, "head.json")); err != nil {
		return err
	}
	return atomicWriteBytes(filepath.Join(s.dir, "head.json"), b)
}

// Publish commits one sparse block unit. Rows must be an explicit (possibly
// empty) array and keys must be unique within the unit. Repeated publication
// is idempotent only for byte-identical units. An existing different unit at
// the same height requires an explicit reconciliation before replacement.
func (s *recipeStore) Publish(ctx context.Context, u recipeStoreUnit, guard recipeStoreGuard) error {
	return s.publish(ctx, u, nil, guard, nil)
}

// PublishAtRevision rejects any coverage change since the caller inspected it.
// Revision zero is a valid fence for a not-yet-initialized generation.
func (s *recipeStore) PublishAtRevision(ctx context.Context, u recipeStoreUnit, revision uint64, guard recipeStoreGuard) error {
	return s.publish(ctx, u, &revision, guard, nil)
}

func (s *recipeStore) publish(ctx context.Context, u recipeStoreUnit, expectedRevision *uint64, guard recipeStoreGuard, check func() error) error {
	if ctx == nil {
		return fmt.Errorf("missing context")
	}
	if err := recipeStoreValidateUnit(u, s.fingerprint); err != nil {
		return err
	}
	if len(u.Lineage) > 0 && check == nil {
		return fmt.Errorf("dependency lineage requires a guarded store transaction")
	}
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if len(b) > recipeStoreUnitMaxBytes {
		return fmt.Errorf("recipe unit exceeds budget")
	}
	digest := recipeStoreHash(b)
	return recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := s.rejectReconcileFenceLocked(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		h, err := s.head()
		if err != nil {
			return err
		}
		if expectedRevision != nil && h.Revision != *expectedRevision {
			return fmt.Errorf("recipe coverage changed since planning")
		}
		h, err = s.initialize(h)
		if err != nil {
			return err
		}
		if err = updateapply.CheckPath(filepath.Join(s.dir, "pages")); err != nil {
			return err
		}
		t := s.tree(ctx, h)
		key := recipeStoreHeightKey(u.Height)
		var previous recipeStoreRef
		found, err := t.get(key, &previous)
		if err != nil {
			return err
		}
		if found {
			if _, err := s.readUnit(previous, u.Height); err != nil {
				return err
			}
			if previous.BlockHash != u.BlockHash || previous.Unit != digest {
				return fmt.Errorf("recipe height already published; reconcile before replacement")
			}
			return nil
		}
		if err = recipeStorePutImmutable(filepath.Join(s.dir, "units"), digest, b); err != nil {
			return err
		}
		if err = t.put(key, recipeStoreRef{BlockHash: u.BlockHash, Unit: digest}); err != nil {
			return err
		}
		root, err := t.flush()
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return s.publishHead(h, root)
	})
}

// Read returns found=false for uncovered height, including after reconciliation.
// Callers must separately check the returned anchor against their held chain
// authority before presenting it as current evidence.
func (s *recipeStore) Read(ctx context.Context, height int64) (recipeStoreUnit, bool, error) {
	var u recipeStoreUnit
	if ctx == nil || height < 0 {
		return u, false, fmt.Errorf("invalid recipe read")
	}
	if err := ctx.Err(); err != nil {
		return u, false, err
	}
	recipeStoreMutex.Lock()
	defer recipeStoreMutex.Unlock()
	if err := s.rejectReconcileFenceLocked(); err != nil {
		return u, false, err
	}
	h, err := s.head()
	if err != nil {
		return u, false, err
	}
	if err = updateapply.CheckPath(filepath.Join(s.dir, "pages")); err != nil {
		return u, false, err
	}
	var ref recipeStoreRef
	found, err := s.tree(ctx, h).get(recipeStoreHeightKey(height), &ref)
	if err != nil || !found {
		return u, false, err
	}
	u, err = s.readUnit(ref, height)
	if err == nil && len(u.Lineage) != 0 {
		return recipeStoreUnit{}, false, fmt.Errorf("dependency lineage requires a verified read")
	}
	return u, err == nil, err
}

// Reconcile removes exactly the listed height/hash pairs from active coverage,
// atomically. It rejects stale or missing pairs and preserves all old units and
// pages. The guard must hold the chain/admission fence over the whole commit.
func (s *recipeStore) Reconcile(ctx context.Context, anchors map[int64]string, guard recipeStoreGuard) error {
	return s.reconcile(ctx, 0, anchors, guard, false)
}

// ReconcileAtRevision fences an enumeration/reorg plan against concurrent
// publication or reconciliation, including changes that later restore the
// same anchors. The caller must also hold/check its selected-chain epoch.
func (s *recipeStore) ReconcileAtRevision(ctx context.Context, revision uint64, anchors map[int64]string, guard recipeStoreGuard) error {
	if revision == 0 {
		return fmt.Errorf("missing recipe plan revision")
	}
	return s.reconcile(ctx, revision, anchors, guard, false)
}

func (s *recipeStore) reconcile(ctx context.Context, revision uint64, anchors map[int64]string, guard recipeStoreGuard, journal bool) error {
	if ctx == nil || len(anchors) == 0 || len(anchors) > 100000 {
		return fmt.Errorf("invalid recipe reconciliation")
	}
	for height, hash := range anchors {
		if height < 0 || !recipeStoreHashValid(hash) {
			return fmt.Errorf("invalid reconciliation anchor")
		}
	}
	return recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if journal {
			fence, err := s.readReconcileFenceLocked()
			if err != nil {
				return err
			}
			for height := range anchors {
				if height < fence.From || height > fence.To {
					return fmt.Errorf("reconciliation batch exceeds fenced range")
				}
			}
		} else if err := s.rejectReconcileFenceLocked(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := s.head()
		if err != nil {
			return err
		}
		if h.Revision == 0 {
			return fmt.Errorf("recipe generation is not initialized")
		}
		if revision != 0 && h.Revision != revision {
			return fmt.Errorf("recipe coverage changed since planning")
		}
		if err = updateapply.CheckPath(filepath.Join(s.dir, "pages")); err != nil {
			return err
		}
		t := s.tree(ctx, h)
		for height, hash := range anchors {
			var ref recipeStoreRef
			key := recipeStoreHeightKey(height)
			found, err := t.get(key, &ref)
			if err != nil {
				return err
			}
			if !found || ref.BlockHash != hash {
				return fmt.Errorf("recipe reconciliation anchor changed at %d", height)
			}
			if _, err = s.readUnit(ref, height); err != nil {
				return err
			}
			t.remove(key)
		}
		root, err := t.flush()
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return s.publishHead(h, root)
	})
}

// recipeCoveragePage enumerates committed sparse units in height order. A
// continuation must use the same revision; no selected-chain claim is made.
// Values are independently anchored receipts, not a contiguous frontier.
type recipeCoverageAnchor struct {
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
}
type recipeCoveragePage struct {
	Revision uint64                 `json:"revision"`
	Anchors  []recipeCoverageAnchor `json:"anchors"`
	Next     *int64                 `json:"next,omitempty"`
}

const recipeCoverageMaxPage = 256
const recipeCoverageMaxTreePages = 16384

// Coverage checks every returned unit's digest, extent and anchor. It prunes
// radix branches outside the requested interval and stops after one page, so
// work depends on the selected range, not the whole authenticated prefix.
// A successful empty page means only that this committed store snapshot has no
// units in this interval; callers still need selected-chain authority.
func (s *recipeStore) Coverage(ctx context.Context, from, to int64, after *int64, expectedRevision uint64, limit int) (recipeCoveragePage, error) {
	return s.coverage(ctx, from, to, after, expectedRevision, limit, false)
}
func (s *recipeStore) coverage(ctx context.Context, from, to int64, after *int64, expectedRevision uint64, limit int, journal bool) (recipeCoveragePage, error) {
	return s.coverageMode(ctx, from, to, after, expectedRevision, limit, journal, false)
}

// Raw dependent anchors are never evidence on their own; only the DAG
// verifier may consume this internal enumeration before atomic rechecking.
func (s *recipeStore) dependencyCoverageRefs(ctx context.Context, from, to int64, after *int64, expectedRevision uint64, limit int) (recipeCoveragePage, error) {
	return s.coverageMode(ctx, from, to, after, expectedRevision, limit, false, true)
}
func (s *recipeStore) coverageMode(ctx context.Context, from, to int64, after *int64, expectedRevision uint64, limit int, journal, allowLineage bool) (recipeCoveragePage, error) {
	return s.coverageSnapshotMode(ctx, from, to, after, expectedRevision, limit, journal, allowLineage, nil)
}

// A historical caller supplies an authenticated immutable published head and
// separately checks its full requested interval against current publication.
// Existing run/reconciliation paths retain their current-head revision fence.
func (s *recipeStore) coverageSnapshotMode(ctx context.Context, from, to int64, after *int64, expectedRevision uint64, limit int, journal, allowLineage bool, snapshot *recipeStoreHead) (recipeCoveragePage, error) {
	var result recipeCoveragePage
	if ctx == nil || from < 0 || to < from || limit < 1 || limit > recipeCoverageMaxPage || after != nil && (*after < from || *after > to || expectedRevision == 0) {
		return result, fmt.Errorf("invalid recipe coverage request")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	recipeStoreMutex.Lock()
	defer recipeStoreMutex.Unlock()
	if journal {
		fence, err := s.readReconcileFenceLocked()
		if err != nil {
			return result, err
		}
		if from < fence.From || to > fence.To {
			return result, fmt.Errorf("reconciliation coverage exceeds fenced range")
		}
	} else if err := s.rejectReconcileFenceLocked(); err != nil {
		return result, err
	}
	h, err := s.head()
	if err != nil {
		return result, err
	}
	if snapshot != nil {
		h = *snapshot
	}
	if expectedRevision != 0 && h.Revision != expectedRevision {
		return result, fmt.Errorf("recipe coverage changed since pagination began")
	}
	result.Revision = h.Revision
	result.Anchors = []recipeCoverageAnchor{}
	if after != nil && *after == to {
		return result, nil
	}
	first := from
	if after != nil {
		first = *after + 1
	}
	low, high := recipeStoreHeightKey(first), recipeStoreHeightKey(to)
	tree := s.tree(ctx, h)
	pages := 0
	var walk func(string, string) error
	walk = func(hash, prefix string) error {
		if hash == "" {
			return nil
		}
		if len(result.Anchors) > limit {
			return nil
		}
		if len(prefix) > 16 {
			return fmt.Errorf("recipe coverage tree depth is invalid")
		}
		lower := prefix + strings.Repeat("0", 16-len(prefix))
		upper := prefix + strings.Repeat("f", 16-len(prefix))
		if upper < low || lower > high {
			return nil
		}
		pages++
		if pages > recipeCoverageMaxTreePages {
			return fmt.Errorf("recipe coverage page read budget exceeded")
		}
		page, err := tree.read(hash)
		if err != nil {
			return err
		}
		if len(page.Children) > 0 {
			if len(prefix) == 16 {
				return fmt.Errorf("recipe coverage branch is too deep")
			}
			for i, child := range page.Children {
				if err := walk(child, prefix+"0123456789abcdef"[i:i+1]); err != nil {
					return err
				}
				if len(result.Anchors) > limit {
					break
				}
			}
			return nil
		}
		for _, kv := range page.Entries {
			if len(kv.Key) != 16 || !strings.HasPrefix(kv.Key, prefix) {
				return fmt.Errorf("invalid recipe coverage key")
			}
			if kv.Key < low || kv.Key > high {
				continue
			}
			n, err := strconv.ParseUint(kv.Key, 16, 64)
			if err != nil || n > uint64(^uint64(0)>>1) {
				return fmt.Errorf("invalid recipe coverage height")
			}
			var ref recipeStoreRef
			if err = json.Unmarshal(kv.Value, &ref); err != nil {
				return fmt.Errorf("invalid recipe coverage reference: %w", err)
			}
			unit, readErr := s.readUnit(ref, int64(n))
			if readErr != nil {
				return readErr
			}
			if len(unit.Lineage) != 0 && !allowLineage {
				return fmt.Errorf("dependency lineage requires verified coverage")
			}
			result.Anchors = append(result.Anchors, recipeCoverageAnchor{Height: int64(n), BlockHash: ref.BlockHash})
			if len(result.Anchors) > limit {
				break
			}
		}
		return nil
	}
	if err = walk(h.Root, ""); err != nil {
		return recipeCoveragePage{}, err
	}
	if len(result.Anchors) > limit {
		result.Anchors = result.Anchors[:limit]
		next := result.Anchors[len(result.Anchors)-1].Height
		result.Next = &next
	}
	return result, nil
}

// PlanReorg checks each enumerated saved anchor against caller-supplied
// selected-chain evidence. Any unavailable/invalid evidence aborts the entire
// page without returning a partial plan. This never mutates stored coverage.
// The caller must hold a stable selected-chain snapshot across all pages and
// pass Revision to ReconcileAtRevision under the corresponding commit guard.
func (s *recipeStore) PlanReorg(ctx context.Context, from, to int64, after *int64, expectedRevision uint64, limit int, selectedHash func(int64) (string, error)) (recipeCoveragePage, error) {
	if selectedHash == nil {
		return recipeCoveragePage{}, fmt.Errorf("selected-chain evidence is required")
	}
	page, err := s.Coverage(ctx, from, to, after, expectedRevision, limit)
	if err != nil {
		return recipeCoveragePage{}, err
	}
	affected := make([]recipeCoverageAnchor, 0, len(page.Anchors))
	for _, anchor := range page.Anchors {
		if err = ctx.Err(); err != nil {
			return recipeCoveragePage{}, err
		}
		selected, err := selectedHash(anchor.Height)
		if err != nil {
			return recipeCoveragePage{}, fmt.Errorf("selected-chain evidence unavailable at %d: %w", anchor.Height, err)
		}
		if !recipeStoreHashValid(selected) {
			return recipeCoveragePage{}, fmt.Errorf("selected-chain evidence invalid at %d", anchor.Height)
		}
		if selected != anchor.BlockHash {
			affected = append(affected, anchor)
		}
	}
	page.Anchors = affected
	return page, nil
}
