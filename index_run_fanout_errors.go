package main

import (
	"fmt"
	"strings"
)

// A bounded retained diagnostic makes independent point-owner failures visible
// without claiming their already committed siblings failed or rolling them back.
func (a *app) recordRunSourceFactErrors(id string, height int64, hash string, rows []indexBrowseResult) string {
	return a.recordRunSourceFactFailures(id, sourceFactFailures(height, hash, rows))
}
func sourceFactFailures(height int64, hash string, rows []indexBrowseResult) []indexRunSourceFactError {
	failures := []indexRunSourceFactError{}
	for _, row := range rows {
		if row.State != "error" && row.State != "unavailable" {
			continue
		}
		reason := row.Error
		if len(reason) > 1024 {
			reason = reason[:1024]
		}
		failures = append(failures, indexRunSourceFactError{row.Index, height, hash, reason})
	}
	return failures
}
func (a *app) recordRunSourceFactFailures(id string, failures []indexRunSourceFactError) string {
	if len(failures) == 0 {
		return ""
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if run := a.indexRunState.Run; run != nil && run.ID == id {
		run.SourceFactErrors = append(run.SourceFactErrors, failures...)
		if len(run.SourceFactErrors) > 32 {
			run.SourceFactErrors = append([]indexRunSourceFactError(nil), run.SourceFactErrors[len(run.SourceFactErrors)-32:]...)
		}
	}
	names := []string{}
	for _, failure := range failures {
		if len(names) == 4 {
			break
		}
		names = append(names, failure.Index)
	}
	return fmt.Sprintf("Some armed source-fact owners could not publish: %s. Independently committed checkpoints are retained; source_fact_errors contains bounded anchors and details.", strings.Join(names, ", "))
}
