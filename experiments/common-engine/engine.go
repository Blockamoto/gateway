// Package engine is a fixture-only experiment in a common operation interpreter.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"strconv"
	"strings"
	"sync"
)

type Recipe struct {
	Version    int         `json:"version"`
	ID         string      `json:"id"`
	State      string      `json:"state"`
	Operations []Operation `json:"operations"`
}
type Operation struct {
	Op     string   `json:"op"`
	Fields []string `json:"fields,omitempty"`
}

func ReadRecipe(r io.Reader) (Recipe, error) {
	var p Recipe
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, errors.New("trailing recipe JSON")
	}
	return p, p.Validate()
}
func (p Recipe) Validate() error {
	if p.Version != 1 || strings.TrimSpace(p.ID) == "" {
		return errors.New("invalid recipe identity")
	}
	var required []string
	switch p.State {
	case "none":
		if len(p.Operations) == 1 && p.Operations[0].Op == "project-inscription-fields" {
			required = []string{"project-inscription-fields"}
		} else {
			required = []string{"emit"}
		}
	case "utxo":
		required = []string{"consume-inputs", "create-outputs", "emit"}
	case "bitmap-districts":
		required = []string{"select-bitmap-district-winner"}
	default:
		return errors.New("unsupported state model")
	}
	if len(p.Operations) != len(required) {
		return errors.New("invalid operation sequence")
	}
	for i, op := range p.Operations {
		if op.Op != required[i] {
			return errors.New("invalid operation sequence")
		}
		if op.Op != "emit" {
			if len(op.Fields) != 0 {
				return errors.New("state operation cannot select fields")
			}
			continue
		}
		if len(op.Fields) == 0 {
			return errors.New("emit requires fields")
		}
		seen := map[string]bool{}
		for _, field := range op.Fields {
			if (field != "tx.id" && field != "tx.position") || seen[field] {
				return fmt.Errorf("unknown or duplicate field %q", field)
			}
			seen[field] = true
		}
	}
	return nil
}

