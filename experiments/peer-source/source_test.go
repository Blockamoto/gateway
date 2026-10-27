package peersource

import (
	"context"
	"errors"
	"testing"
)

type authorityFunc func(context.Context, uint64) (Anchor, error)

func (f authorityFunc) Selected(c context.Context, h uint64) (Anchor, error) { return f(c, h) }

type policyFunc func(context.Context) (Consent, error)

func (f policyFunc) Current(c context.Context) (Consent, error) { return f(c) }

type providerFunc func(context.Context, Anchor) ([]byte, error)

func (f providerFunc) Fetch(c context.Context, a Anchor) ([]byte, error) { return f(c, a) }

type verifierFunc func(context.Context, Anchor, []byte) error

func (f verifierFunc) Verify(c context.Context, a Anchor, b []byte) error { return f(c, a, b) }

func mockFixture() (Anchor, []byte) {
	var a Anchor
	a.Height = 12
	a.Revision = 1
	for i := range a.Header {
		a.Header[i] = byte(i)
	}
	a.Hash = hashHeader(a.Header)
	raw := append(append([]byte{}, a.Header[:]...), 1)
	return a, raw
}
func fixtureService(t *testing.T, anchor *Anchor, consent *Consent, verify verifierFunc, sources ...Source) *Service {
	t.Helper()
	svc, err := New(authorityFunc(func(_ context.Context, _ uint64) (Anchor, error) { return *anchor, nil }), policyFunc(func(context.Context) (Consent, error) { return *consent, nil }), verify, sources)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}
func TestPeerAcquisitionAndDefensiveCopies(t *testing.T) {
	a, raw := mockFixture()
	policy := Consent{Revision: 1, Network: true}
	calls := 0
	svc := fixtureService(t, &a, &policy, func(_ context.Context, _ Anchor, b []byte) error { calls++; b[80] = 99; return nil }, Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { return raw, nil })})
	result, err := svc.Acquire(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.SourceKind() != BitcoinPeer || result.Raw()[80] != 1 {
		t.Fatalf("bad result/copy, calls %d", calls)
	}
	raw[80] = 42
	copy := result.Raw()
	copy[80] = 33
	if result.Raw()[80] != 1 {
		t.Fatal("source or caller mutated acquired block")
	}
	if err = svc.CheckPublication(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	other := fixtureService(t, &a, &policy, func(context.Context, Anchor, []byte) error { return nil })
	if err = other.CheckPublication(context.Background(), result); err == nil {
		t.Fatal("accepted block issued by different service")
	}
}
func TestDisabledRemoteAndCoreSourcesNeverCalled(t *testing.T) {
	a, raw := mockFixture()
	policy := Consent{Revision: 1}
	var peer, core, local int
	svc := fixtureService(t, &a, &policy, func(context.Context, Anchor, []byte) error { return nil },
		Source{Core, providerFunc(func(context.Context, Anchor) ([]byte, error) { core++; return raw, nil })},
		Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { peer++; return raw, nil })},
		Source{Local, providerFunc(func(context.Context, Anchor) ([]byte, error) { local++; return raw, nil })})
	b, err := svc.Acquire(context.Background(), 12)
	if err != nil || b.SourceKind() != Local || peer != 0 || core != 0 || local != 1 {
		t.Fatalf("result=%v err=%v calls %d/%d/%d", b.SourceKind(), err, peer, core, local)
	}
}
func TestInvalidSourceAndVerifierFailover(t *testing.T) {
	a, raw := mockFixture()
	policy := Consent{Revision: 1, Network: true}
	calls := 0
	mismatch := append([]byte(nil), raw...)
	mismatch[0]++
	svc := fixtureService(t, &a, &policy, func(_ context.Context, _ Anchor, b []byte) error {
		calls++
		if b[80] == 2 {
			return errors.New("invalid witness")
		}
		return nil
	},
		Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { return mismatch, nil })},
		Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { return make([]byte, MaxBlockBytes+1), nil })},
		Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) {
			x := append([]byte(nil), raw...)
			x[80] = 2
			return x, nil
		})},
		Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { return raw, nil })})
	if _, err := svc.Acquire(context.Background(), 12); err != nil || calls != 2 {
		t.Fatalf("failover err=%v verifier calls=%d", err, calls)
	}
}
func TestStaleAnchorAndConsentFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Anchor, *Consent)
		want   error
	}{
		{"reorg", func(a *Anchor, c *Consent) { a.Revision++ }, ErrAnchorChanged},
		{"revoked", func(a *Anchor, c *Consent) { c.Network = false; c.Revision++ }, ErrPolicyChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, raw := mockFixture()
			policy := Consent{Revision: 1, Network: true}
			svc := fixtureService(t, &a, &policy, func(context.Context, Anchor, []byte) error { tc.change(&a, &policy); return nil }, Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { return raw, nil })})
			if _, err := svc.Acquire(context.Background(), 12); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}
