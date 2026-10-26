// Package host provides an experimental shared entry point for two small
// interpreters. It does not authenticate blocks, persist state or claim parity.
package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	engine "gateway-index-experiment"
	state "gateway-state-experiment"
)

// Envelope gives both recipe kinds one definition entry point. Each embedded
// recipe has its own strict, versioned operation vocabulary.
type Envelope struct {
	Version int             `json:"version"`
	Kind    string          `json:"kind"`
	Recipe  json.RawMessage `json:"recipe"`
}

type Host struct {
	mu     sync.Mutex
	kind   string
	sparse engine.Definition
	state  *state.Machine
}

// Load constructs a single isolated index instance. undoLimit applies only to
// the stateful interpreter. Its anchors remain fixture-only caller assertions.
func Load(r io.Reader, undoLimit int) (*Host, error) {
	var e Envelope
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing envelope JSON")
	}
	if e.Version != 1 || len(e.Recipe) == 0 || bytes.Equal(e.Recipe, []byte("null")) {
		return nil, errors.New("invalid envelope")
	}
	h := &Host{kind: e.Kind}
	switch e.Kind {
	case "sparse":
		p, err := engine.DecodeDefinition(bytes.NewReader(e.Recipe))
		if err != nil {
			return nil, err
		}
		h.sparse = p
	case "state":
		p, err := state.ReadProgram(bytes.NewReader(e.Recipe))
		if err != nil {
			return nil, err
		}
		m, err := state.New(p, undoLimit)
		if err != nil {
			return nil, err
		}
		h.state = m
	default:
		return nil, fmt.Errorf("unsupported recipe kind %q", e.Kind)
	}
	return h, nil
}

// Request holds exactly one of the two explicit fixture inputs. There is no
// implicit fetch or prerequisite build; missing resolver evidence either
// remains unresolved (sparse) or prevents state publication (state).
type Request struct {
	SparseBlock *engine.Block
	StateBlock  *state.Block
	Resolver    engine.Resolver
}
type StateView struct {
	Head  *state.Head
	Coins map[state.Outpoint]state.Coin
}
type Result struct {
	Kind   string
	Sparse *engine.Result
	State  *StateView
}

func (h *Host) Execute(r Request) (Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch h.kind {
	case "sparse":
		if r.SparseBlock == nil || r.StateBlock != nil {
			return Result{}, errors.New("sparse request requires only a sparse block")
		}
		out, err := engine.Run(h.sparse, *r.SparseBlock, r.Resolver)
		if err != nil {
			return Result{}, err
		}
		return Result{Kind: h.kind, Sparse: &out}, nil
	case "state":
		if r.StateBlock == nil || r.SparseBlock != nil || r.Resolver != nil {
			return Result{}, errors.New("state request requires only a state block")
		}
		if err := h.state.Apply(*r.StateBlock); err != nil {
			return Result{}, err
		}
		head, coins := h.state.View()
		return Result{Kind: h.kind, State: &StateView{Head: head, Coins: coins}}, nil
	default:
		return Result{}, errors.New("uninitialized host")
	}
}

func (h *Host) Rollback(expectedHash string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.kind != "state" {
		return errors.New("rollback is only supported by state recipes")
	}
	return h.state.RollbackOne(expectedHash)
}
