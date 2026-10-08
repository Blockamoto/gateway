package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type resolverTarget struct {
	Namespace    string  `json:"namespace"`
	Kind         string  `json:"kind"`
	Input        string  `json:"input"`
	SearchQuery  string  `json:"search_query,omitempty"`
	CanonicalURI string  `json:"canonical_uri"`
	Friendly     string  `json:"friendly,omitempty"`
	TxID         string  `json:"txid,omitempty"`
	Index        int     `json:"index,omitempty"`
	SatOffset    *uint64 `json:"sat_offset,omitempty"`
}

type localResolveResponse struct {
	Target resolverTarget `json:"target"`
	Result searchResponse `json:"result"`
}

func parseUintIndex(s string) (int, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 31)
	if err != nil {
		return 0, false
	}
	return int(n), true
}
func parseHeightToken(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil && n >= 0
}
func godURI(friendly string) string {
	f := strings.TrimSpace(friendly)
	if strings.Contains(f, ":") || isInscriptionID(f) {
		return "god://open.gateway/?address=" + url.QueryEscape(f)
	}
	return "god://" + strings.ToLower(f)
}

// parseBitcoinFriendly mirrors the positional shorthand already used by BOD:
//
//	{height}.bitcoin
//	{txindex}.{height}.bitcoin
//	{vout}.{txindex}.{height}.bitcoin
//	i{vin}.{txindex}.{height}.bitcoin
//	{sat-offset}.{vout}.{txindex}.{height}.bitcoin
func parseBitcoinFriendly(input string) (resolverTarget, bool, error) {
	raw := strings.TrimSpace(input)
	lower := strings.ToLower(strings.TrimSuffix(raw, "."))
	if !strings.HasSuffix(lower, ".bitcoin") {
		return resolverTarget{}, false, nil
	}
	stem := strings.TrimSuffix(lower, ".bitcoin")
	parts := strings.Split(stem, ".")
	bad := func() (resolverTarget, bool, error) {
		return resolverTarget{}, true, fmt.Errorf("invalid .bitcoin coordinate %q", raw)
	}
	friendly := lower
	if len(parts) == 1 {
		if h, ok := parseHeightToken(parts[0]); ok {
			f := fmt.Sprintf("%d.bitcoin", h)
			return resolverTarget{Namespace: "bitcoin", Kind: "block", Input: raw, SearchQuery: strconv.FormatInt(h, 10), CanonicalURI: godURI(f), Friendly: f}, true, nil
		}
		return bad()
	}
	if len(parts) == 2 {
		txi, ok1 := parseUintIndex(parts[0])
		h, ok2 := parseHeightToken(parts[1])
		if !ok1 || !ok2 {
			return bad()
		}
		f := fmt.Sprintf("%d.%d.bitcoin", txi, h)
		return resolverTarget{Namespace: "bitcoin", Kind: "transaction", Input: raw, SearchQuery: txCoordinate(txi, h), CanonicalURI: godURI(f), Friendly: f, Index: txi}, true, nil
	}
	if len(parts) == 3 {
		h, okh := parseHeightToken(parts[2])
		if !okh {
			return bad()
		}
		if strings.HasPrefix(parts[0], "i") {
			vin, ok1 := parseUintIndex(strings.TrimPrefix(parts[0], "i"))
			txi, ok2 := parseUintIndex(parts[1])
			if !ok1 || !ok2 {
				return bad()
			}
			f := fmt.Sprintf("i%d.%d.%d.bitcoin", vin, txi, h)
			return resolverTarget{Namespace: "bitcoin", Kind: "input", Input: raw, SearchQuery: inputCoordinate(vin, txi, h), CanonicalURI: godURI(f), Friendly: f, Index: vin}, true, nil
		}
		vout, ok1 := parseUintIndex(parts[0])
		txi, ok2 := parseUintIndex(parts[1])
		if !ok1 || !ok2 {
			return bad()
		}
		f := fmt.Sprintf("%d.%d.%d.bitcoin", vout, txi, h)
		return resolverTarget{Namespace: "bitcoin", Kind: "output", Input: raw, SearchQuery: outputCoordinate(vout, txi, h), CanonicalURI: godURI(f), Friendly: f, Index: vout}, true, nil
	}
	if len(parts) == 4 {
		off, err := strconv.ParseUint(parts[0], 10, 64)
		vout, ok1 := parseUintIndex(parts[1])
		txi, ok2 := parseUintIndex(parts[2])
		h, ok3 := parseHeightToken(parts[3])
		if err != nil || !ok1 || !ok2 || !ok3 {
			return bad()
		}
		f := fmt.Sprintf("%d.%d.%d.%d.bitcoin", off, vout, txi, h)
		offCopy := off
		return resolverTarget{Namespace: "bitcoin", Kind: "satpoint", Input: raw, SearchQuery: satpointCoordinate(off, vout, txi, h), CanonicalURI: godURI(f), Friendly: f, Index: vout, SatOffset: &offCopy}, true, nil
	}
	_ = friendly
	return bad()
}

