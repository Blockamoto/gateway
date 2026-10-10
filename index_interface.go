package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Both local interfaces delegate state and work to the same index runtime.
func (a *app) registerIndexRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/indexes", a.handleIndexPage)
	mux.HandleFunc("/api/v1/index/", a.handleIndexAPI)
}

func (a *app) handleIndexPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/indexes" {
		http.NotFound(w, r)
		return
	}
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	if !safeLocalRequest(r) {
		jsonError(w, http.StatusForbidden, fmt.Errorf("indexes are restricted to the local management interface"))
		return
	}
	body, err := gatewayShell.ReadFile("ui/shell/indexes.html")
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	var random [24]byte
	if _, err = rand.Read(random[:]); err != nil {
		jsonError(w, 500, err)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(random[:])
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	ancestors := "'none'"
	if r.URL.Query().Get("embedded") == "1" {
		ancestors = "'self'"
	}
	contentSource := ""
	if content, e := url.Parse(a.contentURL); e == nil && (content.Scheme == "http" || content.Scheme == "https") && content.Host != "" && content.User == nil {
		contentSource = " " + content.Scheme + "://" + content.Host
	}
	frameSource := contentSource
	if frameSource == "" {
		frameSource = " 'none'"
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'self' 'nonce-"+nonce+"'; connect-src 'self'; img-src 'self'"+contentSource+"; media-src"+frameSource+"; frame-src"+frameSource+"; object-src 'none'; frame-ancestors "+ancestors+"; base-uri 'none'; form-action 'self'")
	bootstrap, _ := json.Marshal(map[string]any{"content_origin": a.contentURL})
	page := strings.ReplaceAll(string(body), "__INDEX_NONCE__", nonce)
	page = strings.Replace(page, "__INDEX_BOOTSTRAP__", string(bootstrap), 1)
	io.WriteString(w, page)
}

func indexMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	jsonError(w, http.StatusMethodNotAllowed, fmt.Errorf("%s required", method))
	return false
}

func indexRequestBody(w http.ResponseWriter, r *http.Request, value any) bool {
	// Also bound requests when a handler is embedded without the outer guard.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		jsonError(w, 400, fmt.Errorf("invalid index request: %w", err))
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		jsonError(w, 400, fmt.Errorf("index request must be a JSON object"))
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		jsonError(w, 400, fmt.Errorf("index request must contain exactly one JSON object"))
		return false
	}
	fields := json.NewDecoder(bytes.NewReader(raw))
	fields.DisallowUnknownFields()
	if err := fields.Decode(value); err != nil {
		jsonError(w, 400, fmt.Errorf("invalid index request: %w", err))
		return false
	}
	return true
}

func indexRules(id string) (any, error) {
	if id == "" {
		return indexDefinitions(), nil
	}
	return findIndexDefinition(id)
}

func indexQueryLimit(raw string) (int, error) {
	if raw == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 1000 {
		return 0, fmt.Errorf("limit must be an integer from 1 to 1000")
	}
	return n, nil
}

