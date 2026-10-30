package main

// This is display-only placement. It never creates, arms, builds, or adds an
// execution dependency to an index. Index references can outlive a definition.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"./internal/updateapply"
)

const indexPresentationMaxNodes = 320
const indexPresentationMaxDepth = 32

var indexPresentationID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type indexPresentationNode struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // category or track
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"` // display node ID; empty is root
	Index  string `json:"index,omitempty"`  // track reference only; may be unknown
}
type indexPresentationView struct {
	Revision uint64                  `json:"revision"`
	Nodes    []indexPresentationNode `json:"nodes"`
}

func indexPresentationText(s string, max int) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validateIndexPresentation(nodes []indexPresentationNode) error {
	if len(nodes) > indexPresentationMaxNodes {
		return fmt.Errorf("too many presentation nodes")
	}
	byID := make(map[string]indexPresentationNode, len(nodes))
	for _, n := range nodes {
		if !indexPresentationID.MatchString(n.ID) {
			return fmt.Errorf("invalid presentation node id %q", n.ID)
		}
		if _, exists := byID[n.ID]; exists {
			return fmt.Errorf("duplicate presentation node id %q", n.ID)
		}
		if !indexPresentationText(n.Name, 128) {
			return fmt.Errorf("invalid presentation name for %q", n.ID)
		}
		switch n.Kind {
		case "category":
			if n.Index != "" {
				return fmt.Errorf("category %q cannot reference an index", n.ID)
			}
		case "track":
			if !indexPresentationText(n.Index, 256) {
				return fmt.Errorf("track %q needs an index reference", n.ID)
			}
		default:
			return fmt.Errorf("invalid presentation kind for %q", n.ID)
		}
		byID[n.ID] = n
	}
	for _, node := range nodes {
		seen := map[string]bool{node.ID: true}
		parent := node.Parent
		depth := 0
		for parent != "" {
			depth++
			if depth > indexPresentationMaxDepth {
				return fmt.Errorf("presentation hierarchy exceeds depth limit")
			}
			if seen[parent] {
				return fmt.Errorf("presentation hierarchy contains a cycle")
			}
			seen[parent] = true
			p, ok := byID[parent]
			if !ok {
				return fmt.Errorf("missing presentation parent %q", parent)
			}
			parent = p.Parent
		}
	}
	return nil
}

func (a *app) handleIndexPresentation(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, http.StatusForbidden, fmt.Errorf("presentation layout is restricted to the local management interface"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		jsonError(w, 405, fmt.Errorf("GET or POST required"))
		return
	}
	if r.Method == http.MethodGet {
		indexLayoutMu.Lock()
		d, err := readStoredIndexLayout(r.Context(), a.dataDir)
		indexLayoutMu.Unlock()
		if err != nil {
			jsonError(w, 409, err)
			return
		}
		nodes := append([]indexPresentationNode{}, d.Hierarchy...)
		writeJSON(w, indexPresentationView{Revision: d.Revision, Nodes: nodes})
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		jsonError(w, 400, fmt.Errorf("application/json required"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		jsonError(w, 400, fmt.Errorf("one bounded hierarchy request is required"))
		return
	}
	if err = recipeValidateJSON(body); err != nil {
		jsonError(w, 400, fmt.Errorf("invalid hierarchy request: %w", err))
		return
	}
	var q struct {
		ExpectedRevision *uint64                  `json:"expected_revision"`
		Nodes            *[]indexPresentationNode `json:"nodes"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&q) != nil || dec.Decode(new(any)) != io.EOF || q.ExpectedRevision == nil || q.Nodes == nil {
		jsonError(w, 400, fmt.Errorf("expected_revision and nodes are required"))
		return
	}
	if err = validateIndexPresentation(*q.Nodes); err != nil {
		jsonError(w, 400, err)
		return
	}
	if err = r.Context().Err(); err != nil {
		jsonError(w, 409, err)
		return
	}
	indexLayoutMu.Lock()
	defer indexLayoutMu.Unlock()
	d, err := readStoredIndexLayout(r.Context(), a.dataDir)
	if err != nil {
		jsonError(w, 409, err)
		return
	}
	if *q.ExpectedRevision != d.Revision {
		jsonError(w, 409, fmt.Errorf("timeline layout changed; reload before saving the hierarchy"))
		return
	}
	if d.Revision == ^uint64(0) {
		jsonError(w, 409, fmt.Errorf("timeline layout revision exhausted"))
		return
	}
	if err = r.Context().Err(); err != nil {
		jsonError(w, 409, err)
		return
	}
	// Keep the stored Order exactly as it was, including unknown and legacy
	// entries; this operation does not run the legacy read projection.
	d.Schema = 3
	d.Hierarchy = append([]indexPresentationNode(nil), (*q.Nodes)...)
	d.Revision++
	d.Checksum = indexLayoutDigest(d)
	if err = validateIndexLayoutStorageSize(d); err != nil {
		jsonError(w, 400, err)
		return
	}
	if err = updateapply.CheckPath(indexLayoutPath(a.dataDir)); err == nil {
		err = atomicWriteJSON(indexLayoutPath(a.dataDir), d)
	}
	if err == nil {
		err = syncDirectory(a.dataDir)
	}
	if err != nil {
		jsonError(w, 409, err)
		return
	}
	writeJSON(w, indexPresentationView{Revision: d.Revision, Nodes: append([]indexPresentationNode{}, d.Hierarchy...)})
}
