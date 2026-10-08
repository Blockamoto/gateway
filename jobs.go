package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (a *app) persistJobLocked(j *satlineJob) {
	if j == nil || j.ID == "" {
		return
	}
	_ = atomicWriteJSON(filepath.Join(a.dataDir, "satline", "jobs", j.ID+".json"), j)
}
func (a *app) loadDurableJobs() {
	a.satlineJobsMu.Lock()
	defer a.satlineJobsMu.Unlock()
	if a.satlineJobs == nil {
		a.satlineJobs = map[string]*satlineJob{}
	}
	entries, _ := os.ReadDir(filepath.Join(a.dataDir, "satline", "jobs"))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(a.dataDir, "satline", "jobs", e.Name()))
		if err != nil {
			continue
		}
		var j satlineJob
		if json.Unmarshal(b, &j) != nil || !j.Query.valid() || j.ID == "" || filepath.Base(j.ID) != j.ID || j.ID+".json" != e.Name() {
			continue
		}
		if !j.Done {
			j.Done = true
			j.Status = "paused"
			j.Stage = "Gateway restarted. Resume from the last complete checkpoint."
		}
		a.satlineJobs[j.ID] = &j
	}
}
func (a *app) handleJobs(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method == http.MethodGet {
		a.satlineJobsMu.Lock()
		defer a.satlineJobsMu.Unlock()
		jobs := []*satlineJob{}
		for _, j := range a.satlineJobs {
			jobs = append(jobs, j)
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].Updated.After(jobs[j].Updated) })
		satlineReply(w, map[string]any{"jobs": jobs})
		return
	}
	var req struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if !satlineBody(w, r, &req) {
		return
	}
	a.satlineJobsMu.Lock()
	j, ok := a.satlineJobs[req.ID]
	if !ok {
		a.satlineJobsMu.Unlock()
		jsonError(w, 404, fmt.Errorf("job not found"))
		return
	}
	if req.Action == "pause" || req.Action == "cancel" {
		if j.cancel != nil {
			j.cancel()
		}
		j.Status = "paused"
		if req.Action == "cancel" {
			j.Status = "cancelled"
		}
		j.Stage = "Stopping at a complete checkpoint"
		a.persistJobLocked(j)
		a.satlineJobsMu.Unlock()
		satlineReply(w, map[string]bool{"ok": true})
		return
	}
	q, op, n, peer := j.Query, j.Operation, j.MaxHops, j.Peer
	a.satlineJobsMu.Unlock()
	if req.Action != "resume" {
		jsonError(w, 400, fmt.Errorf("unknown job action"))
		return
	}
	if op == "rebuild" || op == "recheck" {
		op = "resolve"
	}
	id, e := a.startSatlineJob(q, op, n, peer)
	if e != nil {
		jsonError(w, 409, e)
		return
	}
	satlineReply(w, map[string]string{"id": id})
}