func (a *app) handleIndexAPI(w http.ResponseWriter, r *http.Request) {
	var result any
	var err error
	switch strings.TrimPrefix(r.URL.Path, "/api/v1/index/") {
	case "sat":
		a.handleIndexSat(w, r)
		return
	case "discover-sat":
		a.handleInscriptionDiscoverSat(w, r)
		return
	case "rules":
		if !indexMethod(w, r, http.MethodGet) {
			return
		}
		result, err = indexRules(r.URL.Query().Get("index"))
	case "status":
		if !indexMethod(w, r, http.MethodGet) {
			return
		}
		result = a.indexStatus()
	case "storage":
		if !indexMethod(w, r, http.MethodGet) {
			return
		}
		result, err = a.indexStorage(r.Context(), r.URL.Query().Get("index"))
	case "plan", "build":
		if !indexMethod(w, r, http.MethodPost) {
			return
		}
		var req indexBuildRequest
		if !indexRequestBody(w, r, &req) {
			return
		}
		result, err = a.planIndexBuild(req)
		if err != nil && strings.HasSuffix(r.URL.Path, "/build") {
			if definition, e := findIndexDefinition(req.Index); e == nil && !definition.Buildable {
				jsonError(w, http.StatusConflict, err)
				return
			}
		}
		if err == nil && strings.HasSuffix(r.URL.Path, "/build") {
			result, err = a.startIndexBuild(req)
			if err != nil {
				jsonError(w, http.StatusConflict, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
		}
	case "pause":
		if !indexMethod(w, r, http.MethodPost) {
			return
		}
		var pause struct {
			Index string `json:"index,omitempty"`
		}
		if !indexRequestBody(w, r, &pause) {
			return
		}
		result, err = a.pauseIndexBuildFor(pause.Index)
	case "queue":
		if !indexMethod(w, r, http.MethodPost) {
			return
		}
		var req indexQueueRequest
		if !indexRequestBody(w, r, &req) {
			return
		}
		result, err = a.controlIndexQueue(req)
	case "live":
		if !indexMethod(w, r, http.MethodPost) {
			return
		}
		var request indexLiveRequest
		if !indexRequestBody(w, r, &request) {
			return
		}
		result, err = a.setIndexLivePolicy(request)
		if err != nil {
			jsonError(w, http.StatusConflict, err)
			return
		}
	case "publish", "unpublish":
		if !indexMethod(w, r, http.MethodPost) {
			return
		}
		var publication struct {
			Index      string `json:"index"`
			Checkpoint string `json:"checkpoint,omitempty"`
		}
		if !indexRequestBody(w, r, &publication) {
			return
		}
		publish := strings.HasSuffix(r.URL.Path, "/publish")
		if publish && publication.Checkpoint != "" {
			result, err = a.setIndexPublication(publication.Index, true, publication.Checkpoint)
		} else {
			result, err = a.setIndexPublication(publication.Index, publish)
		}
		if err != nil {
			jsonError(w, http.StatusConflict, err)
			return
		}
	case "query":
		if !indexMethod(w, r, http.MethodGet) {
			return
		}
		var limit int
		limit, err = indexQueryLimit(r.URL.Query().Get("limit"))
		if err == nil {
			if rawHeight := r.URL.Query().Get("height"); rawHeight != "" {
				var height int64
				height, err = strconv.ParseInt(rawHeight, 10, 64)
				if err == nil && height >= 0 {
					result, err = a.indexQueryBlock(r.Context(), r.URL.Query().Get("index"), height, limit)
				} else {
					err = fmt.Errorf("height must be a non-negative integer")
				}
			} else {
				result, err = a.indexQuery(r.URL.Query().Get("index"), limit)
			}
		}
	case "lookup":
		if !indexMethod(w, r, http.MethodGet) {
			return
		}
		result, err = a.indexLookup(r.URL.Query().Get("index"), r.URL.Query().Get("key"))
	case "peers":
		if !indexMethod(w, r, http.MethodGet) {
			return
		}
		result, err = a.indexPeerClaims()
	case "peer-preview":
		if !indexMethod(w, r, http.MethodPost) {
			return
		}
		var request struct {
			Peer  string `json:"peer"`
			Index string `json:"index"`
		}
		if !indexRequestBody(w, r, &request) {
			return
		}
		result, err = a.indexPeerPreview(request.Peer, request.Index)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, result)
}

type indexCLICommand struct {
	Action     string
	LiveAction string
	Request    indexBuildRequest
	Limit      int
	Key        string
	Peer       string
}

func parseIndexCLI(args []string) (indexCLICommand, error) {
	c := indexCLICommand{Action: "status", Limit: 50}
	if len(args) == 0 {
		return c, nil
	}
	c.Action = strings.ToLower(args[0])
	args = args[1:]
	switch c.Action {
	case "status", "pause", "peers":
		if len(args) != 0 {
			return c, fmt.Errorf("index %s takes no arguments", c.Action)
		}
		return c, nil
	case "rules":
		if len(args) > 1 {
			return c, fmt.Errorf("usage: index rules [index-id]")
		}
		if len(args) == 1 {
			c.Request.Index = args[0]
		}
		return c, nil
	case "publish", "unpublish":
		if len(args) != 1 || strings.HasPrefix(args[0], "-") {
			return c, fmt.Errorf("usage: index %s <index-id>", c.Action)
		}
		c.Request.Index = args[0]
		return c, nil
	case "lookup":
		if len(args) != 2 {
			return c, fmt.Errorf("usage: index lookup bitmap <district>")
		}
		c.Request.Index, c.Key = args[0], args[1]
		return c, nil
	case "peer":
		if len(args) != 2 {
			return c, fmt.Errorf("usage: index peer <known-peer-host:port> <index-id>")
		}
		c.Peer, c.Request.Index = args[0], args[1]
		return c, nil
	case "live":
		if len(args) < 2 || strings.HasPrefix(args[0], "-") {
			return c, fmt.Errorf("usage: index live <index-id> <enable|pause|resume|disable> [--retention ephemeral|cache|retain]")
		}
		c.Request.Index, c.LiveAction = args[0], strings.ToLower(args[1])
		if c.LiveAction != "enable" && c.LiveAction != "pause" && c.LiveAction != "resume" && c.LiveAction != "disable" {
			return c, fmt.Errorf("live action must be enable, pause, resume or disable")
		}
		args = args[2:]
	case "plan", "build", "query":
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return c, fmt.Errorf("index %s requires an index ID", c.Action)
		}
		c.Request.Index = args[0]
		args = args[1:]
	default:
		return c, fmt.Errorf("index action must be rules, plan, build, live, pause, status, query, lookup, peers, peer, publish or unpublish")
	}
	seen := make(map[string]bool)
	for len(args) > 0 {
		key, value, inline := strings.Cut(args[0], "=")
		args = args[1:]
		if seen[key] {
			return c, fmt.Errorf("duplicate option %s", key)
		}
		seen[key] = true
		if !inline {
			if len(args) == 0 {
				return c, fmt.Errorf("%s requires a value", key)
			}
			value, args = args[0], args[1:]
		}
		if c.Action == "live" {
			if key == "--conventional-ids" && (value == "true" || value == "false") {
				v := value == "true"
				c.Request.ConventionalIDs = &v
				continue
			}
			if key != "--retention" || value == "" || !validIndexRetention(value) {
				return c, fmt.Errorf("index live accepts --retention ephemeral|cache|retain and --conventional-ids true|false")
			}
			c.Request.Retention = value
			continue
		}
		if c.Action == "query" {
			if key != "--limit" {
				return c, fmt.Errorf("index query only accepts --limit")
			}
			var err error
			c.Limit, err = indexQueryLimit(value)
			if err != nil || value == "" {
				return c, fmt.Errorf("limit must be an integer from 1 to 1000")
			}
			continue
		}
		switch key {
		case "--conventional-ids":
			if value != "true" && value != "false" {
				return c, fmt.Errorf("conventional-ids requires true or false")
			}
			v := value == "true"
			c.Request.ConventionalIDs = &v
		case "--outputs":
			if value == "" {
				return c, fmt.Errorf("outputs requires comma-separated index IDs")
			}
			c.Request.Outputs = strings.Split(value, ",")
		case "--retain-sat-history":
			c.Request.SatHistoryConfigured = true
			if value != "true" && value != "false" {
				return c, fmt.Errorf("retain-sat-history requires true or false")
			}
			c.Request.RetainSatHistory = value == "true"
		case "--from", "--to":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return c, fmt.Errorf("%s requires an integer height", key)
			}
			if key == "--from" {
				c.Request.From = &n
			} else {
				c.Request.To = &n
			}
		case "--mode":
			if value != "lean" && value != "full" {
				return c, fmt.Errorf("mode requires lean or full")
			}
			c.Request.Mode = value
		case "--retention":
			if value == "" {
				return c, fmt.Errorf("retention requires ephemeral, cache or retain")
			}
			c.Request.Retention = value
		default:
			return c, fmt.Errorf("unknown index option %q", key)
		}
	}
	return c, nil
}

