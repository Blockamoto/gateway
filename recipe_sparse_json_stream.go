package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Keep the canonical encoding/json bytes while buffering at most one field,
// rather than the whole projected row or block. The interpreter's source and
// expression budgets still govern the individual in-memory field values.
type recipeJSONContextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w recipeJSONContextWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		if err := w.ctx.Err(); err != nil {
			return written, err
		}
		part := data[:min(len(data), 64<<10)]
		n, err := w.w.Write(part)
		written += n
		if err != nil {
			return written, err
		}
		if n != len(part) {
			return written, io.ErrShortWrite
		}
		data = data[n:]
	}
	return written, nil
}

// Encoder.Encode appends one newline. Retain only its last byte and discard
// precisely that trailing newline; newlines inside JSON strings stay escaped.
type recipeJSONValueWriter struct {
	w       io.Writer
	last    byte
	pending bool
}

func (w *recipeJSONValueWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if w.pending {
		if _, err := w.w.Write([]byte{w.last}); err != nil {
			return 0, err
		}
		w.pending = false
	}
	n, err := w.w.Write(data[:len(data)-1])
	if err != nil {
		return n, err
	}
	if n != len(data)-1 {
		return n, io.ErrShortWrite
	}
	w.last, w.pending = data[len(data)-1], true
	return len(data), nil
}

func recipeWriteJSONValue(ctx context.Context, w io.Writer, value any) error {
	if ctx == nil || w == nil {
		return fmt.Errorf("JSON streaming requires context and writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	filtered := &recipeJSONValueWriter{w: recipeJSONContextWriter{ctx: ctx, w: w}}
	if err := json.NewEncoder(filtered).Encode(value); err != nil {
		return err
	}
	if !filtered.pending || filtered.last != '\n' {
		return fmt.Errorf("JSON encoder did not terminate its value")
	}
	return ctx.Err()
}

func recipeWriteRowJSON(ctx context.Context, w io.Writer, row map[string]any) error {
	if row == nil {
		return recipeWriteJSONValue(ctx, w, row)
	}
	out := recipeJSONContextWriter{ctx: ctx, w: w}
	if ctx == nil || w == nil {
		return fmt.Errorf("row streaming requires context and writer")
	}
	if _, err := out.Write([]byte("{")); err != nil {
		return err
	}
	names := make([]string, 0, len(row))
	for name := range row {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if i > 0 {
			if _, err := out.Write([]byte(",")); err != nil {
				return err
			}
		}
		if err := recipeWriteJSONValue(ctx, out, name); err != nil {
			return err
		}
		if _, err := out.Write([]byte(":")); err != nil {
			return err
		}
		if err := recipeWriteJSONValue(ctx, out, row[name]); err != nil {
			return err
		}
	}
	_, err := out.Write([]byte("}"))
	return err
}

func recipeWriteStoreRowJSON(ctx context.Context, w io.Writer, key string, row map[string]any) error {
	if ctx == nil || w == nil {
		return fmt.Errorf("stored row streaming requires context and writer")
	}
	out := recipeJSONContextWriter{ctx: ctx, w: w}
	if _, err := out.Write([]byte(`{"key":`)); err != nil {
		return err
	}
	if err := recipeWriteJSONValue(ctx, out, key); err != nil {
		return err
	}
	if _, err := out.Write([]byte(`,"value":`)); err != nil {
		return err
	}
	if err := recipeWriteRowJSON(ctx, out, row); err != nil {
		return err
	}
	_, err := out.Write([]byte("}"))
	return err
}
