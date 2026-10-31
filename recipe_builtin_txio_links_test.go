package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRecipeTXIOLinksSemanticBinding(t *testing.T) {
	if _, err := builtinTXIOLinksRecipe(); err != nil {
		t.Fatal(err)
	}
	if err := recipeTXIOLinksCheckEquivalence(builtinTXIOLinksSemantic); err != nil {
		t.Fatal(err)
	}
	d, err := findIndexDefinition("txo-spender")
	if err != nil || !bytes.Equal(d.ExecutableRecipe, builtinTXIOLinksJSON) || len(d.ExecutableRecipeVariants) != 7 {
		t.Fatal("native inspector lost the full executable program", err)
	}
}

func TestRecipeTXIOLinksCompletePhysicalParityAndProviderOrder(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	for _, withFacts := range []bool{false, true} {
		left, err := openIndexStore(t.TempDir(), "txo-spender")
		if err != nil {
			t.Fatal(err)
		}
		right, err := openIndexStore(t.TempDir(), "txo-spender")
		if err != nil {
			t.Fatal(err)
		}
		left.retainOutputValues, left.retainOutputScripts = withFacts, withFacts
		right.retainOutputValues, right.retainOutputScripts = withFacts, withFacts
		actual, err := left.deriveTXIOBlock(block)
		if err != nil {
			t.Fatal(err)
		}
		want, err := right.deriveTXIOBlockOracle(block)
		if err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatal("complete TXIO physical/reference parity", withFacts, err)
		}
	}
	fixture := inscriptionTestBlock(transactionView{TxID: testID(669001), Inputs: []inputView{{PrevTxID: testID(669000)}}, Outputs: []outputView{{N: 0}}})
	prepared := &indexRunPrepared{block: fixture}
	cohort := context.WithValue(ctx, indexRunPreparedKey{}, prepared)
	runRememberPrevout(cohort, 1, 0, utxoCoin{TxID: testID(669000), Vout: 0, Position: outputCoordinate(0, 0, 0), BlockHash: genesisHashDisplay})
	for _, oracle := range []bool{false, true} {
		store, err := openIndexStore(t.TempDir(), "txo-spender")
		if err != nil {
			t.Fatal(err)
		}
		store.locatorContext = cohort
		calls := 0
		store.spenderOrigin = func(context.Context, inputView, int64, int) (txioCreation, error) {
			calls++
			return txioCreation{}, errors.New("should not probe lower-priority provider")
		}
		var out *indexSpenderBlock
		if oracle {
			out, err = store.deriveTXIOBlockOracle(fixture)
		} else {
			out, err = store.deriveTXIOBlock(fixture)
		}
		if err != nil || calls != 0 || len(out.Rows) != 1 || out.Rows[0].Output != outputCoordinate(0, 0, 0) {
			t.Fatal("JSON did not preserve lazy cohort precedence", oracle, calls, out, err)
		}
	}
}

func TestRecipeTXIOLinksPendingAndProviderErrors(t *testing.T) {
	block := inscriptionTestBlock(transactionView{TxID: testID(669021), Inputs: []inputView{{PrevTxID: testID(669020)}}, Outputs: []outputView{{N: 0}}})
	for _, mode := range []string{"unavailable", "future", "cancelled"} {
		left, err := openIndexStore(t.TempDir(), "txo-spender")
		if err != nil {
			t.Fatal(err)
		}
		right, err := openIndexStore(t.TempDir(), "txo-spender")
		if err != nil {
			t.Fatal(err)
		}
		makeProvider := func(store *indexStore) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			store.locatorContext = ctx
			store.spenderOrigin = func(context.Context, inputView, int64, int) (txioCreation, error) {
				switch mode {
				case "future":
					return txioCreation{outputCoordinate(0, 0, block.Height+1), testID(669029)}, nil
				case "cancelled":
					cancel()
					return txioCreation{}, ctx.Err()
				default:
					return txioCreation{}, errors.New("provider not available")
				}
			}
		}
		makeProvider(left)
		makeProvider(right)
		actual, aerr := left.deriveTXIOBlock(block)
		want, werr := right.deriveTXIOBlockOracle(block)
		if (aerr == nil) != (werr == nil) {
			t.Fatal("provider failure semantics changed", mode, aerr, werr)
		}
		if mode == "unavailable" && (aerr != nil || !reflect.DeepEqual(actual, want) || len(actual.PendingTranslations) != 1) {
			t.Fatal("missing proof was guessed or dropped", actual, want, aerr)
		}
		if mode == "cancelled" && (!errors.Is(aerr, context.Canceled) || actual != nil) {
			t.Fatal("cancellation exposed partial TXIO", aerr)
		}
	}
}
