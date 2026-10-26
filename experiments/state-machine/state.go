// Package state demonstrates a tiny declarative state machine, not Bitcoin validation.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
)

// Program is a deliberately constrained executable instruction sequence.
// Input consumption must precede output creation within each transaction.
type Program struct {
	Version int      `json:"version"`
	ID      string   `json:"id"`
	Scope   string   `json:"scope"`
	Ops     []string `json:"ops"`
}

func ReadProgram(r io.Reader) (Program, error) {
	var p Program
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, errors.New("trailing JSON")
	}
	return p, p.Validate()
}

func (p Program) Validate() error {
	if p.Version != 1 || strings.TrimSpace(p.ID) == "" || p.Scope != "transactions-in-block-order" {
		return errors.New("invalid program identity or scope")
	}
	if len(p.Ops) != 2 || p.Ops[0] != "consume_inputs" || p.Ops[1] != "create_outputs" {
		return errors.New("program must consume inputs before creating outputs")
	}
	return nil
}

type Outpoint struct {
	TxID  string
	Index uint32
}
type Coin struct {
	Value  uint64
	Script string
}
type Transaction struct {
	ID       string
	Coinbase bool
	Inputs   []Outpoint
	Outputs  []Coin
}
type Block struct {
	Height       uint64
	Hash         string
	ParentHash   string
	Transactions []Transaction
}
type Head struct {
	Height uint64
	Hash   string
}

// Machine requires its caller to authenticate all blocks and selected-chain
// membership. It only checks predecessor hashes and fixture invariants.
// Each successful transition keeps a complete prior copy for bounded undo;
// this is deliberately not a scalable storage design.
type Machine struct {
	mu        sync.Mutex
	program   Program
	head      *Head
	coins     map[Outpoint]Coin
	undo      []snapshot
	undoLimit int
}
type snapshot struct {
	head  *Head
	coins map[Outpoint]Coin
}

func New(p Program, undoLimit int) (*Machine, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if undoLimit < 0 {
		return nil, errors.New("negative undo limit")
	}
	p.Ops = append([]string(nil), p.Ops...)
	return &Machine{program: p, coins: map[Outpoint]Coin{}, undoLimit: undoLimit}, nil
}

func cloneHead(h *Head) *Head {
	if h == nil {
		return nil
	}
	copy := *h
	return &copy
}
func cloneCoins(coins map[Outpoint]Coin) map[Outpoint]Coin {
	copy := make(map[Outpoint]Coin, len(coins))
	for k, v := range coins {
		copy[k] = v
	}
	return copy
}

// View returns detached copies so callers cannot mutate machine state.
func (m *Machine) View() (*Head, map[Outpoint]Coin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneHead(m.head), cloneCoins(m.coins)
}

func (m *Machine) Apply(b Block) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.Hash == "" {
		return errors.New("missing block hash")
	}
	if m.head == nil {
		if b.Height != 0 || b.ParentHash != "" {
			return errors.New("genesis required")
		}
	} else if m.head.Height == math.MaxUint64 || b.Height != m.head.Height+1 || b.ParentHash != m.head.Hash {
		return errors.New("block is not the exact next anchored successor")
	}
	next := cloneCoins(m.coins)
	seenTransactions := map[string]bool{}
	consumed := map[Outpoint]bool{}
	for txIndex, tx := range b.Transactions {
		if tx.ID == "" || seenTransactions[tx.ID] {
			return errors.New("empty or duplicate transaction identity")
		}
		seenTransactions[tx.ID] = true
		if tx.Coinbase && (len(tx.Inputs) != 0 || txIndex != 0) {
			return errors.New("fixture coinbase must be first and have no inputs")
		}
		if !tx.Coinbase && len(tx.Inputs) == 0 {
			return errors.New("non-coinbase transaction requires inputs")
		}
		for _, op := range m.program.Ops {
			switch op {
			case "consume_inputs":
				for _, in := range tx.Inputs {
					if in.TxID == "" {
						return errors.New("empty input outpoint")
					}
					if consumed[in] {
						return fmt.Errorf("duplicate input %s:%d", in.TxID, in.Index)
					}
					consumed[in] = true
					if _, ok := next[in]; !ok {
						return fmt.Errorf("unresolved previous output %s:%d", in.TxID, in.Index)
					}
					delete(next, in)
				}
			case "create_outputs":
				for i, coin := range tx.Outputs {
					if uint64(i) > uint64(^uint32(0)) {
						return errors.New("too many outputs")
					}
					out := Outpoint{TxID: tx.ID, Index: uint32(i)}
					if _, exists := next[out]; exists {
						return fmt.Errorf("live output collision %s:%d", tx.ID, i)
					}
					next[out] = coin
				}
			}
		}
	}
	if m.undoLimit > 0 {
		m.undo = append(m.undo, snapshot{head: cloneHead(m.head), coins: m.coins})
		if len(m.undo) > m.undoLimit {
			m.undo = m.undo[len(m.undo)-m.undoLimit:]
		}
	}
	m.coins = next
	m.head = &Head{Height: b.Height, Hash: b.Hash}
	return nil
}

// RollbackOne only removes the named current head. Insufficient retained undo
// is explicit and leaves state untouched. Apply can then accept a replacement
// child, once the caller establishes its selected-chain evidence.
func (m *Machine) RollbackOne(expectedHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.head == nil || expectedHash == "" || m.head.Hash != expectedHash {
		return errors.New("rollback head mismatch")
	}
	if len(m.undo) == 0 {
		return errors.New("undo not retained; rebuild required")
	}
	previous := m.undo[len(m.undo)-1]
	m.undo = m.undo[:len(m.undo)-1]
	m.head, m.coins = previous.head, previous.coins
	return nil
}
