package engine

import (
	"errors"
	"sync"
)

// Backend must read one coherent instance snapshot and atomically publish the
// complete candidate only after comparing the instance revision, selected-chain
// epoch/anchors and consent epoch in the same transaction. Implementations must
// copy data at the boundary or otherwise prevent later caller mutation. A real
// backend must return read/commit errors and define crash durability separately.
type Backend interface {
	Read(instance, recipeHash string, height uint64, hash string) (Snapshot, error)
	View(instance, recipeHash string) (View, error)
	Commit(Commit) error
}

type Fence struct{ Revision, ChainEpoch, ConsentEpoch uint64 }
type Snapshot struct {
	View  View
	Fence Fence
}
type Commit struct {
	Instance, RecipeHash string
	Height               uint64
	Hash                 string
	Fence                Fence
	Candidate            View
}

type memoryInstance struct {
	recipeHash string
	revision   uint64
	view       View
}
type consent struct {
	epoch    uint64
	disabled bool
}

// MemoryBackend is a non-durable reference for transaction semantics.
type MemoryBackend struct {
	mu         sync.Mutex
	chainEpoch uint64
	anchors    map[uint64]string
	instances  map[string]memoryInstance
	consents   map[string]consent
	failNext   bool
	beforeNext func()
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{anchors: map[uint64]string{}, instances: map[string]memoryInstance{}, consents: map[string]consent{}}
}

// SetAnchor is a fixture chain-authority update, not Bitcoin authentication.
// Any change advances a conservative epoch for in-flight writers.
func (m *MemoryBackend) SetAnchor(height uint64, hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.anchors[height] != hash {
		m.anchors[height] = hash
		m.chainEpoch++
	}
}
func (m *MemoryBackend) setFixtureAnchorIfAbsent(height uint64, hash string) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.anchors[height]; !ok {
		m.anchors[height] = hash
		m.chainEpoch++
		return m.chainEpoch, true
	}
	return 0, false
}

// Remove only the exact provisional anchor installed by a failed isolated
// fixture run. A concurrent authority change always wins over cleanup.
func (m *MemoryBackend) clearFixtureAnchorIfUnchanged(height uint64, hash string, epoch uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chainEpoch == epoch && m.anchors[height] == hash {
		delete(m.anchors, height)
		m.chainEpoch++
	}
}
func (m *MemoryBackend) SetConsent(instance string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.consent(instance)
	if c.disabled == enabled {
		c.disabled = !enabled
		c.epoch++
		m.consents[instance] = c
	}
}
func (m *MemoryBackend) consent(instance string) consent {
	c, ok := m.consents[instance]
	if !ok {
		return consent{disabled: true}
	}
	return c
}

// FailNextCommit fails after fencing but before any publication.
func (m *MemoryBackend) FailNextCommit() { m.mu.Lock(); defer m.mu.Unlock(); m.failNext = true }

// BeforeNextCommit injects a test event before the transaction lock is taken.
func (m *MemoryBackend) BeforeNextCommit(f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beforeNext = f
}

func emptyView() View { return View{Coins: map[Outpoint]Coin{}, Receipts: map[uint64]Receipt{}} }
func cloneView(v View) View {
	c := View{Head: cloneHead(v.Head), Coins: cloneCoins(v.Coins), Receipts: make(map[uint64]Receipt, len(v.Receipts))}
	for h, r := range v.Receipts {
		c.Receipts[h] = cloneReceipt(r)
	}
	return c
}
func (m *MemoryBackend) instance(instance, hash string) (memoryInstance, error) {
	i, ok := m.instances[instance]
	if !ok {
		return memoryInstance{recipeHash: hash, view: emptyView()}, nil
	}
	if i.recipeHash != hash {
		return memoryInstance{}, errors.New("instance recipe mismatch")
	}
	return i, nil
}

// The prototype has no reorg reconciliation or stale-coverage flag. Do not
// present retained receipts as selected-chain coverage after an anchor changes.
// This linear check is intentionally a correctness baseline, not a design for
// a production-sized index.
func (m *MemoryBackend) checkCurrent(v View) error {
	if v.Head != nil && m.anchors[v.Head.Height] != v.Head.Hash {
		return errors.New("saved head is no longer on selected chain")
	}
	for height, receipt := range v.Receipts {
		if m.anchors[height] != receipt.Hash {
			return errors.New("saved coverage is no longer on selected chain")
		}
	}
	return nil
}
func (m *MemoryBackend) View(instance, recipeHash string) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.instance(instance, recipeHash)
	if err != nil {
		return View{}, err
	}
	if err := m.checkCurrent(i.view); err != nil {
		return View{}, err
	}
	return cloneView(i.view), nil
}
func (m *MemoryBackend) Read(instance, recipeHash string, height uint64, hash string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.instance(instance, recipeHash)
	if err != nil {
		return Snapshot{}, err
	}
	c := m.consent(instance)
	if c.disabled {
		return Snapshot{}, errors.New("consumer disabled")
	}
	if hash == "" || m.anchors[height] != hash {
		return Snapshot{}, errors.New("block is not anchored to selected chain")
	}
	if err := m.checkCurrent(i.view); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{View: cloneView(i.view), Fence: Fence{Revision: i.revision, ChainEpoch: m.chainEpoch, ConsentEpoch: c.epoch}}, nil
}
func (m *MemoryBackend) Commit(c Commit) error {
	m.mu.Lock()
	hook := m.beforeNext
	m.beforeNext = nil
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.instance(c.Instance, c.RecipeHash)
	if err != nil {
		return err
	}
	consent := m.consent(c.Instance)
	if consent.disabled || consent.epoch != c.Fence.ConsentEpoch {
		return errors.New("stale consent fence")
	}
	if m.chainEpoch != c.Fence.ChainEpoch || m.anchors[c.Height] != c.Hash {
		return errors.New("stale selected-chain fence")
	}
	if err := m.checkCurrent(i.view); err != nil {
		return err
	}
	if i.revision != c.Fence.Revision {
		return errors.New("stale instance revision")
	}
	r, ok := c.Candidate.Receipts[c.Height]
	if !ok || r.Hash != c.Hash || r.Height != c.Height {
		return errors.New("candidate lacks matching receipt")
	}
	if m.failNext {
		m.failNext = false
		return errors.New("injected transaction failure")
	}
	m.instances[c.Instance] = memoryInstance{recipeHash: c.RecipeHash, revision: i.revision + 1, view: cloneView(c.Candidate)}
	return nil
}
