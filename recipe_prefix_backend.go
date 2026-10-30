package main

import "context"

// Prefix scheduling depends on an authenticated position/token and retained
// recovery points, independently of the physical Coin or Sat row codec.
type recipePrefixPoint struct {
	Height                       int64
	BlockHash, Commitment, Token string
}

type recipePrefixBackend interface {
	create(context.Context) error
	prefixSnapshot(context.Context) (recipePrefixPoint, bool, error)
	prefixRetained(context.Context) ([]recipePrefixPoint, error)
	reviewToken(context.Context) (string, error)
	resetForRebuild(context.Context, string, string, recipeStoreGuard) error
	apply(context.Context, blockView, recipeStoreGuard) error
	applyFromToken(context.Context, blockView, string, recipeStoreGuard) error
	reconcile(context.Context, int64, func(context.Context, int64) (string, error), recipeStoreGuard) error
	maintain(context.Context) error
}

func recipePrefixSource(source string) bool {
	return source == "bitcoin.ordered" || source == recipeSatSource
}

func openRecipePrefixBackend(root string, program *recipeProgram) (recipePrefixBackend, error) {
	if program != nil && program.definition.Source == recipeSatSource {
		return openRecipeSatGeneration(root, program)
	}
	return openRecipeOrderedGeneration(root, program)
}

func (g *recipeOrderedGeneration) prefixSnapshot(ctx context.Context) (recipePrefixPoint, bool, error) {
	point := recipePrefixPoint{Height: -1, Token: g.origin().Checksum}
	s, initialized, err := g.inspect(ctx)
	if err != nil || !initialized || s.head == nil {
		return point, initialized, err
	}
	cp := s.head.Checkpoint
	return recipePrefixPoint{Height: cp.Height, BlockHash: cp.BlockHash, Commitment: cp.Commitment, Token: s.head.Checksum}, true, nil
}

func (g *recipeOrderedGeneration) prefixRetained(ctx context.Context) ([]recipePrefixPoint, error) {
	s, err := g.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if s.head == nil {
		return nil, nil
	}
	points, err := s.retainedCheckpoints(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]recipePrefixPoint, 0, len(points))
	for _, cp := range points {
		result = append(result, recipePrefixPoint{Height: cp.Height, BlockHash: cp.BlockHash, Commitment: cp.Commitment})
	}
	return result, nil
}
