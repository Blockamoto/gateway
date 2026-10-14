package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

var errNumberNavigationTip077 = errors.New("Selected chain tip is unavailable; use an explicit N.bitcoin or N.sat address.")

type numberNavigation077 struct {
	Kind      string `json:"kind"`
	Address   string `json:"address"`
	TipHeight int64  `json:"tip_height"`
	TipHash   string `json:"tip_hash"`
}

func decimalNumber077(input string) bool {
	if input == "" {
		return false
	}
	for _, c := range input {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Existing hash and destination identities win over bare-number shorthand.
// This check is entirely local and does not consult the chain or fetch data.
func bareNumberInput077(input string) bool {
	q := strings.TrimSpace(input)
	if !decimalNumber077(q) || validHash(q) {
		return false
	}
	if looksLikeAddress(q) {
		if _, _, _, err := bitcoinDestinationScript(q); err == nil {
			return false
		}
	}
	return true
}

func parseNavigationNumber077(input string) (uint64, string, error) {
	q := strings.TrimSpace(input)
	if len(q) > 1024 || !decimalNumber077(q) {
		return 0, "", fmt.Errorf("enter a non-negative whole number using digits only")
	}
	q = strings.TrimLeft(q, "0")
	if q == "" {
		q = "0"
	}
	n, err := strconv.ParseUint(q, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("number exceeds the supported unsigned 64-bit range")
	}
	return n, strconv.FormatUint(n, 10), nil
}

func (a *app) classifyNumberNavigation077(ctx context.Context, input string) (numberNavigation077, error) {
	if err := ctx.Err(); err != nil {
		return numberNavigation077{}, err
	}
	n, canonical, err := parseNavigationNumber077(input)
	if err != nil {
		return numberNavigation077{}, err
	}
	// Pin one actual selected authority. Peer-advertised target heights are
	// progress estimates and must never decide what a user's number means.
	authority := a.currentChainAuthority()
	if authority.Height < 0 || !validHash(authority.Hash) || (authority.Source != "bitcoin_core" && authority.Source != "bod_headers") {
		return numberNavigation077{}, errNumberNavigationTip077
	}
	if err := ctx.Err(); err != nil {
		return numberNavigation077{}, err
	}
	out := numberNavigation077{Kind: "sat", Address: canonical + ".sat", TipHeight: authority.Height, TipHash: strings.ToLower(authority.Hash)}
	if n <= uint64(authority.Height) {
		out.Kind, out.Address = "block", canonical+".bitcoin"
	}
	return out, nil
}

// Classification reads only chain authority metadata. It never resolves a
// block, builds an index or starts a Satline trace.
func (a *app) handleNumberNavigation077(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, http.StatusForbidden, fmt.Errorf("number navigation is restricted to the local management interface"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, fmt.Errorf("GET required"))
		return
	}
	out, err := a.classifyNumberNavigation077(r.Context(), r.URL.Query().Get("number"))
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errNumberNavigationTip077) {
			code = http.StatusConflict
		}
		jsonError(w, code, err)
		return
	}
	writeJSON(w, out)
}
