package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

type recipeStreamTestWriter func([]byte) (int, error)

func (f recipeStreamTestWriter) Write(data []byte) (int, error) { return f(data) }

func TestRecipeSparseJSONStreamMatchesCanonicalLegacyBytes(t *testing.T) {
	ctx := context.Background()
	for _, row := range []map[string]any{nil, {}, {"a": nil, "b": true, "c": json.Number("9007199254740993"), "d": []any{"text\n<&>", false, json.Number("-12")}}, {"__proto__": "kept", "Title": "A", "title": "B", "a#": "z", "a\"": "q", "α": json.RawMessage(` { "z" : 2, "a":1 } `), "bytes": []byte{0, 1, 2}}} {
		want, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := recipeWriteRowJSON(ctx, &got, row); err != nil || !bytes.Equal(got.Bytes(), want) {
			t.Fatal("row encoding changed", got.String(), string(want), err)
		}
		key := `["table","key\u003c"]`
		wrapped, err := json.Marshal(recipeStoreRow{Key: key, Value: want})
		if err != nil {
			t.Fatal(err)
		}
		got.Reset()
		if err := recipeWriteStoreRowJSON(ctx, &got, key, row); err != nil || !bytes.Equal(got.Bytes(), wrapped) {
			t.Fatal("stored row encoding changed", got.String(), string(wrapped), err)
		}
	}
}

func TestRecipeSparseJSONStreamsLargeRowsAndStopsOnCancellation(t *testing.T) {
	ctx := context.Background()
	value := strings.Repeat("x", 512<<10)
	row := map[string]any{}
	for i := 0; i < 128; i++ {
		row[string(rune('A'+i))] = value
	}
	bytesWritten, maxWrite := int64(0), 0
	w := recipeStreamTestWriter(func(data []byte) (int, error) {
		bytesWritten += int64(len(data))
		maxWrite = max(maxWrite, len(data))
		return len(data), nil
	})
	if err := recipeWriteRowJSON(ctx, w, row); err != nil {
		t.Fatal(err)
	}
	if bytesWritten <= 64<<20 || maxWrite > 64<<10 {
		t.Fatal("large row was limited or emitted as one giant buffer", bytesWritten, maxWrite)
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	bytesWritten = 0
	w = func(data []byte) (int, error) {
		bytesWritten += int64(len(data))
		if bytesWritten > 1000 {
			cancel()
		}
		return len(data), nil
	}
	if err := recipeWriteRowJSON(canceled, w, row); !errors.Is(err, context.Canceled) || bytesWritten > 1000+(64<<10) {
		t.Fatal("row serialization ignored bounded-chunk cancellation", bytesWritten, err)
	}
	w = func(data []byte) (int, error) { return len(data) - 1, nil }
	if err := recipeWriteRowJSON(ctx, w, map[string]any{"a": 1}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short writer was accepted", err)
	}
	if err := recipeWriteRowJSON(ctx, io.Discard, map[string]any{"a": math.NaN()}); err == nil {
		t.Fatal("invalid JSON number was accepted")
	}
}
