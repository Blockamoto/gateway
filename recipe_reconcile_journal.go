package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

var errRecipeReconcileFenced = errors.New("recipe coverage is being reconciled")

type recipeReconcileFence struct {
	Schema      int    `json:"schema"`
	Instance    string `json:"instance"`
	Fingerprint string `json:"fingerprint"`
	From        int64  `json:"from"`
	To          int64  `json:"to"`
	Checksum    string `json:"checksum"`
}

func recipeReconcileFenceDigest(f recipeReconcileFence) string {
	f.Checksum = ""
	b, _ := json.Marshal(f)
	return recipeStoreHash(b)
}
func (s *recipeStore) reconcileFencePath() string { return filepath.Join(s.dir, "reconcile.json") }

// The caller holds recipeStoreMutex. A malformed marker is never ignored.
func (s *recipeStore) readReconcileFenceLocked() (recipeReconcileFence, error) {
	var f recipeReconcileFence
	b, err := recipeStoreReadBounded(s.reconcileFencePath(), recipeStoreHeadMaxBytes)
	if err != nil {
		return f, err
	}
	if err = json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("invalid recipe reconciliation fence: %w", err)
	}
	if f.Schema != 1 || f.Instance != s.instance || f.Fingerprint != s.fingerprint || f.From < 0 || f.To < f.From || f.Checksum != recipeReconcileFenceDigest(f) {
		return f, fmt.Errorf("invalid recipe reconciliation fence")
	}
	return f, nil
}
func (s *recipeStore) rejectReconcileFenceLocked() error {
	_, err := s.readReconcileFenceLocked()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return errRecipeReconcileFenced
}
func (s *recipeStore) inspectReconcileFence() (recipeReconcileFence, bool, error) {
	recipeStoreMutex.Lock()
	defer recipeStoreMutex.Unlock()
	f, err := s.readReconcileFenceLocked()
	if os.IsNotExist(err) {
		return f, false, nil
	}
	return f, err == nil, err
}

// A fence is installed only after a complete read-only audit. Ordinary reads,
// writes and dependency reads remain unavailable until guarded completion.
func (s *recipeStore) beginReconcileFence(ctx context.Context, revision uint64, from, to int64, guard recipeStoreGuard) error {
	if ctx == nil || from < 0 || to < from {
		return fmt.Errorf("invalid recipe reconciliation window")
	}
	return recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		head, err := s.head()
		if err != nil {
			return err
		}
		if head.Revision != revision {
			return fmt.Errorf("recipe coverage changed since reconciliation audit")
		}
		previous, err := s.readReconcileFenceLocked()
		if err == nil {
			if from > previous.From || to < previous.To {
				return fmt.Errorf("reconciliation recovery requires the previously fenced range")
			}
			if from == previous.From && to == previous.To {
				return nil
			}
			previous.From, previous.To = from, to
			previous.Checksum = recipeReconcileFenceDigest(previous)
			b, e := json.Marshal(previous)
			if e != nil {
				return e
			}
			return atomicWriteBytes(s.reconcileFencePath(), b)
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err = updateapply.CheckPath(s.reconcileFencePath()); err != nil {
			return err
		}
		if err = os.MkdirAll(s.dir, 0700); err != nil {
			return err
		}
		fence := recipeReconcileFence{Schema: 1, Instance: s.instance, Fingerprint: s.fingerprint, From: from, To: to}
		fence.Checksum = recipeReconcileFenceDigest(fence)
		b, err := json.Marshal(fence)
		if err != nil {
			return err
		}
		return atomicWriteBytes(s.reconcileFencePath(), b)
	})
}
func (s *recipeStore) finishReconcileFence(ctx context.Context, revision uint64, from, to int64, guard recipeStoreGuard) error {
	if ctx == nil {
		return fmt.Errorf("missing context")
	}
	return recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		fence, err := s.readReconcileFenceLocked()
		if err != nil {
			return err
		}
		if from > fence.From || to < fence.To {
			return fmt.Errorf("reconciliation recovery requires the previously fenced range")
		}
		head, err := s.head()
		if err != nil {
			return err
		}
		if head.Revision != revision {
			return fmt.Errorf("recipe coverage changed during reconciliation")
		}
		if err = updateapply.CheckPath(s.reconcileFencePath()); err != nil {
			return err
		}
		if err = os.Remove(s.reconcileFencePath()); err != nil {
			return err
		}
		return syncDirectory(s.dir)
	})
}

// The caller is recovering an existing fence; this does not expose coverage.
func (s *recipeStore) reconcileFenceRevision() (uint64, error) {
	recipeStoreMutex.Lock()
	defer recipeStoreMutex.Unlock()
	if _, err := s.readReconcileFenceLocked(); err != nil {
		return 0, err
	}
	head, err := s.head()
	return head.Revision, err
}
