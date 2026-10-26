// Package sqlitebackend is an experimental SQLite implementation of the common
// engine's transactional fixture Backend. It is not a production index store.
package sqlitebackend

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"

	engine "gateway-common-engine-experiment"
	_ "modernc.org/sqlite"
)

type Backend struct {
	db         *sql.DB
	mu         sync.Mutex
	failNext   bool
	beforeNext func()
	afterWrite func()
}

var _ engine.Backend = (*Backend)(nil)

// layout describes every field recursively, so adding a field to engine.View
// cannot silently discard state when opening an existing database.
func layout(t reflect.Type) (string, error) {
	return layoutType(t, map[reflect.Type]bool{})
}
func layoutType(t reflect.Type, stack map[reflect.Type]bool) (string, error) {
	if stack[t] {
		return "", errors.New("recursive View schema unsupported")
	}
	stack[t] = true
	defer delete(stack, t)
	name := t.PkgPath() + "/" + t.Name() + ":"
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice:
		e, err := layoutType(t.Elem(), stack)
		return name + t.Kind().String() + "(" + e + ")", err
	case reflect.Map:
		k, err := layoutType(t.Key(), stack)
		if err != nil {
			return "", err
		}
		v, err := layoutType(t.Elem(), stack)
		return name + "map(" + k + "," + v + ")", err
	case reflect.Struct:
		var b strings.Builder
		b.WriteString("struct(")
		b.WriteString(t.PkgPath())
		b.WriteString("/")
		b.WriteString(t.Name())
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				return "", errors.New("unexported View schema field")
			}
			typ, err := layoutType(f.Type, stack)
			if err != nil {
				return "", err
			}
			b.WriteString(";" + f.Name + ":" + string(f.Tag) + ":" + typ)
		}
		b.WriteString(")")
		return b.String(), nil
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return name + t.Kind().String(), nil
	default:
		return "", fmt.Errorf("unsupported View schema kind %s", t.Kind())
	}
}
func schemaID() (string, error) {
	s, err := layout(reflect.TypeOf(engine.View{}))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("gob-v1:" + s))
	return fmt.Sprintf("%x", sum), nil
}

