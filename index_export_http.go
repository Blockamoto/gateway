package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
)

const indexExportJSONLFormat = "gateway-index-export-jsonl-v1"

type indexExportHTTPFailure struct {
	indexExportMetadata
	State string `json:"state"`
	Error string `json:"error"`
}

type indexExportWriteFunc func(context.Context, indexExportRequest, io.Writer) (indexExportMetadata, error)

func (a *app) registerIndexExportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/index/export/review", a.handleIndexExportReview)
	mux.HandleFunc("/api/v1/index/export/download", a.handleIndexExportDownload)
}

func indexExportHTTPHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
}

func indexExportHTTPError(w http.ResponseWriter, code int, state string, meta indexExportMetadata, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(indexExportHTTPFailure{indexExportMetadata: meta, State: state, Error: err.Error()})
}

func indexExportHTTPRequest(w http.ResponseWriter, r *http.Request, download bool) (indexExportRequest, bool) {
	var q indexExportRequest
	indexExportHTTPHeaders(w)
	if !safeLocalRequest(r) {
		indexExportHTTPError(w, http.StatusForbidden, "forbidden", indexExportMetadata{}, fmt.Errorf("exports are restricted to the local management interface"))
		return q, false
	}
	if !indexMethod(w, r, http.MethodPost) {
		return q, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		indexExportHTTPError(w, http.StatusUnsupportedMediaType, "invalid_request", indexExportMetadata{}, fmt.Errorf("export requests require application/json"))
		return q, false
	}
	if !indexRequestBody(w, r, &q) {
		return q, false
	}
	if q.Index != "inscriptions" && q.Index != bitmapStandardDistrictID && q.Index != bitmapStandardParcelID {
		indexExportHTTPError(w, http.StatusBadRequest, "invalid_request", indexExportMetadata{}, fmt.Errorf("choose Inscriptions or an active Bitmap Standard District/Parcel index"))
		return q, false
	}
	if q.Representation != "native" && q.Representation != "conventional" {
		indexExportHTTPError(w, http.StatusBadRequest, "invalid_request", indexExportMetadata{}, fmt.Errorf("representation must be native or conventional"))
		return q, false
	}
	if download && q.Snapshot == "" || q.Snapshot != "" && !validHash(q.Snapshot) {
		indexExportHTTPError(w, http.StatusBadRequest, "invalid_request", indexExportMetadata{}, fmt.Errorf("download requires the exact snapshot from a successful export review"))
		return q, false
	}
	return q, true
}

func indexExportFailureStatus(err error) (int, string) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return http.StatusRequestTimeout, "cancelled"
	}
	if errors.Is(err, errIndexExportBusy) {
		return http.StatusConflict, "busy"
	}
	if errors.Is(err, errIndexExportExpired) {
		return http.StatusConflict, "snapshot_expired"
	}
	if errors.Is(err, errIndexExportSnapshotChanged) {
		return http.StatusConflict, "snapshot_changed"
	}
	return http.StatusConflict, "unavailable"
}

func completeIndexExportMetadata(q indexExportRequest, meta indexExportMetadata) bool {
	validComplete := meta.Complete && !meta.Partial && meta.MissingCount == 0 && meta.OmittedWinners == 0
	validPartial := q.Index == bitmapStandardDistrictID && q.Representation == "conventional" && !meta.Complete && meta.Partial && meta.MissingCount > 0 && meta.OmittedWinners > 0
	return meta.Ready && (validComplete || validPartial) && meta.Index == q.Index && meta.Representation == q.Representation && meta.Format == indexExportJSONLFormat && validHash(meta.Snapshot) && (q.Snapshot == "" || meta.Snapshot == q.Snapshot)
}

func (a *app) handleIndexExportReview(w http.ResponseWriter, r *http.Request) {
	q, ok := indexExportHTTPRequest(w, r, false)
	if !ok {
		return
	}
	meta, err := a.indexExportReview(r.Context(), q)
	if err != nil {
		code, state := indexExportFailureStatus(err)
		indexExportHTTPError(w, code, state, meta, err)
		return
	}
	if !completeIndexExportMetadata(q, meta) {
		indexExportHTTPError(w, http.StatusConflict, "requirements", meta, fmt.Errorf("export is not complete; inspect the saved coverage and listed requirements before downloading"))
		return
	}
	writeJSON(w, meta)
}

func (a *app) handleIndexExportDownload(w http.ResponseWriter, r *http.Request) {
	q, ok := indexExportHTTPRequest(w, r, true)
	if !ok {
		return
	}
	serveIndexExportDownload(w, r, q, a.writeIndexExport)
}

// A core writer may discover missing/corrupt evidence late in the scan. Nothing
// becomes an attachment until the entire pinned artifact has been validated.
func serveIndexExportDownload(w http.ResponseWriter, r *http.Request, q indexExportRequest, write indexExportWriteFunc) {
	file, err := os.CreateTemp("", "gateway-index-export-*.jsonl")
	if err != nil {
		indexExportHTTPError(w, http.StatusInternalServerError, "unavailable", indexExportMetadata{}, fmt.Errorf("prepare export download: %w", err))
		return
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	digest := sha256.New()
	meta, err := write(r.Context(), q, io.MultiWriter(file, digest))
	if err == nil {
		err = r.Context().Err()
	}
	if err != nil {
		code, state := indexExportFailureStatus(err)
		indexExportHTTPError(w, code, state, meta, err)
		return
	}
	if !completeIndexExportMetadata(q, meta) {
		indexExportHTTPError(w, http.StatusConflict, "requirements", meta, fmt.Errorf("export is incomplete; no download was created"))
		return
	}
	info, err := file.Stat()
	if err == nil && info.Size() == 0 {
		err = fmt.Errorf("validated export artifact is empty")
	}
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		indexExportHTTPError(w, http.StatusInternalServerError, "unavailable", meta, fmt.Errorf("read prepared export download: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="gateway-%s-%s-%s.jsonl"`, q.Index, q.Representation, meta.Snapshot[:12]))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Gateway-Export-Snapshot", meta.Snapshot)
	w.Header().Set("X-Gateway-Export-SHA256", hex.EncodeToString(digest.Sum(nil)))
	w.Header().Set("X-Gateway-Export-Rows", strconv.FormatUint(meta.Rows, 10))
	w.Header().Set("X-Gateway-Export-Artifact-Complete", "true")
	w.Header().Set("X-Gateway-Export-Complete", strconv.FormatBool(meta.Complete))
	w.Header().Set("X-Gateway-Export-Partial", strconv.FormatBool(meta.Partial))
	w.Header().Set("X-Gateway-Export-Omitted-Winners", strconv.FormatUint(meta.OmittedWinners, 10))
	_, _ = io.Copy(w, file)
}
