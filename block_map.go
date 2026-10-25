package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

type blockMapPreview struct {
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	DataURL string `json:"data_url,omitempty"`
}
type blockMapInscription struct {
	Coordinate  string           `json:"coordinate"`
	ID          string           `json:"id"`
	Index       uint32           `json:"index"`
	ContentType string           `json:"content_type"`
	Size        int              `json:"size"`
	Preview     *blockMapPreview `json:"preview,omitempty"`
}
type blockMapTransaction struct {
	Index            int                  `json:"index"`
	TxID             string               `json:"txid"`
	OutputSats       uint64               `json:"output_sats"`
	VSize            int                  `json:"vsize"`
	Weight           int                  `json:"weight"`
	Coinbase         bool                 `json:"coinbase"`
	Coordinate       string               `json:"coordinate"`
	InscriptionCount int                  `json:"inscription_count"`
	Inscription      *blockMapInscription `json:"inscription,omitempty"`
}
type blockMapView struct {
	Height            int64                 `json:"height"`
	Hash              string                `json:"hash"`
	Total             int                   `json:"total"`
	TransactionCount  int                   `json:"transaction_count"`
	Transactions      []blockMapTransaction `json:"transactions"`
	Indexing          []indexBrowseResult   `json:"indexing"`
	Verification      verificationView      `json:"verification"`
	VerificationState string                `json:"verification_state"`
}

// Map previews are inert text or small, validated raster images only. HTML,
// SVG, delegated and recursive content retain their isolated viewer path.
func safeBlockMapPreview(o inscriptionOccurrence, remaining *int) *blockMapPreview {
	ct, _, err := mime.ParseMediaType(o.Envelope.ContentType)
	if err != nil || strings.TrimSpace(o.Envelope.ContentEncoding) != "" {
		return nil
	}
	if ct == "text/plain" && utf8.Valid(o.Body) {
		runes := []rune(string(o.Body))
		if len(runes) > 160 {
			runes = runes[:160]
		}
		text := string(runes)
		if len(text) > *remaining {
			return nil
		}
		*remaining -= len(text)
		return &blockMapPreview{Kind: "text", Text: text}
	}
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/gif" {
		return nil
	}
	if len(o.Body) == 0 || len(o.Body) > 64<<10 || len(o.Body) > *remaining || http.DetectContentType(o.Body) != ct {
		return nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(o.Body))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || (format != "png" && format != "jpeg" && format != "gif") {
		return nil
	}
	dataURL := "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(o.Body)
	if len(dataURL) > *remaining {
		return nil
	}
	*remaining -= len(dataURL)
	return &blockMapPreview{Kind: "image", DataURL: dataURL}
}
func makeBlockMap(block blockView, extracted inscriptionBlockResult) (blockMapView, error) {
	if !integrityVerified(block) || len(block.Transactions) == 0 || uint64(len(block.Transactions)) != block.TransactionCount {
		return blockMapView{}, fmt.Errorf("block map requires complete verified transactions")
	}
	if err := validateCompleteInscriptionInventory(block, &extracted); err != nil {
		return blockMapView{}, err
	}
	out := blockMapView{Height: block.Height, Hash: block.Hash, Total: len(block.Transactions), TransactionCount: len(block.Transactions), Transactions: make([]blockMapTransaction, 0, len(block.Transactions)), Indexing: []indexBrowseResult{}, Verification: block.Verification, VerificationState: block.VerificationState}
	for i, tx := range block.Transactions {
		if tx.Index != i || !validHash(tx.TxID) {
			return out, fmt.Errorf("block map transaction order mismatch")
		}
		out.Transactions = append(out.Transactions, blockMapTransaction{Index: i, TxID: tx.TxID, OutputSats: tx.OutputSats, VSize: tx.VSize, Weight: tx.Weight, Coinbase: tx.Coinbase, Coordinate: fmt.Sprintf("%d.%d.bitcoin", i, block.Height)})
	}
	remaining := 4 << 20
	for _, o := range extracted.Occurrences {
		if o.TxIndex < 0 || o.TxIndex >= len(out.Transactions) {
			return out, fmt.Errorf("block map inscription position mismatch")
		}
		row := &out.Transactions[o.TxIndex]
		row.InscriptionCount++
		if row.Inscription == nil {
			row.Inscription = &blockMapInscription{Coordinate: inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height), ID: o.ID, Index: o.Index, ContentType: o.Envelope.ContentType, Size: len(o.Body), Preview: safeBlockMapPreview(o, &remaining)}
		}
	}
	return out, nil
}
func (a *app) handleIndexBlockMap(w http.ResponseWriter, r *http.Request) {
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	block, err := a.fetchAndDecodeContext(r.Context(), r.URL.Query().Get("block"))
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	extracted := block.inscriptionExtraction
	if extracted == nil {
		complete, extractErr := extractInscriptionOccurrences(block)
		if extractErr != nil {
			jsonError(w, 400, extractErr)
			return
		}
		extracted = &complete
	}
	result, err := makeBlockMap(block, *extracted)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	result.Indexing = a.indexBrowsedBlock(r.Context(), block, extracted)
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 16<<20 {
		jsonError(w, 400, fmt.Errorf("block map exceeds the bounded summary budget"))
		return
	}
	writeJSON(w, result)
}

// Compatibility notification for callers already holding a complete block.
// Generic and dependency resolution use the common source hook directly;
// indexing alone must not fetch an additional containing block.
func (a *app) indexBrowsedNavigation(ctx context.Context, result searchResponse) []indexBrowseResult {
	if result.Block != nil {
		return a.indexBrowsedBlock(ctx, *result.Block, nil)
	}
	return []indexBrowseResult{}
}
