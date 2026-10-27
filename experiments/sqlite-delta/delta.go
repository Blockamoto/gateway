// Package delta is an experimental incremental SQLite index backend.
package delta

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"sync"

	engine "gateway-common-engine-experiment"
	_ "modernc.org/sqlite"
)

// Delta is the proposed producer-to-store contract. It names only changed
// coins and one complete anchored receipt. Fence values must come from Read.
// A nil Head is valid only for a sparse consumer without a prior head; a
// non-nil Head advances it. The producer must validate index semantics/inputs.
type Delta struct {
	Instance, RecipeHash string
	Fence                engine.Fence
	Receipt              engine.Receipt
	Head                 *engine.Head
	ParentHash           string
	Upsert               map[engine.Outpoint]engine.Coin
	Delete               []engine.Outpoint
}
type WriteStats struct{ Upserts, Deletes, Receipts int }
type Preparation struct {
	Fence engine.Fence
	Head  *engine.Head
}
type Store struct {
	db           *sql.DB
	mu           sync.Mutex
	failNext     bool
	beforeNext   func()
	afterChanges func()
}

var _ engine.Backend = (*Store)(nil)

// Explicitly reject changes to supported persisted shapes until serialization
// and migrations are reviewed; never silently drop a newly added View field.
func fields(t reflect.Type, names ...string) bool {
	if t.Kind() != reflect.Struct || t.NumField() != len(names) {
		return false
	}
	for i, name := range names {
		// JSON tags affect the persisted rows_json representation. A future
		// tag must be explicitly reviewed and versioned with this schema.
		if t.Field(i).Name != name || !t.Field(i).IsExported() || t.Field(i).Tag != "" {
			return false
		}
	}
	return true
}
func supportedSchema() bool {
	check := func(t reflect.Type, names []string, types ...reflect.Type) bool {
		if !fields(t, names...) || len(types) != len(names) {
			return false
		}
		for i, kind := range types {
			if t.Field(i).Type != kind {
				return false
			}
		}
		return true
	}
	str := reflect.TypeOf("")
	u64 := reflect.TypeOf(uint64(0))
	u32 := reflect.TypeOf(uint32(0))
	return check(reflect.TypeOf(engine.View{}), []string{"Head", "Coins", "Receipts"}, reflect.TypeOf((*engine.Head)(nil)), reflect.TypeOf(map[engine.Outpoint]engine.Coin{}), reflect.TypeOf(map[uint64]engine.Receipt{})) &&
		check(reflect.TypeOf(engine.Head{}), []string{"Height", "Hash"}, u64, str) &&
		check(reflect.TypeOf(engine.Outpoint{}), []string{"TxID", "Index"}, str, u32) &&
		check(reflect.TypeOf(engine.Coin{}), []string{"Value", "Script"}, u64, str) &&
		check(reflect.TypeOf(engine.Receipt{}), []string{"Height", "Hash", "Rows"}, u64, str, reflect.TypeOf([]engine.Row(nil))) &&
		check(reflect.TypeOf(engine.Row{}), []string{"Cells"}, reflect.TypeOf([]engine.Cell(nil))) &&
		check(reflect.TypeOf(engine.Cell{}), []string{"Field", "Value"}, str, str)
}
func Open(path string) (*Store, error) {
	if !supportedSchema() {
		return nil, errors.New("unsupported engine View schema")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS meta (id INTEGER PRIMARY KEY CHECK(id=1), schema_id TEXT NOT NULL, epoch INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS anchors (height INTEGER PRIMARY KEY, hash TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS consents (instance TEXT PRIMARY KEY, enabled INTEGER NOT NULL, epoch INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS instances (instance TEXT PRIMARY KEY, recipe_hash TEXT NOT NULL, revision INTEGER NOT NULL, head_height INTEGER, head_hash TEXT)`,
		`CREATE TABLE IF NOT EXISTS coins (instance TEXT NOT NULL, txid TEXT NOT NULL, out_index INTEGER NOT NULL, value TEXT NOT NULL, script TEXT NOT NULL, PRIMARY KEY(instance,txid,out_index))`,
		`CREATE TABLE IF NOT EXISTS receipts (instance TEXT NOT NULL, height INTEGER NOT NULL, hash TEXT NOT NULL, rows_json BLOB NOT NULL, PRIMARY KEY(instance,height))`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO meta(id,schema_id,epoch) VALUES(1,'sqlite-delta-v1',0)`); err != nil {
		return nil, err
	}
	var schema string
	if err = tx.QueryRow(`SELECT schema_id FROM meta WHERE id=1`).Scan(&schema); err != nil {
		return nil, err
	}
	if schema != "sqlite-delta-v1" {
		return nil, errors.New("unsupported stored schema")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	ok = true
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func num(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, errors.New("height or revision overflow")
	}
	return int64(v), nil
}
func lockWriter(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE meta SET epoch=epoch WHERE id=1`)
	return err
}
func epoch(tx *sql.Tx) (uint64, error) {
	var n int64
	err := tx.QueryRow(`SELECT epoch FROM meta WHERE id=1`).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("invalid epoch")
	}
	return uint64(n), nil
}
func anchor(tx *sql.Tx, height uint64) (string, error) {
	h, err := num(height)
	if err != nil {
		return "", err
	}
	var x string
	err = tx.QueryRow(`SELECT hash FROM anchors WHERE height=?`, h).Scan(&x)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return x, err
}
func consent(tx *sql.Tx, id string) (bool, uint64, error) {
	var enabled, n int64
	err := tx.QueryRow(`SELECT enabled,epoch FROM consents WHERE instance=?`, id).Scan(&enabled, &n)
	if err == sql.ErrNoRows {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if (enabled != 0 && enabled != 1) || n < 0 {
		return false, 0, errors.New("invalid consent")
	}
	return enabled == 1, uint64(n), nil
}

type info struct {
	revision uint64
	head     *engine.Head
}

func instance(tx *sql.Tx, id, hash string) (info, error) {
	var saved string
	var rev int64
	var h sql.NullInt64
	var hh sql.NullString
	err := tx.QueryRow(`SELECT recipe_hash,revision,head_height,head_hash FROM instances WHERE instance=?`, id).Scan(&saved, &rev, &h, &hh)
	if err == sql.ErrNoRows {
		return info{}, nil
	}
	if err != nil {
		return info{}, err
	}
	if saved != hash {
		return info{}, errors.New("instance recipe mismatch")
	}
	if rev < 0 || h.Valid != hh.Valid || (h.Valid && (h.Int64 < 0 || hh.String == "")) {
		return info{}, errors.New("invalid instance state")
	}
	x := info{revision: uint64(rev)}
	if h.Valid {
		x.head = &engine.Head{Height: uint64(h.Int64), Hash: hh.String}
	}
	return x, nil
}
func current(tx *sql.Tx, id string, x info) error {
	if x.head != nil {
		h, err := anchor(tx, x.head.Height)
		if err != nil {
			return err
		}
		if h != x.head.Hash {
			return errors.New("saved head is no longer on selected chain")
		}
	}
	var stale int64
	err := tx.QueryRow(`SELECT COUNT(*) FROM receipts r LEFT JOIN anchors a ON a.height=r.height WHERE r.instance=? AND (a.hash IS NULL OR a.hash<>r.hash)`, id).Scan(&stale)
	if err != nil {
		return err
	}
	if stale != 0 {
		return errors.New("saved coverage is no longer on selected chain")
	}
	return nil
}
func loadView(tx *sql.Tx, id string, x info) (engine.View, error) {
	v := engine.View{Head: x.head, Coins: map[engine.Outpoint]engine.Coin{}, Receipts: map[uint64]engine.Receipt{}}
	rows, err := tx.Query(`SELECT txid,out_index,value,script FROM coins WHERE instance=?`, id)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var txid, value, script string
		var index int64
		if err = rows.Scan(&txid, &index, &value, &script); err != nil {
			rows.Close()
			return v, err
		}
		n, e := strconv.ParseUint(value, 10, 64)
		if e != nil || txid == "" || index < 0 || index > math.MaxUint32 {
			rows.Close()
			return v, errors.New("invalid coin row")
		}
		v.Coins[engine.Outpoint{TxID: txid, Index: uint32(index)}] = engine.Coin{Value: n, Script: script}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return v, err
	}
	if err = rows.Close(); err != nil {
		return v, err
	}
	rows, err = tx.Query(`SELECT height,hash,rows_json FROM receipts WHERE instance=?`, id)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var h int64
		var hash string
		var blob []byte
		if err = rows.Scan(&h, &hash, &blob); err != nil {
			rows.Close()
			return v, err
		}
		if h < 0 || hash == "" {
			rows.Close()
			return v, errors.New("invalid receipt row")
		}
		var entries []engine.Row
		dec := json.NewDecoder(bytes.NewReader(blob))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&entries); err != nil {
			rows.Close()
			return v, err
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			rows.Close()
			return v, errors.New("invalid receipt encoding")
		}
		v.Receipts[uint64(h)] = engine.Receipt{Height: uint64(h), Hash: hash, Rows: entries}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return v, err
	}
	if err = rows.Close(); err != nil {
		return v, err
	}
	return v, nil
}
func (s *Store) View(id, hash string) (engine.View, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return engine.View{}, err
	}
	defer tx.Rollback()
	x, err := instance(tx, id, hash)
	if err != nil {
		return engine.View{}, err
	}
	if err = current(tx, id, x); err != nil {
		return engine.View{}, err
	}
	v, err := loadView(tx, id, x)
	if err != nil {
		return engine.View{}, err
	}
	if err = tx.Commit(); err != nil {
		return engine.View{}, err
	}
	return v, nil
}
func (s *Store) Read(id, recipe string, height uint64, hash string) (engine.Snapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return engine.Snapshot{}, err
	}
	defer tx.Rollback()
	x, err := instance(tx, id, recipe)
	if err != nil {
		return engine.Snapshot{}, err
	}
	on, ce, err := consent(tx, id)
	if err != nil {
		return engine.Snapshot{}, err
	}
	if !on {
		return engine.Snapshot{}, errors.New("consumer disabled")
	}
	a, err := anchor(tx, height)
	if err != nil {
		return engine.Snapshot{}, err
	}
	if hash == "" || a != hash {
		return engine.Snapshot{}, errors.New("block is not anchored to selected chain")
	}
	if err = current(tx, id, x); err != nil {
		return engine.Snapshot{}, err
	}
	chain, err := epoch(tx)
	if err != nil {
		return engine.Snapshot{}, err
	}
	v, err := loadView(tx, id, x)
	if err != nil {
		return engine.Snapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return engine.Snapshot{}, err
	}
	return engine.Snapshot{View: v, Fence: engine.Fence{Revision: x.revision, ChainEpoch: chain, ConsentEpoch: ce}}, nil
}

// PrepareDelta captures the publication fence and current head without loading
// all coin rows or receipts into Go memory. It still checks saved coverage in
// SQL, which is intentionally a correctness-first scan in this experiment.
func (s *Store) PrepareDelta(id, recipe string, height uint64, hash string) (Preparation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Preparation{}, err
	}
	defer tx.Rollback()
	x, err := instance(tx, id, recipe)
	if err != nil {
		return Preparation{}, err
	}
	on, ce, err := consent(tx, id)
	if err != nil {
		return Preparation{}, err
	}
	if !on {
		return Preparation{}, errors.New("consumer disabled")
	}
	a, err := anchor(tx, height)
	if err != nil {
		return Preparation{}, err
	}
	if hash == "" || a != hash {
		return Preparation{}, errors.New("block is not anchored to selected chain")
	}
	if err = current(tx, id, x); err != nil {
		return Preparation{}, err
	}
	chain, err := epoch(tx)
	if err != nil {
		return Preparation{}, err
	}
	if err = tx.Commit(); err != nil {
		return Preparation{}, err
	}
	return Preparation{Fence: engine.Fence{Revision: x.revision, ChainEpoch: chain, ConsentEpoch: ce}, Head: x.head}, nil
}

// LookupCoins reads only requested outpoints at the prepared revision. Missing
// keys are returned explicitly; no state or coverage is created by a lookup.
func (s *Store) LookupCoins(id, recipe string, fence engine.Fence, keys []engine.Outpoint) (map[engine.Outpoint]engine.Coin, []engine.Outpoint, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	x, err := instance(tx, id, recipe)
	if err != nil {
		return nil, nil, err
	}
	on, ce, err := consent(tx, id)
	if err != nil {
		return nil, nil, err
	}
	chain, err := epoch(tx)
	if err != nil {
		return nil, nil, err
	}
	if !on || ce != fence.ConsentEpoch {
		return nil, nil, errors.New("stale consent fence")
	}
	if chain != fence.ChainEpoch {
		return nil, nil, errors.New("stale selected-chain fence")
	}
	if x.revision != fence.Revision {
		return nil, nil, errors.New("stale instance revision")
	}
	if err = current(tx, id, x); err != nil {
		return nil, nil, err
	}
	found := map[engine.Outpoint]engine.Coin{}
	missing := []engine.Outpoint{}
	seen := map[engine.Outpoint]bool{}
	for _, key := range keys {
		if key.TxID == "" || seen[key] {
			return nil, nil, errors.New("empty or duplicate lookup identity")
		}
		seen[key] = true
		var value, script string
		err = tx.QueryRow(`SELECT value,script FROM coins WHERE instance=? AND txid=? AND out_index=?`, id, key.TxID, int64(key.Index)).Scan(&value, &script)
		if err == sql.ErrNoRows {
			missing = append(missing, key)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		n, e := strconv.ParseUint(value, 10, 64)
		if e != nil {
			return nil, nil, errors.New("invalid coin value")
		}
		found[key] = engine.Coin{Value: n, Script: script}
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return found, missing, nil
}

func (s *Store) SetAnchor(height uint64, hash string) error {
	h, err := num(height)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockWriter(tx); err != nil {
		return err
	}
	old, err := anchor(tx, height)
	if err != nil {
		return err
	}
	if old != hash {
		ep, e := epoch(tx)
		if e != nil {
			return e
		}
		if ep >= math.MaxInt64 {
			return errors.New("chain epoch overflow")
		}
		if hash == "" {
			_, err = tx.Exec(`DELETE FROM anchors WHERE height=?`, h)
		} else {
			_, err = tx.Exec(`INSERT INTO anchors(height,hash) VALUES(?,?) ON CONFLICT(height) DO UPDATE SET hash=excluded.hash`, h, hash)
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE meta SET epoch=epoch+1 WHERE id=1`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) SetConsent(id string, enabled bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockWriter(tx); err != nil {
		return err
	}
	old, ep, err := consent(tx, id)
	if err != nil {
		return err
	}
	if old != enabled {
		if ep >= math.MaxInt64 {
			return errors.New("consent epoch overflow")
		}
		v := 0
		if enabled {
			v = 1
		}
		if _, err = tx.Exec(`INSERT INTO consents(instance,enabled,epoch) VALUES(?,?,?) ON CONFLICT(instance) DO UPDATE SET enabled=excluded.enabled,epoch=excluded.epoch`, id, v, int64(ep+1)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) BeforeNextCommit(f func()) { s.mu.Lock(); defer s.mu.Unlock(); s.beforeNext = f }
func (s *Store) FailNextCommit()           { s.mu.Lock(); defer s.mu.Unlock(); s.failNext = true }

// AfterChangesBeforeReceipt is a test-only process-termination hook. It runs
// under an open SQL transaction and must not call this Store.
func (s *Store) AfterChangesBeforeReceipt(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterChanges = f
}

func less(a, b engine.Outpoint) bool {
	if a.TxID != b.TxID {
		return a.TxID < b.TxID
	}
	return a.Index < b.Index
}

// CommitDelta fences and publishes only the named changed coin rows and one
// receipt. A failure after SQL mutations rolls the entire transaction back.
func (s *Store) CommitDelta(d Delta) (WriteStats, error) {
	s.mu.Lock()
	hook := s.beforeNext
	s.beforeNext = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	zero := WriteStats{}
	if d.Instance == "" || d.RecipeHash == "" || d.Receipt.Hash == "" {
		return zero, errors.New("missing delta identity")
	}
	h, err := num(d.Receipt.Height)
	if err != nil {
		return zero, err
	}
	deletes := append([]engine.Outpoint(nil), d.Delete...)
	sort.Slice(deletes, func(i, j int) bool { return less(deletes[i], deletes[j]) })
	for i, key := range deletes {
		if key.TxID == "" || (i > 0 && key == deletes[i-1]) {
			return zero, errors.New("invalid duplicate deletion")
		}
		if _, ok := d.Upsert[key]; ok {
			return zero, errors.New("coin cannot be deleted and upserted")
		}
	}
	upserts := make([]engine.Outpoint, 0, len(d.Upsert))
	for key := range d.Upsert {
		if key.TxID == "" {
			return zero, errors.New("empty coin identity")
		}
		upserts = append(upserts, key)
	}
	sort.Slice(upserts, func(i, j int) bool { return less(upserts[i], upserts[j]) })
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	if err = lockWriter(tx); err != nil {
		return zero, err
	}
	x, err := instance(tx, d.Instance, d.RecipeHash)
	if err != nil {
		return zero, err
	}
	on, ce, err := consent(tx, d.Instance)
	if err != nil {
		return zero, err
	}
	if !on || ce != d.Fence.ConsentEpoch {
		return zero, errors.New("stale consent fence")
	}
	ep, err := epoch(tx)
	if err != nil {
		return zero, err
	}
	a, err := anchor(tx, d.Receipt.Height)
	if err != nil {
		return zero, err
	}
	if ep != d.Fence.ChainEpoch || a != d.Receipt.Hash {
		return zero, errors.New("stale selected-chain fence")
	}
	if err = current(tx, d.Instance, x); err != nil {
		return zero, err
	}
	if x.revision != d.Fence.Revision {
		return zero, errors.New("stale instance revision")
	}
	if x.revision >= math.MaxInt64 {
		return zero, errors.New("revision overflow")
	}
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM receipts WHERE instance=? AND height=?`, d.Instance, h).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		return zero, err
	}
	if err == nil {
		return zero, errors.New("block already committed")
	}
	if d.Head != nil {
		if d.Head.Height != d.Receipt.Height || d.Head.Hash != d.Receipt.Hash {
			return zero, errors.New("head and receipt mismatch")
		}
		if x.head != nil && (x.head.Height == math.MaxUint64 || d.Head.Height != x.head.Height+1 || d.ParentHash != x.head.Hash) {
			return zero, errors.New("head is not exact next successor")
		}
	} else if x.head != nil {
		return zero, errors.New("stateful head cannot be cleared")
	}
	var headHeight, headHash any
	if d.Head != nil {
		headHeight = h
		headHash = d.Head.Hash
	}
	_, err = tx.Exec(`INSERT INTO instances(instance,recipe_hash,revision,head_height,head_hash) VALUES(?,?,?,?,?) ON CONFLICT(instance) DO UPDATE SET revision=excluded.revision,head_height=excluded.head_height,head_hash=excluded.head_hash`, d.Instance, d.RecipeHash, int64(x.revision+1), headHeight, headHash)
	if err != nil {
		return zero, err
	}
	stats := WriteStats{}
	for _, key := range deletes {
		r, e := tx.Exec(`DELETE FROM coins WHERE instance=? AND txid=? AND out_index=?`, d.Instance, key.TxID, int64(key.Index))
		if e != nil {
			return zero, e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return zero, e
		}
		if n != 1 {
			return zero, errors.New("delete missing coin")
		}
		stats.Deletes++
	}
	for _, key := range upserts {
		coin := d.Upsert[key]
		r, e := tx.Exec(`INSERT INTO coins(instance,txid,out_index,value,script) VALUES(?,?,?,?,?) ON CONFLICT(instance,txid,out_index) DO UPDATE SET value=excluded.value,script=excluded.script WHERE coins.value<>excluded.value OR coins.script<>excluded.script`, d.Instance, key.TxID, int64(key.Index), strconv.FormatUint(coin.Value, 10), coin.Script)
		if e != nil {
			return zero, e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return zero, e
		}
		stats.Upserts += int(n)
	}
	s.mu.Lock()
	after := s.afterChanges
	s.afterChanges = nil
	s.mu.Unlock()
	if after != nil {
		after()
	}
	s.mu.Lock()
	fail := s.failNext
	s.failNext = false
	s.mu.Unlock()
	if fail {
		return zero, errors.New("injected transaction failure")
	}
	blob, err := json.Marshal(d.Receipt.Rows)
	if err != nil {
		return zero, err
	}
	if _, err = tx.Exec(`INSERT INTO receipts(instance,height,hash,rows_json) VALUES(?,?,?,?)`, d.Instance, h, d.Receipt.Hash, blob); err != nil {
		return zero, err
	}
	stats.Receipts = 1
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return stats, nil
}

func sameReceipt(a, b engine.Receipt) bool {
	if a.Height != b.Height || a.Hash != b.Hash || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Rows {
		if len(a.Rows[i].Cells) != len(b.Rows[i].Cells) {
			return false
		}
		for j := range a.Rows[i].Cells {
			if a.Rows[i].Cells[j] != b.Rows[i].Cells[j] {
				return false
			}
		}
	}
	return true
}

// Commit adapts the legacy full-View interface by loading and comparing the
// entire snapshot. It is deliberately not advertised as an incremental
// producer; only its resulting SQL mutations are incremental.
func (s *Store) Commit(c engine.Commit) error {
	snapshot, err := s.Read(c.Instance, c.RecipeHash, c.Height, c.Hash)
	if err != nil {
		return err
	}
	if snapshot.Fence != c.Fence {
		return errors.New("stale commit fence")
	}
	if c.Candidate.Coins == nil || c.Candidate.Receipts == nil {
		return errors.New("incomplete candidate View")
	}
	r, ok := c.Candidate.Receipts[c.Height]
	if !ok || r.Height != c.Height || r.Hash != c.Hash {
		return errors.New("candidate lacks matching receipt")
	}
	if len(c.Candidate.Receipts) != len(snapshot.View.Receipts)+1 {
		return errors.New("candidate coverage is not a single addition")
	}
	for h, old := range snapshot.View.Receipts {
		candidate, ok := c.Candidate.Receipts[h]
		if !ok || !sameReceipt(old, candidate) {
			return errors.New("candidate changed existing coverage")
		}
	}
	d := Delta{Instance: c.Instance, RecipeHash: c.RecipeHash, Fence: c.Fence, Receipt: r, Head: c.Candidate.Head, Upsert: map[engine.Outpoint]engine.Coin{}}
	if snapshot.View.Head != nil {
		d.ParentHash = snapshot.View.Head.Hash
	}
	for key, old := range snapshot.View.Coins {
		if _, ok := c.Candidate.Coins[key]; !ok {
			d.Delete = append(d.Delete, key)
		} else if c.Candidate.Coins[key] != old {
			d.Upsert[key] = c.Candidate.Coins[key]
		}
	}
	for key, coin := range c.Candidate.Coins {
		if _, ok := snapshot.View.Coins[key]; !ok {
			d.Upsert[key] = coin
		}
	}
	_, err = s.CommitDelta(d)
	return err
}
