package main

import (
	"context"
	"sort"
)

type recipeLineageHead struct {
	Identity recipeGraphIdentity `json:"identity"`
	Revision uint64              `json:"revision"`
	Root     string              `json:"root"`
	Checksum string              `json:"checksum"`
}

// Caller holds recipeStoreMutex. This is an in-process consistency token,
// not a signature or selected-chain proof. Fenced ancestors are unavailable.
func (p *recipeLineagePlan) headTokenLocked(ctx context.Context) (string, error) {
	ids := make([]recipeGraphIdentity, 0, len(p.nodes)-1)
	for id := range p.nodes {
		if id != p.root {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if a.Instance != b.Instance {
			return a.Instance < b.Instance
		}
		if a.Revision != b.Revision {
			return a.Revision < b.Revision
		}
		return a.SemanticHash < b.SemanticHash
	})
	heads := make([]recipeLineageHead, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		node := p.nodes[id]
		if err := node.store.rejectReconcileFenceLocked(); err != nil {
			return "", err
		}
		head, err := node.store.head()
		if err != nil {
			return "", err
		}
		heads = append(heads, recipeLineageHead{Identity: id, Revision: head.Revision, Root: head.Root, Checksum: head.Checksum})
	}
	return indexDigest(struct {
		Domain string              `json:"domain"`
		Root   recipeGraphIdentity `json:"root"`
		Heads  []recipeLineageHead `json:"heads"`
	}{"gateway-recipe-dag-lineage-token-v1", p.root, heads}), nil
}
