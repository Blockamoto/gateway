package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Cursors retain only checked no-match scan progress. They do not establish
// unspentness, transaction absence, or a parent relationship. The query and
// its last checked block are bound to a selected-chain anchor on every resume.
type parcelRawCursorQuery079 struct {
	Kind         string `json:"kind"`
	TxID         string `json:"txid"`
	Vout         uint32 `json:"vout,omitempty"`
	PointHeight  int64  `json:"point_height,omitempty"`
	PointTxIndex int    `json:"point_tx_index,omitempty"`
	From         int64  `json:"from"`
	To           int64  `json:"to"`
}
type parcelRawCursorDocument079 struct {
	Schema     int                     `json:"schema"`
	Query      parcelRawCursorQuery079 `json:"query"`
	Anchor     string                  `json:"anchor"`
	Next       int64                   `json:"next"`
	LastHeight int64                   `json:"last_height"`
	LastHash   string                  `json:"last_hash,omitempty"`
	Digest     string                  `json:"digest"`
}
type parcelRawCursor079 struct {
	mu       sync.Mutex
	path     string
	doc      parcelRawCursorDocument079
	base     string
	dirty    int
	selected func(int64) (string, error)
}

var parcelRawCursorWriteMu079 sync.Mutex

func parcelRawCursorDigest079(doc parcelRawCursorDocument079) string {
	doc.Digest = ""
	return indexDigest(doc)
}
func parcelRawCursorValid079(d parcelRawCursorDocument079, q parcelRawCursorQuery079) bool {
	if d.Schema != 1 || d.Query != q || !validHash(d.Anchor) || d.Digest != parcelRawCursorDigest079(d) {
		return false
	}
	if q.Kind == "transaction" {
		if d.Next < q.From-1 || d.Next > q.To || d.LastHeight != d.Next+1 && !(d.Next == q.To && d.LastHeight == -1) {
			return false
		}
	} else if d.Next < q.From || d.Next > q.To+1 || d.LastHeight != d.Next-1 && !(d.Next == q.From && d.LastHeight == -1) {
		return false
	}
	return d.LastHeight == -1 && d.LastHash == "" || d.LastHeight >= q.From && d.LastHeight <= q.To && validHash(d.LastHash)
}
func readParcelRawCursor079(path string, q parcelRawCursorQuery079) (parcelRawCursorDocument079, bool, error) {
	var d parcelRawCursorDocument079
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	if err = json.Unmarshal(raw, &d); err != nil || !parcelRawCursorValid079(d, q) {
		return d, false, fmt.Errorf("saved Parcel scan cursor is invalid")
	}
	return d, true, nil
}
func openParcelRawCursor079(root string, q parcelRawCursorQuery079, selected func(int64) (string, error)) (*parcelRawCursor079, error) {
	q.TxID = strings.ToLower(q.TxID)
	if !validHash(q.TxID) || q.From < 0 || q.To < q.From || q.To == int64(^uint64(0)>>1) || selected == nil || (q.Kind != "transaction" && q.Kind != "spender") || q.Kind == "spender" && (q.PointHeight != q.From || q.PointTxIndex < 0) || q.Kind == "transaction" && (q.Vout != 0 || q.PointHeight != 0 || q.PointTxIndex != 0) {
		return nil, fmt.Errorf("invalid Parcel scan cursor query")
	}
	anchor, err := selected(q.To)
	if err != nil || !validHash(anchor) {
		return nil, fmt.Errorf("Parcel cursor selected-chain anchor unavailable")
	}
	anchor = strings.ToLower(anchor)
	path := filepath.Join(root, "indexes", "parcel-raw-cursors-v1", indexDigest(q)+".json")
	parcelRawCursorWriteMu079.Lock()
	defer parcelRawCursorWriteMu079.Unlock()
	saved, have, err := readParcelRawCursor079(path, q)
	if err != nil {
		return nil, err
	}
	base := ""
	if have {
		base = saved.Digest
	}
	initial := q.From
	if q.Kind == "transaction" {
		initial = q.To
	}
	d := parcelRawCursorDocument079{Schema: 1, Query: q, Anchor: anchor, Next: initial, LastHeight: -1}
	if have && strings.EqualFold(saved.Anchor, anchor) {
		if saved.LastHeight >= 0 {
			h, e := selected(saved.LastHeight)
			if e != nil || !strings.EqualFold(h, saved.LastHash) {
				return nil, fmt.Errorf("Parcel cursor last checked block is no longer selected")
			}
		}
		d = saved
	}
	return &parcelRawCursor079{path: path, doc: d, base: base, selected: selected}, nil
}
func (c *parcelRawCursor079) Next() int64 { c.mu.Lock(); defer c.mu.Unlock(); return c.doc.Next }

