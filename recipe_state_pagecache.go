package main

// Shared only within one verification call. Every page is path-checked and
// hash-verified on first load; discarding the cache between calls preserves
// detection of later on-disk corruption. recipeStageMu serializes the call.
import "context"

const recipeStageCacheMaxPages = 64
const recipeStageCacheMaxBytes = 16 << 20

type recipeStageVerifyCache struct {
	store *recipeStageStore
	ctx   context.Context
	owner *ordinalTree
}

func newRecipeStageVerifyCache(s *recipeStageStore, ctx context.Context) *recipeStageVerifyCache {
	return &recipeStageVerifyCache{store: s, ctx: ctx, owner: s.tree(ctx, "")}
}
func (c *recipeStageVerifyCache) tree(root string) *ordinalTree {
	t := c.store.tree(c.ctx, root)
	t.cache = c.owner.cache
	t.cacheOrder = c.owner.cacheOrder
	t.cacheEntries = c.owner.cacheEntries
	checkPath := t.checkPath
	t.checkPath = func(path string) error {
		// Reserve the 8 MiB encoded limit plus generous decoded allocation
		// overhead before loading, apart from the bounded temporary read buffer.
		c.trimTo(recipeStageCacheMaxPages-1, recipeStageCacheMaxBytes-(12<<20))
		return checkPath(path)
	}
	return t
}
func recipeStagePageBytes(p ordinalPage) int64 {
	n := int64(256 + cap(p.Entries)*64 + cap(p.Children)*32)
	for _, v := range p.Entries {
		n += int64(len(v.Key) + cap(v.Value))
	}
	for _, v := range p.Children {
		n += int64(len(v))
	}
	return n
}

// Retained cache entries are capped by count and estimated decoded bytes.
// Existing per-page size/hash checks remain in force on each first load.
func (c *recipeStageVerifyCache) trim() {
	c.trimTo(recipeStageCacheMaxPages, recipeStageCacheMaxBytes)
}
func (c *recipeStageVerifyCache) trimTo(maxPages int, maxBytes int64) {
	var bytes int64
	for _, p := range c.owner.cache {
		bytes += recipeStagePageBytes(p)
	}
	for len(c.owner.cache) > maxPages || bytes > maxBytes {
		old := c.owner.cacheOrder.Back()
		if old == nil {
			break
		}
		key := old.Value.(string)
		bytes -= recipeStagePageBytes(c.owner.cache[key])
		delete(c.owner.cache, key)
		delete(c.owner.cacheEntries, key)
		c.owner.cacheOrder.Remove(old)
	}
}
func (c *recipeStageVerifyCache) get(t *ordinalTree, k recipeStateKey) (recipeStateValue, error) {
	v, e := recipeStageGet(t, k)
	c.trim()
	return v, e
}
func (c *recipeStageVerifyCache) journalGet(t *ordinalTree, k recipeStateKey) (recipeStageJournalEntry, bool, error) {
	v, ok, e := recipeStageJournalGet(t, k)
	c.trim()
	return v, ok, e
}