func parseGOD(input string) (resolverTarget, bool, error) {
	raw := strings.TrimSpace(input)
	if !strings.HasPrefix(strings.ToLower(raw), "god://") {
		return resolverTarget{}, false, nil
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Port() != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return resolverTarget{}, true, fmt.Errorf("invalid Gateway activation envelope")
	}
	address := u.Host
	if strings.EqualFold(address, "open.gateway") {
		q := u.Query()
		if len(q) != 1 || len(q["address"]) != 1 {
			return resolverTarget{}, true, fmt.Errorf("one encoded address is required")
		}
		address = q.Get("address")
		if strings.Contains(address, "://") {
			return resolverTarget{}, true, fmt.Errorf("nested activation envelopes are not supported")
		}
	} else if u.RawQuery != "" {
		return resolverTarget{}, true, fmt.Errorf("unexpected query on a Gateway resource")
	}
	t, e := parseResourceAddress(address)
	t.Input = raw
	return t, true, e
}

// v0.4.1 ond:// addresses are accepted only as a migration courtesy. They are
// never emitted or registered by v0.4.2.
func parseLegacyOND(input string) (resolverTarget, bool, error) {
	raw := strings.TrimSpace(input)
	low := strings.ToLower(raw)
	if !strings.HasPrefix(low, "ond://bitcoin/") {
		return resolverTarget{}, false, nil
	}
	p := strings.TrimPrefix(low, "ond://bitcoin/")
	seg := strings.Split(strings.Trim(p, "/"), "/")
	var friendly string
	switch {
	case len(seg) == 2 && seg[0] == "block":
		friendly = seg[1] + ".bitcoin"
	case len(seg) == 3 && seg[0] == "tx-at":
		friendly = seg[1] + "." + seg[2] + ".bitcoin"
	case len(seg) == 4 && seg[0] == "output-at":
		friendly = seg[1] + "." + seg[2] + "." + seg[3] + ".bitcoin"
	case len(seg) == 4 && seg[0] == "input-at":
		friendly = "i" + seg[1] + "." + seg[2] + "." + seg[3] + ".bitcoin"
	default:
		return resolverTarget{}, true, fmt.Errorf("legacy ond URI is not a supported positional address")
	}
	t, _, err := parseBitcoinFriendly(friendly)
	if err != nil {
		return resolverTarget{}, true, err
	}
	t.Input = raw
	return t, true, nil
}

