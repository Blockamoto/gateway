package main

// Parent-band target coverage reads immutable selected winners. It is distinct
// from the child tracks' consecutive reveal-block evaluation coverage.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const bitmapTargetMaxInteger uint64 = 1<<53 - 2

type bitmapTargetCoverageQuery struct {
	Axis     string  `json:"axis"`
	From     uint64  `json:"from"`
	To       uint64  `json:"to"`
	Pixels   int     `json:"pixels"`
	District *uint64 `json:"district,omitempty"`
	Snapshot string  `json:"snapshot,omitempty"`
}
type bitmapTargetCoverageBin struct {
	From    uint64 `json:"from"`
	To      uint64 `json:"to"`
	Targets uint64 `json:"targets"`
	Winners uint64 `json:"winners"`
	Unknown uint64 `json:"unknown"`
}
type bitmapTargetCoverageLimits struct {
	Winners       int   `json:"winners"`
	Pages         int   `json:"pages"`
	Bytes         int64 `json:"bytes"`
	Anchors       int   `json:"anchors"`
	WinnersPerBin int   `json:"winners_per_bin"`
}

var bitmapTargetDefaultLimits = bitmapTargetCoverageLimits{4096, 256, 16 << 20, 128, 4}
var errBitmapTargetBudget = errors.New("visible target evidence exceeds this request's bounded read budget")
var errBitmapTargetBinBudget = errors.New("visible pixel bin contains more selected winners than its sample budget")

type bitmapTargetCoverageView struct {
	State           string                     `json:"state"`
	Axis            string                     `json:"axis"`
	Instance        string                     `json:"instance"`
	From            uint64                     `json:"from"`
	To              uint64                     `json:"to"`
	Pixels          int                        `json:"pixels"`
	District        *uint64                    `json:"district,omitempty"`
	Bins            []bitmapTargetCoverageBin  `json:"bins"`
	Snapshot        *bitmapDevelopmentSnapshot `json:"snapshot"`
	Chain           chainAuthorityView         `json:"chain"`
	CacheKey        string                     `json:"cache_key"`
	CacheHit        bool                       `json:"cache_hit"`
	QueryComplete   bool                       `json:"query_complete"`
	Truncated       bool                       `json:"truncated"`
	ExaminedWinners int                        `json:"examined_winners"`
	ReadPages       int                        `json:"read_pages"`
	ReadBytes       int64                      `json:"read_bytes"`
	AnchorChecks    int                        `json:"anchor_checks"`
	Limits          bitmapTargetCoverageLimits `json:"limits"`
	Note            string                     `json:"note"`
}
type bitmapTargetReadBudget struct {
	ctx     context.Context
	limits  bitmapTargetCoverageLimits
	pages   int
	bytes   int64
	winners int
	anchors map[int64]string
	trees   map[string]*ordinalTree
}

