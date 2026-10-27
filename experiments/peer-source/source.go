// Package peersource is an experimental acquisition boundary. It contains no
// network transport, Bitcoin verifier, persistence, or publication mechanism.
package peersource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

const MaxBlockBytes = 4_000_000

var (
	ErrUnavailable   = errors.New("no permitted source supplied a verified block")
	ErrPolicyChanged = errors.New("source policy changed during acquisition")
	ErrAnchorChanged = errors.New("selected block anchor changed")
	ErrInvalidAnchor = errors.New("selected anchor has an inconsistent header hash")
)

// Kind is assigned by trusted configuration, not learned from the source.
type Kind uint8

const (
	Local Kind = iota + 1
	Core
	BitcoinPeer
)

// Anchor is an independently selected chain identity. Hash is double-SHA256
// in internal byte order, not the conventional reversed display form. Revision
// changes whenever the authority changes selected-chain state.
type Anchor struct {
	Height   uint64
	Hash     [32]byte
	Header   [80]byte
	Revision uint64
}

type Authority interface {
	Selected(context.Context, uint64) (Anchor, error)
}

// Consent is independently managed current user policy. Revision must change
// whenever relevant source permissions change. Network covers peer access;
// Core controls the separate optional local Core provider.
type Consent struct {
	Revision uint64
	Network  bool
	Core     bool
}
type Policy interface {
	Current(context.Context) (Consent, error)
}

// A provider owns its returned buffer until Fetch returns; implementations must
// respect context and must not mutate a returned buffer. Results are untrusted.
type Provider interface {
	Fetch(context.Context, Anchor) ([]byte, error)
}
type Source struct {
	Kind     Kind
	Provider Provider
}

// Verifier is trusted application code, not a statement supplied by a peer. It
// must verify complete body integrity for the target, including transaction
// commitments and witness commitments when applicable. This package does not
// implement or independently check those rules.
type Verifier interface {
	Verify(context.Context, Anchor, []byte) error
}

type Service struct {
	authority Authority
	policy    Policy
	verifier  Verifier
	sources   []Source
}

type Block struct {
	issuer          *Service
	anchor          Anchor
	raw             []byte
	kind            Kind
	consentRevision uint64
}

func (b Block) Anchor() Anchor   { return b.anchor }
func (b Block) SourceKind() Kind { return b.kind }
func (b Block) Raw() []byte      { return append([]byte(nil), b.raw...) }

func New(authority Authority, policy Policy, verifier Verifier, sources []Source) (*Service, error) {
	if authority == nil || policy == nil || verifier == nil {
		return nil, errors.New("authority, policy and verifier are required")
	}
	copySources := append([]Source(nil), sources...)
	for _, s := range copySources {
		if s.Provider == nil || (s.Kind != Local && s.Kind != Core && s.Kind != BitcoinPeer) {
			return nil, errors.New("invalid source configuration")
		}
	}
	return &Service{authority: authority, policy: policy, verifier: verifier, sources: copySources}, nil
}
func enabled(c Consent, k Kind) bool {
	switch k {
	case Local:
		return true
	case Core:
		return c.Core
	case BitcoinPeer:
		return c.Network
	}
	return false
}
func hashHeader(h [80]byte) [32]byte { first := sha256.Sum256(h[:]); return sha256.Sum256(first[:]) }
func validateAnchor(a Anchor, height uint64) error {
	if a.Height != height || hashHeader(a.Header) != a.Hash {
		return ErrInvalidAnchor
	}
	return nil
}
func sameAnchor(a, b Anchor) bool { return a == b }

// Acquire checks source permissions before each fetch and rechecks the exact
// anchor and policy revision after verification. It performs no retention,
// index work, peer discovery, networking, or UI operations by itself.
func (s *Service) Acquire(ctx context.Context, height uint64) (Block, error) {
	if err := ctx.Err(); err != nil {
		return Block{}, err
	}
	anchor, err := s.authority.Selected(ctx, height)
	if err != nil {
		return Block{}, err
	}
	if err = validateAnchor(anchor, height); err != nil {
		return Block{}, err
	}
	for _, source := range s.sources {
		if err = ctx.Err(); err != nil {
			return Block{}, err
		}
		before, e := s.policy.Current(ctx)
		if e != nil {
			return Block{}, e
		}
		if !enabled(before, source.Kind) {
			continue
		}
		selected, e := s.authority.Selected(ctx, height)
		if e != nil {
			return Block{}, e
		}
		if !sameAnchor(anchor, selected) {
			return Block{}, ErrAnchorChanged
		}
		if err = ctx.Err(); err != nil {
			return Block{}, err
		}
		data, e := source.Provider.Fetch(ctx, anchor)
		if err = ctx.Err(); err != nil {
			return Block{}, err
		}
		if e != nil {
			continue
		}
		// Copy before checking. The size cap excludes a provider retaining or
		// allocating unbounded data itself; the provider must enforce its own
		// network framing and read bounds before returning.
		if len(data) < 81 || len(data) > MaxBlockBytes {
			continue
		}
		raw := append([]byte(nil), data...)
		if !bytes.Equal(raw[:80], anchor.Header[:]) {
			continue
		}
		if e = s.verifier.Verify(ctx, anchor, append([]byte(nil), raw...)); e != nil {
			if err = ctx.Err(); err != nil {
				return Block{}, err
			}
			continue
		}
		if err = ctx.Err(); err != nil {
			return Block{}, err
		}
		after, e := s.policy.Current(ctx)
		if e != nil {
			return Block{}, e
		}
		if after.Revision != before.Revision || !enabled(after, source.Kind) {
			return Block{}, ErrPolicyChanged
		}
		selected, e = s.authority.Selected(ctx, height)
		if e != nil {
			return Block{}, e
		}
		if !sameAnchor(anchor, selected) {
			return Block{}, ErrAnchorChanged
		}
		if err = ctx.Err(); err != nil {
			return Block{}, err
		}
		return Block{issuer: s, anchor: anchor, raw: raw, kind: source.Kind, consentRevision: after.Revision}, nil
	}
	return Block{}, ErrUnavailable
}

// CheckPublication is an optimistic recheck for a caller-owned publication
// transaction. It is not atomic with a subsequent write; production commit
// must coordinate the chain/policy fence with its own durable transaction.
func (s *Service) CheckPublication(ctx context.Context, b Block) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.issuer != s || len(b.raw) < 81 || len(b.raw) > MaxBlockBytes || validateAnchor(b.anchor, b.anchor.Height) != nil {
		return fmt.Errorf("invalid acquired block")
	}
	selected, err := s.authority.Selected(ctx, b.anchor.Height)
	if err != nil {
		return err
	}
	if !sameAnchor(selected, b.anchor) {
		return ErrAnchorChanged
	}
	policy, err := s.policy.Current(ctx)
	if err != nil {
		return err
	}
	if policy.Revision != b.consentRevision || !enabled(policy, b.kind) {
		return ErrPolicyChanged
	}
	return ctx.Err()
}