// Callers must authenticate the block and all relevant context before use.
// Fixture anchors are not themselves evidence or proofs.
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
	// Reveals are in transaction-wide inscription-envelope order. Completeness
	// is required by the District recipe; false never means an empty scan.
	RevealsComplete bool
	Reveals         []Reveal
}
type Reveal struct {
	ContentType string
	Body        []byte
	// Complete ordered pushes from the 0.7.8 full-block extractor, including
	// the body marker and any dangling field tag. They are caller assertions.
	PayloadAvailable bool
	PayloadHex       []string
	ParserProfile    string
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
type Cell struct {
	Field string
	Value string
}
type Row struct{ Cells []Cell }
type Receipt struct {
	Height uint64
	Hash   string
	Rows   []Row
}

type Engine struct {
	mu         sync.Mutex
	recipe     Recipe
	recipeHash string
	instance   string
	backend    Backend
	fixture    *MemoryBackend
}

// New provides the original isolated fixture convenience mode. Its backend
// derives missing anchors from caller input; this does not authenticate blocks.
func New(p Recipe) (*Engine, error) {
	m := NewMemoryBackend()
	m.SetConsent(p.ID, true)
	e, err := NewWithBackend(p, m, p.ID)
	if err != nil {
		return nil, err
	}
	e.fixture = m
	return e, nil
}

// NewWithBackend uses independently managed chain anchors and consent state.
func NewWithBackend(p Recipe, backend Backend, instance string) (*Engine, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if backend == nil || strings.TrimSpace(instance) == "" {
		return nil, errors.New("backend and instance required")
	}
	operations := make([]Operation, len(p.Operations))
	for i, op := range p.Operations {
		operations[i] = Operation{Op: op.Op, Fields: append([]string(nil), op.Fields...)}
	}
	p.Operations = operations
	encoded, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return &Engine{recipe: p, recipeHash: fmt.Sprintf("%x", digest), instance: instance, backend: backend}, nil
}

type View struct {
	Head     *Head
	Coins    map[Outpoint]Coin
	Receipts map[uint64]Receipt
}

func cloneHead(h *Head) *Head {
	if h == nil {
		return nil
	}
	v := *h
	return &v
}
func cloneCoins(coins map[Outpoint]Coin) map[Outpoint]Coin {
	c := make(map[Outpoint]Coin, len(coins))
	for k, v := range coins {
		c[k] = v
	}
	return c
}
func cloneReceipt(r Receipt) Receipt {
	c := Receipt{Height: r.Height, Hash: r.Hash, Rows: make([]Row, len(r.Rows))}
	for i, row := range r.Rows {
		c.Rows[i].Cells = append([]Cell(nil), row.Cells...)
	}
	return c
}
func (e *Engine) View() (View, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.backend.View(e.instance, e.recipeHash)
	if err != nil {
		return View{}, err
	}
	if e.recipe.State == "bitmap-districts" {
		if _, err := priorDistrictWinners(v); err != nil {
			return View{}, err
		}
	}
	return v, nil
}

// Apply evaluates all operations against tentative state in physical block and
// transaction order. It atomically publishes state and the singleton receipt
// only on full success. An absent earlier receipt is not inferred coverage.
func (e *Engine) Apply(b Block) (Receipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if b.Hash == "" {
		return Receipt{}, errors.New("missing block anchor")
	}
	if len(b.Transactions) == 0 {
		return Receipt{}, errors.New("complete block requires at least one transaction")
	}
	published := false
	if e.fixture != nil {
		epoch, installed := e.fixture.setFixtureAnchorIfAbsent(b.Height, b.Hash)
		if installed {
			defer func() {
				if !published {
					e.fixture.clearFixtureAnchorIfUnchanged(b.Height, b.Hash, epoch)
				}
			}()
		}
	}
	snapshot, err := e.backend.Read(e.instance, e.recipeHash, b.Height, b.Hash)
	if err != nil {
		return Receipt{}, err
	}
	if old, ok := snapshot.View.Receipts[b.Height]; ok {
		if old.Hash != b.Hash {
			return Receipt{}, errors.New("height anchor conflict; explicit reorg handling required")
		}
		return Receipt{}, errors.New("block already committed")
	}
	if e.recipe.State == "utxo" || e.recipe.State == "bitmap-districts" {
		if snapshot.View.Head == nil {
			if e.recipe.State == "utxo" && (b.Height != 0 || b.ParentHash != "") {
				return Receipt{}, errors.New("genesis required")
			}
			if e.recipe.State == "bitmap-districts" && (b.Height != bitmapDistrictOrigin || b.ParentHash == "") {
				return Receipt{}, errors.New("District scan requires its configured origin and parent")
			}
		} else if snapshot.View.Head.Height == math.MaxUint64 || b.Height != snapshot.View.Head.Height+1 || b.ParentHash != snapshot.View.Head.Hash {
			return Receipt{}, errors.New("block is not exact next anchored successor")
		}
	}
	next := cloneCoins(snapshot.View.Coins)
	receipt := Receipt{Height: b.Height, Hash: b.Hash, Rows: []Row{}}
	winners := map[string]bool{}
	if e.recipe.State == "bitmap-districts" {
		var err error
		winners, err = priorDistrictWinners(snapshot.View)
		if err != nil {
			return Receipt{}, err
		}
	}
	seenTX := map[string]bool{}
	seenInputs := map[Outpoint]bool{}
	var inscriptionBudget inscriptionBlockBudget
	for ti, tx := range b.Transactions {
		if tx.ID == "" || seenTX[tx.ID] {
			return Receipt{}, errors.New("empty or duplicate transaction identity")
		}
		seenTX[tx.ID] = true
		for _, operation := range e.recipe.Operations {
			switch operation.Op {
			case "project-inscription-fields":
				if !tx.RevealsComplete {
					return Receipt{}, errors.New("incomplete inscription envelope extraction")
				}
				for ri, reveal := range tx.Reveals {
					if err := inscriptionBudget.add(reveal); err != nil {
						return Receipt{}, err
					}
					row, err := projectInscriptionFields(reveal, ti, ri, b.Height)
					if err != nil {
						return Receipt{}, err
					}
					receipt.Rows = append(receipt.Rows, row)
				}
			case "select-bitmap-district-winner":
				if !tx.RevealsComplete {
					return Receipt{}, errors.New("incomplete inscription envelope extraction")
				}
				for ri, reveal := range tx.Reveals {
					district, ok := qualifyingDistrict(reveal, b.Height)
					if !ok || winners[district] {
						continue
					}
					winners[district] = true
					receipt.Rows = append(receipt.Rows, Row{Cells: []Cell{
						{Field: "district", Value: district},
						{Field: "inscription.blocator", Value: fmt.Sprintf("%di%d.%d", ti, ri, b.Height)},
					}})
				}
			case "consume-inputs":
				if tx.Coinbase && (ti != 0 || len(tx.Inputs) != 0) {
					return Receipt{}, errors.New("invalid fixture coinbase")
				}
				if !tx.Coinbase && len(tx.Inputs) == 0 {
					return Receipt{}, errors.New("non-coinbase requires inputs")
				}
				for _, in := range tx.Inputs {
					if in.TxID == "" {
						return Receipt{}, errors.New("empty outpoint")
					}
					if seenInputs[in] {
						return Receipt{}, errors.New("duplicate input")
					}
					seenInputs[in] = true
					if _, ok := next[in]; !ok {
						return Receipt{}, fmt.Errorf("unresolved previous output %s:%d", in.TxID, in.Index)
					}
					delete(next, in)
				}
			case "create-outputs":
				for oi, coin := range tx.Outputs {
					if uint64(oi) > uint64(^uint32(0)) {
						return Receipt{}, errors.New("too many outputs")
					}
					out := Outpoint{TxID: tx.ID, Index: uint32(oi)}
					if _, exists := next[out]; exists {
						return Receipt{}, errors.New("live output collision")
					}
					next[out] = coin
				}
			case "emit":
				row := Row{Cells: make([]Cell, 0, len(operation.Fields))}
				for _, field := range operation.Fields {
					value := ""
					switch field {
					case "tx.id":
						value = tx.ID
					case "tx.position":
						value = fmt.Sprintf("%d.%d", ti, b.Height)
					}
					row.Cells = append(row.Cells, Cell{Field: field, Value: value})
				}
				receipt.Rows = append(receipt.Rows, row)
			}
		}
	}
	// No tentative rows, state or coverage escape before this boundary.
	candidate := cloneView(snapshot.View)
	candidate.Coins = next
	if e.recipe.State == "utxo" || e.recipe.State == "bitmap-districts" {
		candidate.Head = &Head{Height: b.Height, Hash: b.Hash}
	}
	candidate.Receipts[b.Height] = receipt
	if err := e.backend.Commit(Commit{Instance: e.instance, RecipeHash: e.recipeHash, Height: b.Height, Hash: b.Hash, Fence: snapshot.Fence, Candidate: candidate}); err != nil {
		return Receipt{}, err
	}
	published = true
	return cloneReceipt(receipt), nil
}

// projectInscriptionFields consumes ordered pushes from the existing 0.7.8
// extractor's occurrence payload. It does not parse witness or authenticate
// those pushes. Parent tag bytes are declarations, never relationship proof.
const inscriptionOccurrenceLimit = 100000
const inscriptionItemLimit = 250000
const inscriptionPayloadByteLimit = 4000000

// These are upper bounds on the projected subset, not authentication of the
// source witness or a substitute for its full-block extraction budget.
type inscriptionBlockBudget struct{ occurrences, items, bytes int }

func (b *inscriptionBlockBudget) add(reveal Reveal) error {
	if b.occurrences >= inscriptionOccurrenceLimit || len(reveal.PayloadHex) > inscriptionItemLimit-b.items {
		return errors.New("inscription block occurrence or item budget exceeded")
	}
	bytes := b.bytes
	for _, raw := range reveal.PayloadHex {
		if len(raw)%2 != 0 || len(raw)/2 > inscriptionPayloadByteLimit-bytes {
			return errors.New("inscription block payload budget exceeded or invalid hex length")
		}
		bytes += len(raw) / 2
	}
	b.occurrences++
	b.items += len(reveal.PayloadHex)
	b.bytes = bytes
	return nil
}

func projectInscriptionFields(reveal Reveal, txIndex, revealIndex int, height uint64) (Row, error) {
	if !reveal.PayloadAvailable || reveal.ParserProfile != "gateway-inscription-occurrence-v1" {
		return Row{}, errors.New("unresolved inscription payload evidence")
	}
	if len(reveal.PayloadHex) > inscriptionItemLimit {
		return Row{}, errors.New("inscription payload item budget exceeded")
	}
	decoded := make([][]byte, len(reveal.PayloadHex))
	var total int
	for i, raw := range reveal.PayloadHex {
		if len(raw)%2 != 0 || len(raw)/2 > inscriptionPayloadByteLimit-total || strings.ToLower(raw) != raw {
			return Row{}, errors.New("invalid inscription payload encoding or budget")
		}
		b, err := hex.DecodeString(raw)
		if err != nil {
			return Row{}, errors.New("invalid inscription payload hex")
		}
		total += len(b)
		decoded[i] = b
	}
	fields := map[string][]string{}
	incomplete := false
	pendingTag := ""
	for i := 0; i < len(decoded); i += 2 {
		if len(decoded[i]) == 0 {
			break // remaining pushes are body, never tag/value fields
		}
		if i+1 == len(decoded) {
			incomplete = true
			pendingTag = reveal.PayloadHex[i]
			break
		}
		tag := reveal.PayloadHex[i]
		fields[tag] = append(fields[tag], reveal.PayloadHex[i+1])
	}
	fieldsJSON, _ := json.Marshal(fields)
	meta := fields["05"]
	metaState := "absent"
	if len(meta) != 0 {
		metaState = "present"
	}
	if pendingTag == "05" && len(meta) == 0 {
		metaState = "incomplete"
	}
	metaprotocolState := "absent"
	if len(fields["07"]) != 0 {
		metaprotocolState = "present"
	}
	if pendingTag == "07" && len(fields["07"]) == 0 {
		metaprotocolState = "incomplete"
	}
	metaprotocolJSON, _ := json.Marshal(nonNilStrings(fields["07"]))
	parents := fields["03"]
	parentsJSON, _ := json.Marshal(nonNilStrings(parents))
	parentState := "not-declared"
	if len(parents) != 0 {
		parentState = "declared-unresolved"
	}
	if pendingTag == "03" && len(parents) == 0 {
		parentState = "declared-incomplete"
	}
	payloadJSON, _ := json.Marshal(nonNilStrings(reveal.PayloadHex))
	return Row{Cells: []Cell{
		{Field: "inscription.blocator", Value: fmt.Sprintf("%di%d.%d", txIndex, revealIndex, height)},
		{Field: "parser.profile", Value: reveal.ParserProfile},
		{Field: "payload.pushes.hex.json", Value: string(payloadJSON)},
		{Field: "fields.hex.json", Value: string(fieldsJSON)},
		{Field: "metadata.state", Value: metaState},
		{Field: "metadata.hex", Value: strings.Join(meta, "")},
		{Field: "metaprotocol.state", Value: metaprotocolState},
		{Field: "metaprotocol.values.hex.json", Value: string(metaprotocolJSON)},
		{Field: "parent.declarations.hex.json", Value: string(parentsJSON)},
		{Field: "parent.relationship.state", Value: parentState},
		{Field: "field.incomplete", Value: strconv.FormatBool(incomplete)},
		{Field: "field.dangling.tag.hex", Value: pendingTag},
	}}, nil
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// bitmapDistrictOrigin is the current Gateway 0.7.8 start policy, not an
// upstream Bitmap rule. A real implementation must authenticate coverage.
const bitmapDistrictOrigin uint64 = 792435

func qualifyingDistrict(reveal Reveal, height uint64) (string, bool) {
	media, _, err := mime.ParseMediaType(reveal.ContentType)
	if err != nil || !strings.EqualFold(media, "text/plain") {
		return "", false
	}
	body := string(reveal.Body)
	if !strings.HasSuffix(body, ".bitmap") {
		return "", false
	}
	digits := strings.TrimSuffix(body, ".bitmap")
	if len(digits) == 0 || (len(digits) > 1 && digits[0] == '0') {
		return "", false
	}
	for _, c := range []byte(digits) {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	value, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || value > height {
		return "", false
	}
	return digits, true
}

// This scans every committed receipt to reconstruct first winners. It is a
// correctness experiment, not a scalable storage design. Reject incomplete
// prefix state rather than treating a missing receipt as an empty block.
func priorDistrictWinners(view View) (map[string]bool, error) {
	winners := make(map[string]bool)
	if view.Head == nil {
		if len(view.Receipts) != 0 {
			return nil, errors.New("District receipts without head")
		}
		return winners, nil
	}
	if view.Head.Height < bitmapDistrictOrigin || uint64(len(view.Receipts)) != view.Head.Height-bitmapDistrictOrigin+1 {
		return nil, errors.New("incomplete District prefix")
	}
	for height := bitmapDistrictOrigin; ; height++ {
		r, ok := view.Receipts[height]
		if !ok || r.Height != height || r.Hash == "" || (height == view.Head.Height && r.Hash != view.Head.Hash) {
			return nil, errors.New("incomplete District receipt")
		}
		var lastTX, lastReveal uint64
		haveLast := false
		for _, row := range r.Rows {
			if len(row.Cells) != 2 || row.Cells[0].Field != "district" || row.Cells[1].Field != "inscription.blocator" || row.Cells[1].Value == "" || winners[row.Cells[0].Value] {
				return nil, errors.New("invalid District winner state")
			}
			if _, ok := qualifyingDistrict(Reveal{ContentType: "text/plain", Body: []byte(row.Cells[0].Value + ".bitmap")}, height); !ok {
				return nil, errors.New("invalid District winner state")
			}
			tx, reveal, ok := districtWinnerPosition(row.Cells[1].Value, height)
			if !ok || (haveLast && (tx < lastTX || (tx == lastTX && reveal <= lastReveal))) {
				return nil, errors.New("invalid District winner position or order")
			}
			lastTX, lastReveal, haveLast = tx, reveal, true
			winners[row.Cells[0].Value] = true
		}
		if height == view.Head.Height {
			break
		}
	}
	return winners, nil
}

// This validates only the coordinate's syntax and receipt height. Proving the
// referenced envelope exists and qualified requires retained source evidence.
func districtWinnerPosition(coordinate string, height uint64) (uint64, uint64, bool) {
	stem, block, ok := strings.Cut(coordinate, ".")
	if !ok || block != strconv.FormatUint(height, 10) {
		return 0, 0, false
	}
	tx, reveal, ok := strings.Cut(stem, "i")
	if !ok || !canonicalPositionDecimal(tx) || !canonicalPositionDecimal(reveal) {
		return 0, 0, false
	}
	txNumber, err := strconv.ParseUint(tx, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	revealNumber, err := strconv.ParseUint(reveal, 10, 64)
	return txNumber, revealNumber, err == nil
}

func canonicalPositionDecimal(s string) bool {
	if len(s) == 0 || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
