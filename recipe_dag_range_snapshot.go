package main

// Stateless receipt-range snapshots reuse existing immutable published heads.
// They grant no chain/run/registry authority. The selected-chain host must
// provide that fence when consuming a snapshot and its per-page lineage.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const recipeDAGRangeSnapshotPrefix = "gateway-receipt-range-v1."
const recipeDAGRangeSnapshotMaxTokenBytes = 256 << 10

type recipeDAGRangePin struct {
	Identity recipeGraphIdentity `json:"identity"`
	Head     string              `json:"head,omitempty"`
}
type recipeDAGRangeSnapshot struct {
	Headers string              `json:"headers,omitempty"`
	Schema  int                 `json:"schema"`
	Profile string              `json:"profile"`
	Root    recipeGraphIdentity `json:"root"`
	From    int64               `json:"from"`
	To      int64               `json:"to"`
	Pins    []recipeDAGRangePin `json:"pins"`
}

func (snapshot recipeDAGRangeSnapshot) token() (string, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	token := recipeDAGRangeSnapshotPrefix + base64.RawURLEncoding.EncodeToString(data)
	if len(token) > recipeDAGRangeSnapshotMaxTokenBytes {
		return "", fmt.Errorf("receipt range snapshot exceeds graph metadata bound")
	}
	return token, nil
}

func recipeParseDAGRangeSnapshot(token string) (recipeDAGRangeSnapshot, error) {
	var snapshot recipeDAGRangeSnapshot
	if len(token) > recipeDAGRangeSnapshotMaxTokenBytes || !strings.HasPrefix(token, recipeDAGRangeSnapshotPrefix) {
		return snapshot, fmt.Errorf("invalid receipt range snapshot token")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(token, recipeDAGRangeSnapshotPrefix))
	if err != nil {
		return snapshot, err
	}
	if err = recipeValidateJSON(data); err != nil {
		return snapshot, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&snapshot); err != nil {
		return snapshot, err
	}
	canonical, err := snapshot.token()
	if err != nil || canonical != token {
		return snapshot, fmt.Errorf("noncanonical receipt range snapshot")
	}
	return snapshot, nil
}

// Caller holds recipeStoreMutex. Only tiny published-head identities are
// retained; no unit rows or per-height list is placed in the token or on disk.
func (p *recipeLineagePlan) captureRangeSnapshotLocked(ctx context.Context, from, to int64) (recipeDAGRangeSnapshot, error) {
	var snapshot recipeDAGRangeSnapshot
	if p == nil || ctx == nil || from < 0 || to < from || len(p.nodes) < 1 || len(p.nodes) > recipeGraphMaxNodes {
		return snapshot, fmt.Errorf("invalid receipt range snapshot request")
	}
	root, ok := p.nodes[p.root]
	if !ok {
		return snapshot, fmt.Errorf("receipt range root unavailable")
	}
	snapshot = recipeDAGRangeSnapshot{Schema: 1, Profile: indexDigest(root.store.dir), Root: p.root, From: from, To: to, Pins: []recipeDAGRangePin{}}
	ids := make([]recipeGraphIdentity, 0, len(p.nodes))
	for id := range p.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if a.Instance != b.Instance {
			return a.Instance < b.Instance
		}
		if a.Revision != b.Revision {
			return a.Revision < b.Revision
		}
		return a.SemanticHash < b.SemanticHash
	})
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return recipeDAGRangeSnapshot{}, err
		}
		s := p.nodes[id].store
		if err := s.rejectReconcileFenceLocked(); err != nil {
			return recipeDAGRangeSnapshot{}, err
		}
		head, err := s.head()
		if err != nil {
			return recipeDAGRangeSnapshot{}, err
		}
		pin := recipeDAGRangePin{Identity: id}
		if head.Revision != 0 {
			data, err := json.Marshal(head)
			if err != nil {
				return recipeDAGRangeSnapshot{}, err
			}
			pin.Head = recipeStoreHash(data)
		}
		snapshot.Pins = append(snapshot.Pins, pin)
	}
	return snapshot, nil
}

func (s *recipeStore) rangeSnapshotHead(pin recipeDAGRangePin) (recipeStoreHead, error) {
	h := recipeStoreHead{Schema: 1, Instance: s.instance, Fingerprint: s.fingerprint}
	if pin.Head == "" {
		return h, nil
	}
	if !recipeStoreHashValid(pin.Head) {
		return h, fmt.Errorf("invalid receipt range published-head identity")
	}
	data, err := recipeStoreReadBounded(filepath.Join(s.dir, "heads", pin.Head+".json"), recipeStoreHeadMaxBytes)
	if err != nil {
		return h, err
	}
	if recipeStoreHash(data) != pin.Head || json.Unmarshal(data, &h) != nil || h.Schema != 1 || h.Instance != s.instance || h.Fingerprint != s.fingerprint || h.Revision == 0 || h.Root != "" && !recipeStoreHashValid(h.Root) || h.Checksum != recipeStoreHeadDigest(h) {
		return h, fmt.Errorf("receipt range published head is damaged")
	}
	return h, nil
}

// Caller holds recipeStoreMutex and separately supplies its chain/registry
// fence. Every ancestor's complete requested interval is compared, including
// heights returned on prior pages. Appends outside that interval remain valid.
func (p *recipeLineagePlan) checkRangeSnapshotLocked(ctx context.Context, snapshot recipeDAGRangeSnapshot, from, to int64) (map[recipeGraphIdentity]recipeStoreHead, error) {
	if ctx == nil || p == nil || snapshot.Schema != 1 || snapshot.Root != p.root || snapshot.From != from || snapshot.To != to || from < 0 || to < from || len(snapshot.Pins) != len(p.nodes) {
		return nil, fmt.Errorf("receipt range snapshot scope differs")
	}
	root, ok := p.nodes[p.root]
	if !ok || snapshot.Profile != indexDigest(root.store.dir) {
		return nil, fmt.Errorf("receipt range snapshot belongs to a different profile")
	}
	heads := make(map[recipeGraphIdentity]recipeStoreHead, len(snapshot.Pins))
	for _, pin := range snapshot.Pins {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		node, ok := p.nodes[pin.Identity]
		if _, duplicate := heads[pin.Identity]; !ok || duplicate {
			return nil, fmt.Errorf("receipt range snapshot graph differs")
		}
		if err := node.store.rejectReconcileFenceLocked(); err != nil {
			return nil, err
		}
		prior, err := node.store.rangeSnapshotHead(pin)
		if err != nil {
			return nil, err
		}
		current, err := node.store.head()
		if err != nil {
			return nil, err
		}
		equal, err := recipeStoreRangeEqual(ctx, node.store, prior.Root, current.Root, from, to)
		if err != nil {
			return nil, err
		}
		if !equal {
			return nil, fmt.Errorf("receipt range changed for %s", pin.Identity.Instance)
		}
		heads[pin.Identity] = prior
	}
	return heads, nil
}