func (a *app) runIndexCLI(args []string) (any, error) {
	c, err := parseIndexCLI(args)
	if err != nil {
		return nil, err
	}
	switch c.Action {
	case "rules":
		return indexRules(c.Request.Index)
	case "status":
		return a.indexStatus(), nil
	case "plan":
		return a.planIndexBuild(c.Request)
	case "pause":
		return a.pauseIndexBuild()
	case "live":
		return a.setIndexLivePolicy(indexLiveRequest{Index: c.Request.Index, Action: c.LiveAction, Retention: c.Request.Retention, ConventionalIDs: c.Request.ConventionalIDs})
	case "query":
		return a.indexQuery(c.Request.Index, c.Limit)
	case "lookup":
		return a.indexLookup(c.Request.Index, c.Key)
	case "peers":
		return a.indexPeerClaims()
	case "peer":
		return a.indexPeerPreview(c.Peer, c.Request.Index)
	case "publish", "unpublish":
		return a.setIndexPublication(c.Request.Index, c.Action == "publish")
	case "build":
		if _, err = planIndex(c.Request); err != nil {
			return nil, err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		started, startErr := a.startIndexBuild(c.Request)
		if startErr != nil {
			return nil, startErr
		}
		job, err := a.waitQueuedIndexJob(ctx, started.ID)
		if ctx.Err() != nil {
			_, _ = a.controlIndexQueue(indexQueueRequest{started.ID, "pause"})
		}
		return job, err
	}
	return nil, fmt.Errorf("unknown index action")
}

// The profile is process-exclusive. Forward before main acquires that lock so
// a terminal can control an existing UI process through its local API.
func runEarlyIndexCLI(dataDir string, args []string) (bool, int) {
	if len(args) == 0 || !strings.EqualFold(args[0], "index") {
		return false, 0
	}
	done := func(value any, err error) (bool, int) {
		if err == nil {
			err = printCLIJSON(value)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			return true, 1
		}
		return true, 0
	}
	c, err := parseIndexCLI(args[1:])
	if err != nil {
		return done(nil, err)
	}
	// Definitions have no state or network dependency. Offline planning reads
	// local evidence without starting runtime services or acquiring write locks.
	if c.Action == "rules" {
		return done(indexRules(c.Request.Index))
	}
	if c.Action == "plan" {
		reader := &app{dataDir: dataDir, headersPath: filepath.Join(dataDir, "headers", "headers.bin"), settings: appSettings{CoreDisabled: true, CoreMountDisabled: true, NetworkDisabled: true}}
		return done(reader.planIndexBuild(c.Request))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, handled, err := forwardIndexCLI(ctx, dataDir, c)
	if handled || err != nil {
		return done(result, err)
	}
	if c.Action == "pause" {
		return done(requestIndexPause(dataDir))
	}
	return false, 0
}

type indexRemote struct {
	info   runtimeInfo
	client *http.Client
}

func newIndexRemote(info runtimeInfo) (*indexRemote, error) {
	u, err := url.Parse(info.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.Port() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("index runtime URL must be an exact local HTTP origin")
	}
	info.URL = strings.TrimRight(info.URL, "/")
	return &indexRemote{info: info, client: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (remote *indexRemote) request(ctx context.Context, path string, body any) (json.RawMessage, error) {
	method := http.MethodGet
	var payload []byte
	if body != nil {
		method = http.MethodPost
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, remote.info.URL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", remote.info.URL)
	req.Header.Set("X-Gateway-Token", remote.info.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := remote.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	const maxResponse = 32 << 20
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxResponse {
		return nil, fmt.Errorf("index response exceeds 32 MiB; reduce the query limit")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &problem)
		if problem.Error == "" {
			problem.Error = "runtime returned HTTP " + strconv.Itoa(res.StatusCode)
		}
		return nil, fmt.Errorf("%s", problem.Error)
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("runtime returned invalid JSON")
	}
	return json.RawMessage(b), nil
}

func forwardIndexCLI(ctx context.Context, dataDir string, command indexCLICommand) (any, bool, error) {
	info, err := readRuntimeInfo(dataDir)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	remote, err := newIndexRemote(info)
	if err != nil {
		return nil, true, err
	}
	defer remote.client.CloseIdleConnections()
	probe, cancel := context.WithTimeout(ctx, time.Second)
	ping, err := remote.request(probe, "/api/v1/runtime/ping", nil)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}
		// A closed/stale endpoint is safe to fall through: the existing profile
		// lock still prevents a second process from writing an active profile.
		return nil, false, nil
	}
	var identity struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(ping, &identity) != nil || info.PID <= 0 || identity.PID != info.PID {
		return nil, true, fmt.Errorf("local runtime identity changed; restart or refresh Gateway before sending index commands")
	}
	var body any
	path := "/api/v1/index/" + command.Action
	switch command.Action {
	case "query":
		path += "?index=" + url.QueryEscape(command.Request.Index) + "&limit=" + strconv.Itoa(command.Limit)
	case "lookup":
		path += "?index=" + url.QueryEscape(command.Request.Index) + "&key=" + url.QueryEscape(command.Key)
	case "peer":
		path = "/api/v1/index/peer-preview"
		body = struct {
			Peer  string `json:"peer"`
			Index string `json:"index"`
		}{command.Peer, command.Request.Index}
	case "build":
		body = command.Request
	case "pause":
		body = struct{}{}
	case "live":
		body = indexLiveRequest{Index: command.Request.Index, Action: command.LiveAction, Retention: command.Request.Retention, ConventionalIDs: command.Request.ConventionalIDs}
	case "publish", "unpublish":
		body = struct {
			Index string `json:"index"`
		}{command.Request.Index}
	}
	result, err := remote.request(ctx, path, body)
	if err != nil || command.Action != "build" {
		return result, true, err
	}
	var started indexJob
	if err = json.Unmarshal(result, &started); err != nil || started.ID == "" {
		return nil, true, fmt.Errorf("runtime returned no index job identity")
	}
	for {
		var snapshot struct {
			Job  indexJob   `json:"job"`
			Jobs []indexJob `json:"jobs"`
		}
		result, err = remote.request(ctx, "/api/v1/index/status", nil)
		if err == nil {
			err = json.Unmarshal(result, &snapshot)
		}
		if ctx.Err() != nil {
			pauseCtx, pauseCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, pauseErr := remote.request(pauseCtx, "/api/v1/index/queue", indexQueueRequest{started.ID, "pause"})
			pauseCancel()
			if pauseErr != nil {
				return nil, true, fmt.Errorf("interrupted; could not confirm pause: %w", pauseErr)
			}
			return nil, true, fmt.Errorf("interrupted; pause requested for the index build")
		}
		if err != nil {
			return nil, true, fmt.Errorf("lost contact with index job %s; inspect its status before restarting: %w", started.ID, err)
		}
		for _, job := range snapshot.Jobs {
			if job.ID == started.ID {
				snapshot.Job = job
				break
			}
		}
		if snapshot.Job.ID != started.ID {
			return nil, true, fmt.Errorf("runtime has moved to another index job; inspect retained coverage for job %s", started.ID)
		}
		if snapshot.Job.State != "running" && snapshot.Job.State != "waiting" && snapshot.Job.State != "pausing" && snapshot.Job.State != "queued" {
			if snapshot.Job.State == "failed" {
				return snapshot.Job, true, fmt.Errorf("%s", snapshot.Job.Error)
			}
			return snapshot.Job, true, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}
