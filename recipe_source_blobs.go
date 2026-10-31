package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

// Source evidence is separate from VM values. A checked proof may include raw
// blocks larger than one logical value; immutable refs keep working reads bounded.
type recipeSourceBlob struct {
	Role  string
	Hash  string
	Bytes int64
}
type recipeSourceBlobInput struct {
	Ref  recipeSourceBlob
	Path string
}

func recipeSourceBlobPath(root, hash string) string {
	return filepath.Join(root, "source-blobs", hash[:2], hash+".blob")
}
func recipeSourceFileDigest(ctx context.Context, source io.Reader) (string, error) {
	hash := sha256.New()
	if n, err := io.CopyBuffer(recipeJSONContextWriter{ctx: ctx, w: hash}, io.LimitReader(source, parcelReplayMaxBytes079+1), make([]byte, 64<<10)); err != nil {
		return "", err
	} else if n > parcelReplayMaxBytes079 {
		return "", fmt.Errorf("source evidence exceeds bounded extent")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func recipeCheckSourceBlobRef(ref recipeSourceBlob) error {
	if !validHash(ref.Hash) || ref.Bytes < 1 || ref.Bytes > parcelReplayMaxBytes079 || (ref.Role != "block" && ref.Role != "receipt" && ref.Role != "attestation") {
		return fmt.Errorf("invalid owned source blob reference")
	}
	return nil
}
func recipeVerifySourceBlob(ctx context.Context, root string, ref recipeSourceBlob) error {
	if err := recipeCheckSourceBlobRef(ref); err != nil {
		return err
	}
	path := recipeSourceBlobPath(root, ref.Hash)
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	return recipeSparseVerifyFile(ctx, path, ref.Hash, ref.Bytes)
}

// Copy, rather than link, producer files. Publication never overwrites existing
// evidence and a producer's removal or in-place damage cannot alter this copy.
func recipeOwnSourceBlob(ctx context.Context, root string, input recipeSourceBlobInput) error {
	if err := recipeCheckSourceBlobRef(input.Ref); err != nil {
		return err
	}
	path := recipeSourceBlobPath(root, input.Ref.Hash)
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return recipeVerifySourceBlob(ctx, root, input.Ref)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := updateapply.CheckPath(input.Path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	source, err := os.Open(input.Path)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != input.Ref.Bytes {
		return fmt.Errorf("source blob extent changed")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".source-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	hash := sha256.New()
	n, err := io.CopyBuffer(recipeJSONContextWriter{ctx: ctx, w: io.MultiWriter(f, hash)}, io.LimitReader(source, input.Ref.Bytes+1), make([]byte, 64<<10))
	if err == nil && (n != input.Ref.Bytes || hex.EncodeToString(hash.Sum(nil)) != input.Ref.Hash) {
		err = fmt.Errorf("source blob changed during copy")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Link(f.Name(), path); err != nil {
		if check := recipeVerifySourceBlob(ctx, root, input.Ref); check != nil {
			return fmt.Errorf("publish owned source evidence: %w", err)
		}
	}
	return syncDirectory(filepath.Dir(path))
}