func (b *bitmapTargetReadBudget) chargeFile(path string) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 {
		return fmt.Errorf("saved Bitmap target evidence has an invalid file extent")
	}
	if info.Size() > b.limits.Bytes-b.bytes {
		return errBitmapTargetBudget
	}
	b.bytes += info.Size()
	return nil
}
func (b *bitmapTargetReadBudget) tree(dir string) *ordinalTree {
	if b.trees[dir] == nil {
		b.trees[dir] = newOrdinalTree(dir, "").withContext(b.ctx)
	}
	return b.trees[dir]
}
func (b *bitmapTargetReadBudget) page(dir, hash string) (ordinalPage, error) {
	if hash == "" {
		return ordinalPage{}, nil
	}
	t := b.tree(dir)
	if p, ok := t.cache[hash]; ok {
		return p, b.ctx.Err()
	}
	if b.pages >= b.limits.Pages {
		return ordinalPage{}, errBitmapTargetBudget
	}
	if !validHash(hash) {
		return ordinalPage{}, fmt.Errorf("invalid Bitmap target page")
	}
	if err := b.chargeFile(t.pagePath(hash)); err != nil {
		return ordinalPage{}, err
	}
	b.pages++
	return t.read(hash)
}
func (b *bitmapTargetReadBudget) get(dir, root, key string, target any) (bool, error) {
	for hash, depth := root, 0; hash != ""; depth++ {
		p, err := b.page(dir, hash)
		if err != nil {
			return false, err
		}
		if len(p.Children) == 0 {
			i := sort.Search(len(p.Entries), func(i int) bool { return p.Entries[i].Key >= key })
			if i == len(p.Entries) || p.Entries[i].Key != key {
				return false, nil
			}
			return true, json.Unmarshal(p.Entries[i].Value, target)
		}
		if depth >= len(key) {
			return false, fmt.Errorf("Bitmap target key depth mismatch")
		}
		hash = p.Children[ordinalNibble(key, depth)]
	}
	return false, nil
}
func (b *bitmapTargetReadBudget) visible(dir, root, low, high string, visit func(ordinalKV) error) error {
	visited := 0
	var walk func(string, string) error
	walk = func(hash, prefix string) error {
		if hash == "" {
			return nil
		}
		if len(prefix) > 32 {
			return fmt.Errorf("Bitmap target range depth exceeded")
		}
		if prefix+strings.Repeat("f", 32-len(prefix)) < low || prefix+strings.Repeat("0", 32-len(prefix)) > high {
			return nil
		}
		p, err := b.page(dir, hash)
		if err != nil {
			return err
		}
		for _, kv := range p.Entries {
			if kv.Key < low || kv.Key > high {
				continue
			}
			if len(kv.Key) != 32 || !strings.HasPrefix(kv.Key, prefix) {
				return fmt.Errorf("Bitmap target range key mismatch")
			}
			if b.winners >= b.limits.Winners {
				return errBitmapTargetBudget
			}
			if visited >= b.limits.WinnersPerBin {
				return errBitmapTargetBinBudget
			}
			visited++
			b.winners++
			if err := visit(kv); err != nil {
				return err
			}
		}
		for i, child := range p.Children {
			if err := walk(child, prefix+"0123456789abcdef"[i:i+1]); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root, "")
}
func bitmapTargetBins(q bitmapTargetCoverageQuery) []bitmapTargetCoverageBin {
	span := q.To - q.From + 1
	count := min(uint64(q.Pixels), span)
	out := make([]bitmapTargetCoverageBin, int(count))
	for i := uint64(0); i < count; i++ {
		from, to := q.From+i*span/count, q.From+(i+1)*span/count-1
		out[i] = bitmapTargetCoverageBin{From: from, To: to, Targets: to - from + 1, Unknown: to - from + 1}
	}
	return out
}
func bitmapTargetValidateQuery(q bitmapTargetCoverageQuery) error {
	if q.Axis != "blocks" && q.Axis != "transactions" {
		return fmt.Errorf("axis must be blocks or transactions")
	}
	if q.From > q.To || q.To > bitmapTargetMaxInteger || q.Pixels < 1 || q.Pixels > 1024 {
		return fmt.Errorf("ordered visible range and pixels 1..1024 required")
	}
	if q.Axis == "transactions" && (q.District == nil || *q.District > bitmapTargetMaxInteger) || q.Axis == "blocks" && q.District != nil {
		return fmt.Errorf("district is required only for the transaction axis")
	}
	if q.Snapshot != "" && !validHash(q.Snapshot) {
		return fmt.Errorf("invalid expected Bitmap snapshot")
	}
	return nil
}

var bitmapTargetCache = struct {
	sync.Mutex
	rows  map[string]bitmapTargetCoverageView
	order []string
}{rows: map[string]bitmapTargetCoverageView{}}

func bitmapTargetClone(v bitmapTargetCoverageView) bitmapTargetCoverageView {
	v.Bins = append([]bitmapTargetCoverageBin(nil), v.Bins...)
	if v.Snapshot != nil {
		s := *v.Snapshot
		v.Snapshot = &s
	}
	if v.District != nil {
		d := *v.District
		v.District = &d
	}
	return v
}
func bitmapTargetCacheGet(key string) (bitmapTargetCoverageView, bool) {
	bitmapTargetCache.Lock()
	defer bitmapTargetCache.Unlock()
	v, ok := bitmapTargetCache.rows[key]
	if ok {
		v = bitmapTargetClone(v)
		v.CacheHit = true
		v.ExaminedWinners, v.ReadPages, v.ReadBytes, v.AnchorChecks = 0, 0, 0, 0
	}
	return v, ok
}
func bitmapTargetCachePut(v bitmapTargetCoverageView) {
	bitmapTargetCache.Lock()
	defer bitmapTargetCache.Unlock()
	if _, ok := bitmapTargetCache.rows[v.CacheKey]; ok {
		return
	}
	for len(bitmapTargetCache.order) >= 16 {
		delete(bitmapTargetCache.rows, bitmapTargetCache.order[0])
		bitmapTargetCache.order = bitmapTargetCache.order[1:]
	}
	bitmapTargetCache.rows[v.CacheKey] = bitmapTargetClone(v)
	bitmapTargetCache.order = append(bitmapTargetCache.order, v.CacheKey)
}
func bitmapTargetCurrentPointer(ctx context.Context, root, id string) (string, error) {
	var pointer struct {
		Commitment string `json:"commitment"`
	}
	if err := utxoReadJSON(ctx, filepath.Join(bitmapDevelopmentDir(root, id), "head.json"), 4096, &pointer); err != nil {
		return "", err
	}
	if !validHash(pointer.Commitment) {
		return "", fmt.Errorf("invalid Bitmap head pointer")
	}
	return pointer.Commitment, nil
}
func (a *app) bitmapTargetWinner(ctx context.Context, b *bitmapTargetReadBudget, dir string, h *bitmapDevelopmentHead, q bitmapTargetCoverageQuery, kv ordinalKV, hashAt func(int64) (string, error)) (uint64, error) {
	var c bitmapDevelopmentCandidate
	if err := json.Unmarshal(kv.Value, &c); err != nil {
		return 0, err
	}
	if c.District == nil || c.Selection != "selected" || c.Eligibility != "eligible" || c.RevealHeight < h.SegmentStart || c.RevealHeight > h.Height || !validHash(c.RevealHash) || !bitmapValidInscriptionBlocator(c.Blocator) || c.Blocator != inscriptionCoordinate(uint32(c.TxIndex), c.InscriptionIndex, c.RevealHeight) || c.TxIndex < 0 || uint64(c.TxIndex) > uint64(^uint32(0)) || c.ID != "" || kv.Key != bitmapTargetKey(*c.District, c.Transaction) {
		return 0, fmt.Errorf("Bitmap selected target evidence mismatch")
	}
	target := *c.District
	if q.Axis == "blocks" {
		if c.Kind != "district" || c.Transaction != nil {
			return 0, fmt.Errorf("District winner target mismatch")
		}
	} else {
		if c.Kind != "parcel" || c.Transaction == nil || *c.District != *q.District || c.Provenance == nil || c.Provenance.ChildBlocator != c.Blocator || c.Provenance.ChildBlockHash != c.RevealHash {
			return 0, fmt.Errorf("Parcel winner target/parent mismatch")
		}
		if !validHash(c.Provenance.EvidenceHash) {
			return 0, fmt.Errorf("Parcel parent receipt unavailable")
		}
		if err := b.chargeFile(bitmapParentReceiptPathV2(a.dataDir, c.Provenance.EvidenceHash)); err != nil {
			return 0, err
		}
		if err := verifyBitmapParentReceiptV2(ctx, a.dataDir, c.Provenance); err != nil {
			return 0, err
		}
		target = *c.Transaction
	}
	if target < q.From || target > q.To {
		return 0, fmt.Errorf("Bitmap winner outside visible range")
	}
	var outcome bitmapDevelopmentBlock
	have, err := b.get(dir, h.BlockRoot, fmt.Sprintf("%016x", c.RevealHeight), &outcome)
	if err != nil {
		return 0, err
	}
	if !have || outcome.Height != c.RevealHeight || outcome.BlockHash != c.RevealHash || !outcome.BlockEvaluated || !outcome.CompleteScope {
		return 0, fmt.Errorf("Bitmap winner lacks completed reveal outcome")
	}
	selected := false
	for _, value := range outcome.WinnerBlocators {
		if value == c.Blocator {
			selected = true
			break
		}
	}
	if !selected {
		return 0, fmt.Errorf("Bitmap winner absent from committed reveal outcome")
	}
	if saved, ok := b.anchors[c.RevealHeight]; ok {
		if saved != c.RevealHash {
			return 0, fmt.Errorf("Bitmap reveal source mismatch")
		}
	} else {
		if len(b.anchors) >= b.limits.Anchors {
			return 0, errBitmapTargetBudget
		}
		selected, err := hashAt(c.RevealHeight)
		if err != nil {
			return 0, err
		}
		if selected != c.RevealHash {
			return 0, fmt.Errorf("Bitmap reveal left selected chain")
		}
		b.anchors[c.RevealHeight] = selected
	}
	return target, nil
}
func (a *app) bitmapTargetCoverageContext(ctx context.Context, q bitmapTargetCoverageQuery) (bitmapTargetCoverageView, error) {
	return a.bitmapTargetCoverageWithLimits(ctx, q, bitmapTargetDefaultLimits)
}
func (a *app) bitmapTargetCoverageWithLimits(parent context.Context, q bitmapTargetCoverageQuery, limits bitmapTargetCoverageLimits) (bitmapTargetCoverageView, error) {
	out := bitmapTargetCoverageView{State: "not_built", Axis: q.Axis, From: q.From, To: q.To, Pixels: q.Pixels, District: q.District, Limits: limits, Note: "Only committed selected claims are marked. Every other target remains unknown; this view makes no absence or ownership claim."}
	if err := parent.Err(); err != nil {
		return out, err
	}
	if err := bitmapTargetValidateQuery(q); err != nil {
		return out, err
	}
	out.Bins = bitmapTargetBins(q)
	limits.Winners = min(limits.Winners, len(out.Bins)*limits.WinnersPerBin)
	limits.Pages = min(limits.Pages, 64+len(out.Bins)*2)
	limits.Anchors = min(limits.Anchors, len(out.Bins)*limits.WinnersPerBin)
	out.Limits = limits
	out.Instance = bitmapCanonicalDistrictID
	if q.Axis == "transactions" {
		out.Instance = bitmapCanonicalParcelID
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if !bitmapSnapshotMu.TryRLock() {
		out.State = "unavailable"
		out.Note = "Saved Bitmap pages are being maintained; targets remain unknown."
		return out, nil
	}
	defer bitmapSnapshotMu.RUnlock()
	h, err := readBitmapDevelopmentHead(ctx, a.dataDir, out.Instance)
	if err != nil {
		out.State = "unavailable"
		out.Note = "Saved Bitmap target evidence is unavailable; targets remain unknown."
		return out, nil
	}
	if h == nil {
		return out, nil
	}
	out.Snapshot = bitmapSnapshot(h)
	out.Chain = a.currentChainAuthority()
	if a.utxoSelectedChainState(ctx, &utxoCheckpoint{Height: h.Height, BlockHash: h.BlockHash}, out.Chain) != "selected_chain" {
		out.State = "stale"
		out.Note = "Saved Bitmap source is stale or cannot be anchored; targets remain unknown."
		return out, nil
	}
	if q.Snapshot != "" && q.Snapshot != h.Commitment {
		out.State = "changed"
		out.Note = "The current Bitmap snapshot changed; request its visible target range again."
		return out, nil
	}
	out.CacheKey = indexDigest(struct {
		Version                       int
		Root, Instance, Head, Profile string
		Chain                         chainAuthorityView
		Query                         bitmapTargetCoverageQuery
		Limits                        bitmapTargetCoverageLimits
	}{1, a.dataDir, out.Instance, h.Commitment, h.ProfileHash, out.Chain, q, limits})
	if cached, ok := bitmapTargetCacheGet(out.CacheKey); ok {
		current, err := bitmapTargetCurrentPointer(ctx, a.dataDir, out.Instance)
		chain := a.currentChainAuthority()
		if err == nil && current == h.Commitment && chain.Source == out.Chain.Source && chain.Height == out.Chain.Height && chain.Hash == out.Chain.Hash {
			return cached, nil
		}
		out.State, out.Note = "changed", "The Bitmap or selected-chain context changed; request the visible target range again."
		return out, nil
	}
	budget := &bitmapTargetReadBudget{ctx: ctx, limits: limits, anchors: map[int64]string{}, trees: map[string]*ordinalTree{}}
	dir := filepath.Join(bitmapDevelopmentDir(a.dataDir, out.Instance), "pages")
	hashAt := a.addressSelectedHashes(ctx, out.Chain)
	var queryErr error
	for i, bin := range out.Bins {
		var txFrom, txTo *uint64
		districtFrom, districtTo := bin.From, bin.To
		if q.Axis == "transactions" {
			districtFrom, districtTo = *q.District, *q.District
			txFrom, txTo = &bin.From, &bin.To
		}
		low, high := bitmapTargetKey(districtFrom, txFrom), bitmapTargetKey(districtTo, txTo)
		err = budget.visible(dir, h.WinnerRoot, low, high, func(kv ordinalKV) error {
			target, err := a.bitmapTargetWinner(ctx, budget, dir, h, q, kv, hashAt)
			if err != nil {
				return err
			}
			if target < bin.From || target > bin.To {
				return fmt.Errorf("Bitmap target pixel partition mismatch")
			}
			out.Bins[i].Winners++
			out.Bins[i].Unknown--
			return nil
		})
		if err != nil {
			queryErr = err
			if !errors.Is(err, errBitmapTargetBinBudget) {
				break
			}
		}
	}
	err = queryErr
	out.ExaminedWinners, out.ReadPages, out.ReadBytes, out.AnchorChecks = budget.winners, budget.pages, budget.bytes, len(budget.anchors)
	out.State = "ready"
	out.QueryComplete = err == nil
	if err != nil {
		out.State = "partial"
		out.Truncated = errors.Is(err, errBitmapTargetBudget) || errors.Is(err, errBitmapTargetBinBudget) || errors.Is(err, context.DeadlineExceeded)
		out.Note = "Only validated committed winners are marked. The bounded visible-range query could not inspect all target evidence; all other targets remain unknown."
	}
	finishCtx, finishCancel := context.WithTimeout(parent, 250*time.Millisecond)
	defer finishCancel()
	current, pointerErr := bitmapTargetCurrentPointer(finishCtx, a.dataDir, out.Instance)
	chain := a.currentChainAuthority()
	if pointerErr != nil || current != h.Commitment || chain.Source != out.Chain.Source || chain.Height != out.Chain.Height || chain.Hash != out.Chain.Hash {
		out.State = "changed"
		out.QueryComplete = false
		out.Bins = bitmapTargetBins(q)
		out.Note = "The Bitmap or selected-chain context changed during this query; targets remain unknown."
		return out, nil
	}
	if parent.Err() != nil {
		return out, parent.Err()
	}
	// Immutable complete summaries and deterministic quota truncations may be
	// reused. Missing/damaged pages, receipt I/O, anchor errors and deadlines
	// must be retried: repairing evidence need not change its head commitment.
	if err == nil || errors.Is(err, errBitmapTargetBudget) || errors.Is(err, errBitmapTargetBinBudget) {
		bitmapTargetCachePut(out)
	}
	return out, nil
}
func (a *app) handleBitmapTargetCoverage(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Bitmap target coverage is local read-only evidence"))
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, 405, fmt.Errorf("GET required"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	q := bitmapTargetCoverageQuery{Axis: r.URL.Query().Get("axis"), Pixels: 512, Snapshot: r.URL.Query().Get("snapshot")}
	if q.Axis == "" {
		q.Axis = "blocks"
	}
	for key, target := range map[string]*uint64{"from": &q.From, "to": &q.To} {
		n, err := bitmapQueryNumber(r, key)
		if err != nil || n == nil {
			jsonError(w, 400, fmt.Errorf("%s must be a visible unsigned integer", key))
			return
		}
		*target = *n
	}
	var err error
	q.District, err = bitmapQueryNumber(r, "district")
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	if raw := r.URL.Query().Get("pixels"); raw != "" {
		q.Pixels, err = strconv.Atoi(raw)
		if err != nil {
			jsonError(w, 400, err)
			return
		}
	}
	id := bitmapCanonicalDistrictID
	if q.Axis == "transactions" {
		id = bitmapCanonicalParcelID
	}
	if requested := r.URL.Query().Get("instance"); requested != "" && requested != id {
		jsonError(w, 400, fmt.Errorf("target coverage uses only the active Standard track for its axis"))
		return
	}
	out, err := a.bitmapTargetCoverageContext(r.Context(), q)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	writeJSON(w, out)
}
