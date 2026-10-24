package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func indexExportHTTPTestRequest(method, endpoint, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:9090/api/v1/index/export/"+endpoint, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Origin", "http://127.0.0.1:9090")
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestIndexExportHTTPLocalStrictRequestGuards(t *testing.T) {
	valid := `{"index":"inscriptions","representation":"native"}`
	for _, test := range []struct {
		name, method, endpoint, body string
		change                       func(*http.Request)
		code                         int
	}{
		{"remote", "POST", "review", valid, func(r *http.Request) { r.RemoteAddr = "192.0.2.1:50000" }, 403},
		{"foreign host", "POST", "review", valid, func(r *http.Request) { r.Host = "attacker.invalid" }, 403},
		{"foreign origin", "POST", "review", valid, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }, 403},
		{"cross site", "POST", "review", valid, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"get", "GET", "review", "", nil, 405},
		{"download get", "GET", "download", "", nil, 405},
		{"wrong content type", "POST", "review", valid, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"missing content type", "POST", "review", valid, func(r *http.Request) { r.Header.Del("Content-Type") }, 415},
		{"empty", "POST", "review", "", nil, 400},
		{"array", "POST", "review", "[]", nil, 400},
		{"trailing", "POST", "review", valid + " {}", nil, 400},
		{"unknown field", "POST", "review", `{"index":"inscriptions","representation":"native","start":true}`, nil, 400},
		{"oversize", "POST", "review", `{"index":"` + strings.Repeat("a", 17<<10) + `","representation":"native"}`, nil, 400},
		{"legacy index", "POST", "review", `{"index":"bitmap-districts-canonical-v2","representation":"native"}`, nil, 400},
		{"unsupported index", "POST", "review", `{"index":"tx-locator","representation":"native"}`, nil, 400},
		{"unsupported representation", "POST", "review", `{"index":"inscriptions","representation":"txid"}`, nil, 400},
		{"malformed snapshot", "POST", "review", `{"index":"inscriptions","representation":"native","snapshot":"../head"}`, nil, 400},
		{"download needs review", "POST", "download", valid, nil, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := &app{dataDir: t.TempDir()}
			r := indexExportHTTPTestRequest(test.method, test.endpoint, test.body)
			if test.change != nil {
				test.change(r)
			}
			w := httptest.NewRecorder()
			// The handlers retain their own gate even without the outer middleware.
			if test.endpoint == "download" {
				a.handleIndexExportDownload(w, r)
			} else {
				a.handleIndexExportReview(w, r)
			}
			if w.Code != test.code || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
			entries, err := os.ReadDir(a.dataDir)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid export request touched local stores", entries, err)
			}
		})
	}
}

func TestIndexExportHTTPRoutesRegistered(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	for _, endpoint := range []string{"review", "download"} {
		w := indexInterfaceCall(h, "GET", "/api/v1/index/export/"+endpoint, "")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
			t.Fatal("export route was not registered", endpoint, w.Code, w.Body.String())
		}
	}
	w := indexInterfaceCall(h, "POST", "/api/v1/index/export/not-an-endpoint", `{}`)
	if w.Code != http.StatusNotFound {
		t.Fatal(w.Code, w.Body.String())
	}
}

func indexExportHTTPSpoolDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, root)
	}
	if filepath.Clean(os.TempDir()) != filepath.Clean(root) {
		t.Fatal("test could not isolate temporary export files", os.TempDir())
	}
	return root
}

func indexExportHTTPTestMetadata(q indexExportRequest) indexExportMetadata {
	return indexExportMetadata{Schema: 1, Index: q.Index, Representation: q.Representation, Format: indexExportJSONLFormat, Snapshot: q.Snapshot, Ready: true, Complete: true, Rows: 2, Blocks: 1}
}