func TestPublicationRechecksAndCancellation(t *testing.T) {
	a, raw := mockFixture()
	policy := Consent{Revision: 1, Network: true}
	svc := fixtureService(t, &a, &policy, func(context.Context, Anchor, []byte) error { return nil }, Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { return raw, nil })})
	b, err := svc.Acquire(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	policy.Revision++
	policy.Network = false
	if !errors.Is(svc.CheckPublication(context.Background(), b), ErrPolicyChanged) {
		t.Fatal("revocation was ignored")
	}
	policy.Revision--
	policy.Network = true
	a.Revision++
	if !errors.Is(svc.CheckPublication(context.Background(), b), ErrAnchorChanged) {
		t.Fatal("changed anchor was ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = svc.Acquire(ctx, 12); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire: %v", err)
	}
	if err = svc.CheckPublication(ctx, b); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled publication: %v", err)
	}
}
func TestInvalidAnchorAndExplicitUnavailable(t *testing.T) {
	a, raw := mockFixture()
	policy := Consent{Revision: 1, Network: true}
	calls := 0
	svc := fixtureService(t, &a, &policy, func(context.Context, Anchor, []byte) error { return nil }, Source{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { calls++; return raw, nil })})
	a.Hash[0] ^= 1
	if _, err := svc.Acquire(context.Background(), 12); !errors.Is(err, ErrInvalidAnchor) || calls != 0 {
		t.Fatalf("invalid anchor err=%v calls=%d", err, calls)
	}
	a, _ = mockFixture()
	policy.Network = false
	if _, err := svc.Acquire(context.Background(), 12); !errors.Is(err, ErrUnavailable) || calls != 0 {
		t.Fatalf("disabled result err=%v calls=%d", err, calls)
	}
	if _, err := New(nil, policyFunc(func(context.Context) (Consent, error) { return policy, nil }), verifierFunc(func(context.Context, Anchor, []byte) error { return nil }), nil); err == nil {
		t.Fatal("nil authority accepted")
	}
}
func TestAnchorChangeBeforeFetchStopsProvider(t *testing.T) {
	a, raw := mockFixture()
	selectedCalls, providerCalls := 0, 0
	authority := authorityFunc(func(context.Context, uint64) (Anchor, error) {
		selectedCalls++
		if selectedCalls == 2 {
			a.Revision++
		}
		return a, nil
	})
	svc, err := New(authority, policyFunc(func(context.Context) (Consent, error) { return Consent{Revision: 1, Network: true}, nil }), verifierFunc(func(context.Context, Anchor, []byte) error { return nil }), []Source{{BitcoinPeer, providerFunc(func(context.Context, Anchor) ([]byte, error) { providerCalls++; return raw, nil })}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Acquire(context.Background(), 12); !errors.Is(err, ErrAnchorChanged) || providerCalls != 0 {
		t.Fatalf("err=%v provider calls=%d", err, providerCalls)
	}
}