func Open(path string) (*Backend, error) {
	schema, err := schemaID()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
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
		`CREATE TABLE IF NOT EXISTS instances (instance TEXT PRIMARY KEY, recipe_hash TEXT NOT NULL, revision INTEGER NOT NULL, view_blob BLOB NOT NULL)`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO meta(id,schema_id,epoch) VALUES(1,?,0)`, schema); err != nil {
		return nil, err
	}
	var saved string
	if err = tx.QueryRow(`SELECT schema_id FROM meta WHERE id=1`).Scan(&saved); err != nil {
		return nil, err
	}
	if saved != schema {
		return nil, errors.New("unsupported stored View schema; migration required")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	opened = true
	return &Backend{db: db}, nil
}
func (b *Backend) Close() error { return b.db.Close() }
func number(n uint64) (int64, error) {
	if n > math.MaxInt64 {
		return 0, errors.New("SQLite integer overflow")
	}
	return int64(n), nil
}
func encode(v engine.View) ([]byte, error) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(v)
	return buf.Bytes(), err
}
func decode(data []byte) (engine.View, error) {
	var v engine.View
	decoder := gob.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&v); err != nil {
		return engine.View{}, err
	}
	var extra engine.View
	if err := decoder.Decode(&extra); err != io.EOF {
		return engine.View{}, errors.New("trailing or corrupt stored View bytes")
	}
	if v.Coins == nil || v.Receipts == nil {
		return engine.View{}, errors.New("incomplete stored View")
	}
	return v, nil
}
func empty() engine.View {
	return engine.View{Coins: map[engine.Outpoint]engine.Coin{}, Receipts: map[uint64]engine.Receipt{}}
}

func lockWriter(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE meta SET epoch=epoch WHERE id=1`)
	return err
}
func anchor(tx *sql.Tx, h uint64) (string, error) {
	i, err := number(h)
	if err != nil {
		return "", err
	}
	var s string
	err = tx.QueryRow(`SELECT hash FROM anchors WHERE height=?`, i).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s, err
}
func epoch(tx *sql.Tx) (uint64, error) {
	var n int64
	err := tx.QueryRow(`SELECT epoch FROM meta WHERE id=1`).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("invalid chain epoch")
	}
	return uint64(n), nil
}
func consent(tx *sql.Tx, instance string) (bool, uint64, error) {
	var enabled, n int64
	err := tx.QueryRow(`SELECT enabled,epoch FROM consents WHERE instance=?`, instance).Scan(&enabled, &n)
	if err == sql.ErrNoRows {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if (enabled != 0 && enabled != 1) || n < 0 {
		return false, 0, errors.New("invalid consent state")
	}
	return enabled == 1, uint64(n), nil
}
func instance(tx *sql.Tx, id, hash string) (engine.View, uint64, error) {
	var stored string
	var rev int64
	var blob []byte
	err := tx.QueryRow(`SELECT recipe_hash,revision,view_blob FROM instances WHERE instance=?`, id).Scan(&stored, &rev, &blob)
	if err == sql.ErrNoRows {
		return empty(), 0, nil
	}
	if err != nil {
		return engine.View{}, 0, err
	}
	if stored != hash {
		return engine.View{}, 0, errors.New("instance recipe mismatch")
	}
	if rev < 0 {
		return engine.View{}, 0, errors.New("invalid instance revision")
	}
	v, err := decode(blob)
	return v, uint64(rev), err
}
func checkCurrent(tx *sql.Tx, v engine.View) error {
	if v.Head != nil {
		a, err := anchor(tx, v.Head.Height)
		if err != nil {
			return err
		}
		if v.Head.Hash == "" || a != v.Head.Hash {
			return errors.New("saved head is no longer on selected chain")
		}
	}
	for h, r := range v.Receipts {
		a, err := anchor(tx, h)
		if err != nil {
			return err
		}
		if r.Height != h || r.Hash == "" || a != r.Hash {
			return errors.New("saved coverage is no longer on selected chain")
		}
	}
	return nil
}

// Encoding may normalize nil and empty slices. Their meaning for ordered rows
// and cells is identical; compare their elements rather than allocation shape.
func equalReceipt(a, b engine.Receipt) bool {
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

func (b *Backend) View(id, hash string) (engine.View, error) {
	tx, err := b.db.Begin()
	if err != nil {
		return engine.View{}, err
	}
	defer tx.Rollback()
	v, _, err := instance(tx, id, hash)
	if err != nil {
		return engine.View{}, err
	}
	if err = checkCurrent(tx, v); err != nil {
		return engine.View{}, err
	}
	if err = tx.Commit(); err != nil {
		return engine.View{}, err
	}
	return v, nil
}
func (b *Backend) Read(id, recipeHash string, height uint64, hash string) (engine.Snapshot, error) {
	tx, err := b.db.Begin()
	if err != nil {
		return engine.Snapshot{}, err
	}
	defer tx.Rollback()
	v, rev, err := instance(tx, id, recipeHash)
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
	if err = checkCurrent(tx, v); err != nil {
		return engine.Snapshot{}, err
	}
	chain, err := epoch(tx)
	if err != nil {
		return engine.Snapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return engine.Snapshot{}, err
	}
	return engine.Snapshot{View: v, Fence: engine.Fence{Revision: rev, ChainEpoch: chain, ConsentEpoch: ce}}, nil
}

func (b *Backend) BeforeNextCommit(f func()) { b.mu.Lock(); defer b.mu.Unlock(); b.beforeNext = f }
func (b *Backend) FailNextCommit()           { b.mu.Lock(); defer b.mu.Unlock(); b.failNext = true }

// AfterWriteBeforeCommit is a test-only crash injection point inside the SQL
// transaction; its hook must not call methods on this Backend.
func (b *Backend) AfterWriteBeforeCommit(f func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.afterWrite = f
}
func (b *Backend) Commit(c engine.Commit) error {
	b.mu.Lock()
	hook := b.beforeNext
	b.beforeNext = nil
	b.mu.Unlock()
	if hook != nil {
		hook()
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockWriter(tx); err != nil {
		return err
	}
	v, rev, err := instance(tx, c.Instance, c.RecipeHash)
	if err != nil {
		return err
	}
	on, ce, err := consent(tx, c.Instance)
	if err != nil {
		return err
	}
	if !on || ce != c.Fence.ConsentEpoch {
		return errors.New("stale consent fence")
	}
	chain, err := epoch(tx)
	if err != nil {
		return err
	}
	a, err := anchor(tx, c.Height)
	if err != nil {
		return err
	}
	if chain != c.Fence.ChainEpoch || a != c.Hash {
		return errors.New("stale selected-chain fence")
	}
	if err = checkCurrent(tx, v); err != nil {
		return err
	}
	if rev != c.Fence.Revision {
		return errors.New("stale instance revision")
	}
	if c.Candidate.Coins == nil || c.Candidate.Receipts == nil {
		return errors.New("incomplete candidate View")
	}
	r, ok := c.Candidate.Receipts[c.Height]
	if !ok || r.Hash != c.Hash || r.Height != c.Height {
		return errors.New("candidate lacks matching receipt")
	}
	if len(c.Candidate.Receipts) != len(v.Receipts)+1 {
		return errors.New("candidate coverage is not a single addition")
	}
	for h, old := range v.Receipts {
		candidate, ok := c.Candidate.Receipts[h]
		if !ok || !equalReceipt(old, candidate) {
			return errors.New("candidate changed existing coverage")
		}
	}
	if err = checkCurrent(tx, c.Candidate); err != nil {
		return err
	}
	if rev >= math.MaxInt64 {
		return errors.New("revision overflow")
	}
	blob, err := encode(c.Candidate)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO instances(instance,recipe_hash,revision,view_blob) VALUES(?,?,?,?) ON CONFLICT(instance) DO UPDATE SET recipe_hash=excluded.recipe_hash,revision=excluded.revision,view_blob=excluded.view_blob`, c.Instance, c.RecipeHash, int64(rev+1), blob)
	if err != nil {
		return err
	}
	b.mu.Lock()
	afterWrite := b.afterWrite
	b.afterWrite = nil
	b.mu.Unlock()
	if afterWrite != nil {
		afterWrite()
	}
	b.mu.Lock()
	fail := b.failNext
	b.failNext = false
	b.mu.Unlock()
	if fail {
		return errors.New("injected transaction failure")
	}
	return tx.Commit()
}

// SetAnchor and SetConsent are fixture authority controls; they do not verify
// Bitcoin data. Changes are durable SQLite transactions and advance fences.
func (b *Backend) SetAnchor(height uint64, hash string) error {
	h, err := number(height)
	if err != nil {
		return err
	}
	tx, err := b.db.Begin()
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
		current, e := epoch(tx)
		if e != nil {
			return e
		}
		if current >= math.MaxInt64 {
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
		_, err = tx.Exec(`UPDATE meta SET epoch=epoch+1 WHERE id=1`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (b *Backend) SetConsent(id string, enabled bool) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockWriter(tx); err != nil {
		return err
	}
	old, n, err := consent(tx, id)
	if err != nil {
		return err
	}
	if old != enabled {
		if n >= math.MaxInt64 {
			return errors.New("consent epoch overflow")
		}
		value := 0
		if enabled {
			value = 1
		}
		_, err = tx.Exec(`INSERT INTO consents(instance,enabled,epoch) VALUES(?,?,?) ON CONFLICT(instance) DO UPDATE SET enabled=excluded.enabled,epoch=excluded.epoch`, id, value, int64(n+1))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