// Call only after a full, integrity-checked selected block contains no match.
func (c *parcelRawCursor079) Advance(height int64, checkedHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if height != c.doc.Next || height < c.doc.Query.From || height > c.doc.Query.To || !validHash(checkedHash) {
		return fmt.Errorf("Parcel cursor advance is out of order")
	}
	c.doc.LastHeight, c.doc.LastHash = height, strings.ToLower(checkedHash)
	if c.doc.Query.Kind == "transaction" {
		c.doc.Next--
	} else {
		c.doc.Next++
	}
	c.dirty++
	if c.dirty >= 64 {
		return c.flushLocked()
	}
	return nil
}

// Restart discards continuation when its associated evidence journal is not
// available. This never converts the old cursor into a negative proof.
func (c *parcelRawCursor079) Restart() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	parcelRawCursorWriteMu079.Lock()
	defer parcelRawCursorWriteMu079.Unlock()
	previous, have, err := readParcelRawCursor079(c.path, c.doc.Query)
	if err != nil {
		return err
	}
	if have && previous.Digest != c.base || !have && c.base != "" {
		return fmt.Errorf("Parcel scan cursor changed concurrently")
	}
	anchor, err := c.selected(c.doc.Query.To)
	if err != nil || !strings.EqualFold(anchor, c.doc.Anchor) {
		return fmt.Errorf("Parcel scan cursor anchor changed")
	}
	next := c.doc.Query.From
	if c.doc.Query.Kind == "transaction" {
		next = c.doc.Query.To
	}
	reset := parcelRawCursorDocument079{Schema: 1, Query: c.doc.Query, Anchor: c.doc.Anchor, Next: next, LastHeight: -1}
	reset.Digest = parcelRawCursorDigest079(reset)
	if err = atomicWriteJSON(c.path, reset); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(c.path)); err != nil {
		return err
	}
	c.doc, c.base, c.dirty = reset, reset.Digest, 0
	return nil
}
func (c *parcelRawCursor079) Flush() error { c.mu.Lock(); defer c.mu.Unlock(); return c.flushLocked() }
func (c *parcelRawCursor079) flushLocked() error {
	if c.dirty == 0 {
		return nil
	}
	parcelRawCursorWriteMu079.Lock()
	defer parcelRawCursorWriteMu079.Unlock()
	// A stale concurrent scanner cannot overwrite a newer checkpoint.
	previous, have, err := readParcelRawCursor079(c.path, c.doc.Query)
	if err != nil {
		return err
	}
	if have && previous.Digest != c.base || !have && c.base != "" {
		return fmt.Errorf("Parcel scan cursor changed concurrently")
	}
	anchor, err := c.selected(c.doc.Query.To)
	if err != nil || !strings.EqualFold(anchor, c.doc.Anchor) {
		return fmt.Errorf("Parcel scan cursor anchor changed")
	}
	last, err := c.selected(c.doc.LastHeight)
	if err != nil || !strings.EqualFold(last, c.doc.LastHash) {
		return fmt.Errorf("Parcel scan cursor block left the selected chain")
	}
	c.doc.Digest = parcelRawCursorDigest079(c.doc)
	if err = atomicWriteJSON(c.path, c.doc); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(c.path)); err != nil {
		return err
	}
	c.base, c.dirty = c.doc.Digest, 0
	return nil
}
