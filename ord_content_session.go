package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const ordViewerLifetime = 20 * time.Minute
const ordViewerMaxIDs = 128
const ordViewerMaxFetches = 64
const ordViewerMaxBytes = 64 * 1024 * 1024
const ordViewerCookie = "gateway_ord_viewer"

type ordDependency struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	Height    *int64 `json:"height,omitempty"`
	BlockHash string `json:"block_hash,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
}
type ordViewerSession struct {
	mu             sync.Mutex
	token, root    string
	expires        time.Time
	ctx            context.Context
	cancel         context.CancelFunc
	dependencies   map[string]ordDependency
	pending        map[string]*ordViewerFetch
	semaphore      chan struct{}
	fetches, bytes int
}

type ordViewerFetch struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	record  ordRecord
	body    []byte
	err     error
}

func (a *app) attachOrdViewerForRequest(r *http.Request, rec *ordRecord) bool {
	if r.Context().Err() != nil {
		return false
	}
	a.attachOrdViewer(rec)
	if r.Context().Err() == nil {
		return true
	}
	if rec != nil && rec.ViewerSession != "" {
		a.ordViewerMu.Lock()
		if session := a.ordViewers[rec.ViewerSession]; session != nil {
			session.cancel()
			delete(a.ordViewers, rec.ViewerSession)
		}
		a.ordViewerMu.Unlock()
	}
	return false
}

// Only a trusted management action calls this function. Neither indexing nor
// the read-only content service can manufacture an unbounded fetching session.
func (a *app) attachOrdViewer(rec *ordRecord) {
	if rec == nil || !isInscriptionID(rec.ID) || rec.ContentURL == "" {
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return
	}
	token := hex.EncodeToString(random[:])
	ctx, cancel := context.WithTimeout(context.Background(), ordViewerLifetime)
	session := &ordViewerSession{token: token, root: rec.ID, expires: time.Now().Add(ordViewerLifetime), ctx: ctx, cancel: cancel, dependencies: map[string]ordDependency{}, pending: map[string]*ordViewerFetch{}, semaphore: make(chan struct{}, 4)}
	session.dependencies[rec.ID] = ordDependencyFromRecord(*rec)
	a.ordViewerMu.Lock()
	if a.ordViewers == nil {
		a.ordViewers = map[string]*ordViewerSession{}
	}
	for key, old := range a.ordViewers {
		if old.ctx.Err() != nil {
			old.cancel()
			delete(a.ordViewers, key)
		}
	}
	if len(a.ordViewers) >= 64 {
		a.ordViewerMu.Unlock()
		cancel()
		return
	}
	a.ordViewers[token] = session
	a.ordViewerMu.Unlock()
	rec.ViewerSession = token
	rec.DependencyURL = "/api/v1/ord/dependencies?session=" + token
	u, err := url.Parse(rec.ContentURL)
	if err != nil {
		return
	}
	q := u.Query()
	q.Set("session", token)
	u.RawQuery = q.Encode()
	rec.ContentURL = u.String()
	q.Set("download", "1")
	u.RawQuery = q.Encode()
	rec.RawURL = u.String()
	u.Path = "/preview/" + rec.ID
	q.Del("download")
	u.RawQuery = q.Encode()
	rec.PreviewURL = u.String()
}
func ordDependencyFromRecord(rec ordRecord) ordDependency {
	var height *int64
	if rec.Height >= 0 {
		h := rec.Height
		height = &h
	}
	return ordDependency{ID: rec.ID, State: "resolved", Height: height, BlockHash: rec.BlockHash, Evidence: rec.Evidence}
}
func (a *app) ordViewer(token string) *ordViewerSession {
	if len(token) != 64 {
		return nil
	}
	a.ordViewerMu.Lock()
	defer a.ordViewerMu.Unlock()
	s := a.ordViewers[token]
	if s != nil && s.ctx.Err() != nil {
		s.cancel()
		delete(a.ordViewers, token)
		return nil
	}
	return s
}
func (a *app) contentViewer(w http.ResponseWriter, r *http.Request, id string) *ordViewerSession {
	if token := r.URL.Query().Get("session"); token != "" {
		s := a.ordViewer(token)
		// A session can only be bootstrapped at its intended root through the
		// initial scoped navigation grant. Runtime references use an optional
		// HttpOnly cookie or same-origin Referer. This read-only viewer capability
		// grants no access to management routes or session cancellation.
		if s != nil && s.root == id && a.ordContentGrant(id) != "" && r.URL.Query().Get("view") == a.ordContentGrant(id) {
			http.SetCookie(w, &http.Cookie{Name: ordViewerCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(ordViewerLifetime.Seconds())})
			return s
		}
		return nil
	}
	// Embedded cross-site frames may not receive third-party cookies. Same-
	// origin referrers retain the intended document's opaque viewer capability;
	// the response policy never sends it to another origin. No bytes are changed.
	if referrer, err := url.Parse(r.Referer()); err == nil && referrer.User == nil && referrer.Scheme+"://"+referrer.Host == a.contentURL && (strings.HasPrefix(referrer.Path, "/content/") || strings.HasPrefix(referrer.Path, "/preview/")) {
		if token := referrer.Query().Get("session"); len(referrer.Query()["session"]) == 1 {
			if s := a.ordViewer(token); s != nil {
				return s
			}
		}
	}
	cookie, err := r.Cookie(ordViewerCookie)
	if err != nil {
		return nil
	}
	return a.ordViewer(cookie.Value)
}

func (s *ordViewerSession) recordError(id string, err error) {
	s.mu.Lock()
	s.dependencies[id] = ordDependencyFailure(err)
	d := s.dependencies[id]
	d.ID = id
	s.dependencies[id] = d
	s.mu.Unlock()
}

func ordDependencyFailure(err error) ordDependency {
	d := ordDependency{State: "unavailable", Error: "The requested inscription could not be resolved."}
	var resolution *inscriptionResolutionError
	if errors.As(err, &resolution) {
		d.State = resolution.State
		if resolution.State == ordLocationUnknown {
			d.Error = "The reveal transaction location is unknown. Provide its Bitcoin positional address or containing block."
		} else {
			d.Error = "The reveal is located, but verified block bytes are unavailable."
		}
		if resolution.Location != nil {
			h := resolution.Location.Height
			d.Height = &h
			d.BlockHash = resolution.Location.BlockHash
		}
	}
	if errors.Is(err, context.Canceled) {
		d.State = "canceled"
		d.Error = "Viewer request canceled."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		d.State = "timed_out"
		d.Error = "Dependency resolution exceeded its time budget."
	}
	return d
}
func (a *app) viewerInscription(ctx context.Context, s *ordViewerSession, id string) (ordRecord, []byte, error) {
	if err := ctx.Err(); err != nil {
		return ordRecord{}, nil, err
	}
	if s != nil {
		if err := s.ctx.Err(); err != nil {
			return ordRecord{}, nil, err
		}
	}
	rec, body, err := a.loadOrdRecord(id)
	if err == nil && rec.Provider != "local_ord_adapter" && !a.ordRecordSelectedContext(ctx, rec) {
		rec, body, err = ordRecord{}, nil, fmt.Errorf("cached reveal is not on the selected chain")
	}
	if err == nil {
		if s != nil {
			s.mu.Lock()
			if _, ok := s.dependencies[id]; !ok && len(s.dependencies) >= ordViewerMaxIDs {
				s.mu.Unlock()
				return ordRecord{}, nil, fmt.Errorf("dependency ID budget exceeded")
			}
			s.dependencies[id] = ordDependencyFromRecord(rec)
			s.mu.Unlock()
		}
		return rec, body, nil
	}
	if s == nil {
		return ordRecord{}, nil, ordResolutionFailure(ordLocationUnknown, nil, fmt.Errorf("resolve this inscription through an intended Gateway viewer first"))
	}
	s.mu.Lock()
	if fetch := s.pending[id]; fetch != nil {
		fetch.waiters++
		s.mu.Unlock()
		return waitOrdViewerFetch(ctx, s, id, fetch)
	}
	if prior, ok := s.dependencies[id]; ok && prior.State != "resolved" && prior.State != "loading" {
		s.mu.Unlock()
		return ordRecord{}, nil, fmt.Errorf("%s: %s", prior.State, prior.Error)
	}
	if len(s.dependencies) >= ordViewerMaxIDs || s.fetches >= ordViewerMaxFetches {
		s.mu.Unlock()
		return ordRecord{}, nil, fmt.Errorf("dependency request budget exceeded")
	}
	request, cancel := context.WithTimeout(s.ctx, 45*time.Second)
	fetch := &ordViewerFetch{done: make(chan struct{}), cancel: cancel, waiters: 1}
	s.pending[id] = fetch
	s.dependencies[id] = ordDependency{ID: id, State: "loading"}
	s.fetches++
	s.mu.Unlock()
	go a.fetchOrdViewerDependency(request, s, id, fetch)
	return waitOrdViewerFetch(ctx, s, id, fetch)
}

func waitOrdViewerFetch(ctx context.Context, s *ordViewerSession, id string, fetch *ordViewerFetch) (ordRecord, []byte, error) {
	select {
	case <-ctx.Done():
		s.mu.Lock()
		fetch.waiters--
		if fetch.waiters == 0 && s.pending[id] == fetch {
			delete(s.pending, id)
			delete(s.dependencies, id)
			fetch.cancel()
		}
		s.mu.Unlock()
		return ordRecord{}, nil, ctx.Err()
	case <-s.ctx.Done():
		return ordRecord{}, nil, s.ctx.Err()
	case <-fetch.done:
		return fetch.record, fetch.body, fetch.err
	}
}

func (a *app) fetchOrdViewerDependency(ctx context.Context, s *ordViewerSession, id string, fetch *ordViewerFetch) {
	defer fetch.cancel()
	select {
	case s.semaphore <- struct{}{}:
		fetch.record, fetch.err = a.resolveInscription(ctx, id, "")
		if fetch.err == nil {
			fetch.record, fetch.body, fetch.err = a.loadOrdRecord(fetch.record.ID)
		}
		<-s.semaphore
	case <-ctx.Done():
		fetch.err = ctx.Err()
	}
	s.mu.Lock()
	// A canceled obsolete worker cannot overwrite a later request's status.
	if s.pending[id] == fetch {
		if fetch.err == nil {
			s.dependencies[id] = ordDependencyFromRecord(fetch.record)
		} else {
			d := ordDependencyFailure(fetch.err)
			d.ID = id
			s.dependencies[id] = d
		}
		delete(s.pending, id)
	}
	close(fetch.done)
	s.mu.Unlock()
}
func (s *ordViewerSession) chargeBytes(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 0 || n > ordViewerMaxBytes-s.bytes {
		return false
	}
	s.bytes += n
	return true
}

func (a *app) ordContentFailure(w http.ResponseWriter, s *ordViewerSession, id string, err error) {
	dependency := ordDependency{ID: id, State: "location_unknown", Error: "Resolve this inscription through an intended Gateway viewer first."}
	if s != nil {
		s.mu.Lock()
		if known, ok := s.dependencies[id]; ok {
			dependency = known
		}
		s.mu.Unlock()
	}
	code := http.StatusNotFound
	if dependency.State == ordBytesUnavailable {
		code = http.StatusServiceUnavailable
	}
	if errors.Is(err, context.Canceled) {
		code = 499
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = http.StatusGatewayTimeout
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	writeJSON(w, dependency)
}

func (a *app) handleOrdDependencies(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "inscriptions") {
		return
	}
	token := r.URL.Query().Get("session")
	if r.Method == "POST" {
		var q struct {
			Session string `json:"session"`
			Action  string `json:"action"`
		}
		if !satlineBody(w, r, &q) {
			return
		}
		if q.Action != "close" {
			jsonError(w, 400, fmt.Errorf("unknown viewer action"))
			return
		}
		a.ordViewerMu.Lock()
		s := a.ordViewers[q.Session]
		if s != nil {
			s.cancel()
			delete(a.ordViewers, q.Session)
		}
		a.ordViewerMu.Unlock()
		writeJSON(w, map[string]any{"session": q.Session, "state": "closed"})
		return
	}
	if r.Method != "GET" {
		http.Error(w, "GET or POST required", 405)
		return
	}
	s := a.ordViewer(token)
	if s == nil {
		jsonError(w, 404, fmt.Errorf("viewer session expired or closed"))
		return
	}
	s.mu.Lock()
	dependencies := make([]ordDependency, 0, len(s.dependencies))
	for _, d := range s.dependencies {
		dependencies = append(dependencies, d)
	}
	fetches, bytes := s.fetches, s.bytes
	s.mu.Unlock()
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	writeJSON(w, map[string]any{"session": token, "state": "active", "expires_at": s.expires.UTC(), "dependencies": dependencies, "limits": map[string]any{"max_ids": ordViewerMaxIDs, "max_fetches": ordViewerMaxFetches, "max_bytes": ordViewerMaxBytes, "fetches": fetches, "bytes": bytes, "max_concurrent": 4, "request_timeout_seconds": 45}})
}