func parseLocalResolverTarget(input string) (resolverTarget, error) {
	raw := strings.TrimSpace(input)
	if len(raw) > 1024 {
		return resolverTarget{}, fmt.Errorf("address too long")
	}
	if t, m, e := parseGOD(raw); m {
		return t, e
	}
	if t, m, e := parseLegacyOND(raw); m {
		return t, e
	}
	if strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") {
		u, e := url.Parse(raw)
		if e != nil || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return resolverTarget{}, fmt.Errorf("not a Gateway web resource")
		}
		raw = u.Host
	}
	return parseResourceAddress(strings.TrimSuffix(raw, "/"))
}
func parseResourceAddress(raw string) (resolverTarget, error) {
	raw = strings.TrimSpace(raw)
	low := strings.ToLower(raw)
	if strings.HasSuffix(low, ".bitmap") {
		district := strings.TrimSuffix(low, ".bitmap")
		n, err := strconv.ParseUint(district, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != district {
			return resolverTarget{}, fmt.Errorf("expected district.bitmap; parcel resolution is not implemented")
		}
		return resolverTarget{Namespace: "bitmap", Kind: "district", Input: raw, SearchQuery: district, Friendly: low, CanonicalURI: godURI(low)}, nil
	}
	if low == ".gateway" || low == "home.gateway" || low == "gateway.gateway" {
		return resolverTarget{Namespace: "gateway", Kind: "home", Input: raw, Friendly: ".gateway", CanonicalURI: "god://.gateway"}, nil
	}
	for _, name := range []string{"bitcoin", "satline", "ord", "peers", "storage", "activity", "settings", "discovery", "sync"} {
		if low == name+".gateway" {
			return resolverTarget{Namespace: "gateway", Kind: name, Input: raw, Friendly: low, CanonicalURI: godURI(low)}, nil
		}
	}
	stem := strings.TrimSuffix(strings.ToLower(raw), ".bitcoin")
	if hasHashCoordinateIdentity(stem) {
		c, ok := parseHashCoordinate(stem)
		if !ok {
			return resolverTarget{}, fmt.Errorf("invalid hash-located Bitcoin address")
		}
		f := c.Raw + ".bitcoin"
		kind := "transaction_coordinate"
		if c.Kind == coordInscription {
			kind = "inscription_coordinate"
		}
		return resolverTarget{Namespace: "bitcoin", Kind: kind, Input: raw, SearchQuery: c.Raw, Friendly: f, CanonicalURI: godURI(f), TxID: c.TxID}, nil
	}
	if c, ok := parseBODCoordinate(stem); ok && c.Kind == coordInscription {
		f := fmt.Sprintf("%d.i%d.%d.bitcoin", c.TxIndex, c.InscriptionIndex, c.Height)
		return resolverTarget{Namespace: "bitcoin", Kind: "inscription_coordinate", Input: raw, SearchQuery: fmt.Sprintf("%d.i%d.%d", c.TxIndex, c.InscriptionIndex, c.Height), Friendly: f, CanonicalURI: godURI(f)}, nil
	}
	if t, m, e := parseBitcoinFriendly(raw); m {
		return t, e
	}
	if isInscriptionID(low) {
		return resolverTarget{Namespace: "ord", Kind: "inscription", Input: raw, SearchQuery: low, Friendly: low, CanonicalURI: godURI(low)}, nil
	}
	parts := strings.Split(low, ":")
	if (len(parts) == 2 || len(parts) == 3) && validHash(parts[0]) {
		n, e := strconv.ParseUint(parts[1], 10, 32)
		if e != nil {
			return resolverTarget{}, fmt.Errorf("invalid output index")
		}
		f := fmt.Sprintf("%s:%d", parts[0], n)
		t := resolverTarget{Namespace: "bitcoin", Kind: "outpoint", Input: raw, TxID: parts[0], Index: int(n), Friendly: f, SearchQuery: f}
		if len(parts) == 3 {
			off, e := strconv.ParseUint(parts[2], 10, 64)
			if e != nil {
				return resolverTarget{}, fmt.Errorf("invalid sat offset")
			}
			t.Kind = "raw_satpoint"
			t.SatOffset = &off
			t.Friendly = fmt.Sprintf("%s:%d", f, off)
			t.SearchQuery = t.Friendly
		}
		t.CanonicalURI = godURI(t.Friendly)
		return t, nil
	}
	return resolverTarget{}, fmt.Errorf("unregistered Gateway address")
}
func (a *app) resolveLocalTarget(input string) (localResolveResponse, error) {
	t, e := parseLocalResolverTarget(input)
	if e != nil {
		return localResolveResponse{}, e
	}
	var result searchResponse
	switch {
	case t.Namespace == "gateway":
		result = searchResponse{Kind: "module", Module: t.Kind, Query: t.Friendly}
	case t.Namespace == "bitmap":
		value, err := a.indexLookup("bitmap", t.SearchQuery)
		if err != nil {
			return localResolveResponse{}, err
		}
		lookup := value.(indexLookupResult)
		if !lookup.Found {
			return localResolveResponse{}, fmt.Errorf("Bitmap district not found in indexed coverage; build or resume the Bitmap index in Indexes")
		}
		if lookup.ChainState != "selected_chain" {
			return localResolveResponse{}, fmt.Errorf("Bitmap index chain anchor is %s; sync headers or resume the index", lookup.ChainState)
		}
		rec, err := a.resolveInscription(context.Background(), lookup.Record.Inscription, lookup.RevealBlockHash)
		if err != nil {
			return localResolveResponse{}, err
		}
		result = searchResponse{Kind: "inscription", Ord: &rec}
	case t.Namespace == "ord":
		rec, e := a.resolveInscription(context.Background(), t.SearchQuery, "")
		if e != nil {
			return localResolveResponse{}, e
		}
		result = searchResponse{Kind: "inscription", Ord: &rec}
	case t.Kind == "outpoint" || t.Kind == "raw_satpoint":
		tx, e := a.resolveTransactionViaOverlay(t.TxID)
		if e != nil {
			return localResolveResponse{}, e
		}
		if !tx.TransactionVerified {
			return localResolveResponse{}, fmt.Errorf("transaction location is known; verified bytes from a block source are required for output or satpoint lookup")
		}
		if t.Index < 0 || t.Index >= len(tx.Transaction.Outputs) {
			return localResolveResponse{}, fmt.Errorf("output %d does not exist", t.Index)
		}
		result = searchResponse{Kind: "transaction", Tx: &tx, Coordinate: t.Friendly, FocusKind: "output", FocusIndex: t.Index}
		if t.SatOffset != nil {
			if *t.SatOffset >= tx.Transaction.Outputs[t.Index].ValueSats {
				return localResolveResponse{}, fmt.Errorf("sat offset outside output value")
			}
			result.FocusKind = "satpoint"
			result.FocusOffset = t.SatOffset
		}
	default:
		result, e = a.searchQuery(t.SearchQuery)
		if e != nil {
			return localResolveResponse{}, e
		}
	}
	result.Query = t.Friendly
	return localResolveResponse{Target: t, Result: result}, nil
}
func (a *app) resolveSearchInput(input string) (searchResponse, error) {
	if _, e := parseLocalResolverTarget(input); e == nil {
		x, e := a.resolveLocalTarget(input)
		return x.Result, e
	}
	return a.searchQuery(strings.TrimSpace(input))
}
func (a *app) handleLocalResolve(w http.ResponseWriter, r *http.Request) {
	var target string
	switch r.Method {
	case http.MethodGet:
		target = strings.TrimSpace(r.URL.Query().Get("target"))
		if target == "" {
			target = strings.TrimSpace(r.URL.Query().Get("q"))
		}
	case http.MethodPost:
		var req struct {
			Target string `json:"target"`
			Query  string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, 400, err)
			return
		}
		target = strings.TrimSpace(req.Target)
		if target == "" {
			target = strings.TrimSpace(req.Query)
		}
	default:
		http.Error(w, "GET or POST required", 405)
		return
	}
	if target == "" {
		jsonError(w, 400, fmt.Errorf("target is required"))
		return
	}
	out, err := a.resolveLocalTarget(target)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
