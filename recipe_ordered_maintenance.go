package main

import "context"

// Maintenance runs at committed boundaries. The same work slot used by Apply
// excludes unpublished forward COW/fact work; generation ownership excludes
// concurrent publication, query and rebuild while taking the pin snapshot.
// Native retention/GC formats and cadence are reused.
func (g *recipeOrderedGeneration) maintain(ctx context.Context) error {
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	s, _, err := g.inspect(ctx)
	if err != nil || s == nil || s.head == nil {
		return err
	}
	pins := map[string]bool{}
	if err = s.visitCurrentFactPages(ctx, func(hash string) error { pins[hash] = true; return nil }); err != nil {
		return err
	}
	if err = collectOutputFactPagesContext(ctx, g.profileRoot, pins); err != nil {
		return err
	}
	return s.collectPages(ctx)
}

func (c *recipeOrderedRunConsumer) Maintain(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation.maintain(ctx)
}