func TestIndexExportHTTPDownloadCompleteArtifactAndIntegrityHeaders(t *testing.T) {
	root := indexExportHTTPSpoolDir(t)
	q := indexExportRequest{Index: "inscriptions", Representation: "native", Snapshot: strings.Repeat("a", 64)}
	body := "{\"type\":\"manifest\"}\n{\"blocator\":\"1i0.0\"}\n{\"type\":\"complete\",\"complete\":true}\n"
	r := indexExportHTTPTestRequest("POST", "download", "")
	w := httptest.NewRecorder()
	indexExportHTTPHeaders(w)
	serveIndexExportDownload(w, r, q, func(ctx context.Context, request indexExportRequest, output io.Writer) (indexExportMetadata, error) {
		if request != q || ctx != r.Context() {
			t.Fatal("download changed the pinned request")
		}
		_, err := io.WriteString(output, body)
		return indexExportHTTPTestMetadata(q), err
	})
	digest := sha256.Sum256([]byte(body))
	if w.Code != 200 || w.Body.String() != body || w.Header().Get("Content-Type") != "application/x-ndjson; charset=utf-8" || w.Header().Get("Content-Length") != strconv.Itoa(len(body)) || w.Header().Get("X-Gateway-Export-SHA256") != hex.EncodeToString(digest[:]) || w.Header().Get("X-Gateway-Export-Snapshot") != q.Snapshot || w.Header().Get("X-Gateway-Export-Rows") != "2" || w.Header().Get("X-Gateway-Export-Complete") != "true" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), `attachment; filename="gateway-inscriptions-native-`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	if files, err := os.ReadDir(root); err != nil || len(files) != 0 {
		t.Fatal("completed download leaked its spool", files, err)
	}
}

func TestIndexExportHTTPNeverAttachesLatePartialOrMismatchedExport(t *testing.T) {
	for _, kind := range []string{"late error", "requirements", "snapshot changed", "wrong representation", "cancelled", "empty"} {
		t.Run(kind, func(t *testing.T) {
			root := indexExportHTTPSpoolDir(t)
			q := indexExportRequest{Index: bitmapStandardParcelID, Representation: "conventional", Snapshot: strings.Repeat("a", 64)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := indexExportHTTPTestRequest("POST", "download", "").WithContext(ctx)
			w := httptest.NewRecorder()
			indexExportHTTPHeaders(w)
			serveIndexExportDownload(w, r, q, func(_ context.Context, _ indexExportRequest, output io.Writer) (indexExportMetadata, error) {
				meta := indexExportHTTPTestMetadata(q)
				if kind != "empty" {
					_, _ = io.WriteString(output, "PRIVATE_PARTIAL_MUST_NOT_DOWNLOAD\n")
				}
				switch kind {
				case "late error":
					return meta, fmt.Errorf("late source receipt validation failed")
				case "requirements":
					meta.Ready, meta.Complete, meta.MissingCount = false, false, 1
				case "snapshot changed":
					meta.Snapshot = strings.Repeat("b", 64)
				case "wrong representation":
					meta.Representation = "native"
				case "cancelled":
					cancel()
				}
				return meta, nil
			})
			want := http.StatusConflict
			if kind == "cancelled" {
				want = http.StatusRequestTimeout
			} else if kind == "empty" {
				want = http.StatusInternalServerError
			}
			if w.Code != want || strings.Contains(w.Body.String(), "PRIVATE_PARTIAL") || w.Header().Get("Content-Disposition") != "" || w.Header().Get("X-Gateway-Export-Complete") != "" || w.Header().Get("X-Gateway-Export-SHA256") != "" {
				t.Fatal("partial export escaped as a download", w.Code, w.Header(), w.Body.String())
			}
			var failure map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
				t.Fatal("failed export did not return structured requirements/error", failure, err)
			}
			if message, ok := failure["error"].(string); !ok || message == "" {
				t.Fatal("structured error message is missing", failure)
			}
			if kind == "requirements" && failure["missing_count"] != float64(1) {
				t.Fatal("missing evidence summary was discarded", failure)
			}
			if files, err := os.ReadDir(root); err != nil || len(files) != 0 {
				t.Fatal("failed export leaked its spool", files, err)
			}
		})
	}
}

func TestIndexExportHTTPFailureStates(t *testing.T) {
	for _, test := range []struct {
		err   error
		code  int
		state string
	}{
		{fmt.Errorf("capture: %w", errIndexExportBusy), 409, "busy"},
		{fmt.Errorf("capture: %w", errIndexExportSnapshotChanged), 409, "snapshot_changed"},
		{fmt.Errorf("capture: %w", context.DeadlineExceeded), 408, "cancelled"},
		{fmt.Errorf("source receipt unavailable"), 409, "unavailable"},
	} {
		if code, state := indexExportFailureStatus(test.err); code != test.code || state != test.state {
			t.Fatal(code, state, test)
		}
	}
}
