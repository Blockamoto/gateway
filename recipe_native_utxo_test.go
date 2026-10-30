package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func compareNativeRecipeUTXO(t *testing.T, old, new *utxoStore) {
	t.Helper()
	if !reflect.DeepEqual(old.Head(), new.Head()) {
		t.Fatalf("native checkpoint differs:\nold=%+v\nnew=%+v", old.Head(), new.Head())
	}
	if old.Head() != nil && old.Head().Journal != "" {
		a, e1 := old.readJournal(context.Background(), *old.Head())
		b, e2 := new.readJournal(context.Background(), *new.Head())
		if e1 != nil || e2 != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("native undo differs: %v %v", e1, e2)
		}
	}
}

func TestRecipeNativeUTXOSemanticEquivalence(t *testing.T) {
	p, err := loadBuiltinUTXOProgram()
	if err != nil {
		t.Fatal(err)
	}
	if err = recipeNativeUTXOCheckEquivalence(builtinUTXOSemantic, utxoCheckpointSchema); err != nil {
		t.Fatalf("%s: %v", builtinUTXOSemantic, err)
	}
	definition, err := findIndexDefinition("utxo-state")
	if err != nil || definition.ExecutableRecipeHash != builtinUTXOSemantic || string(definition.ExecutableRecipe) != string(builtinUTXOProgramJSON) {
		t.Fatalf("catalog does not expose actual UTXO program: %v", err)
	}
	for _, change := range []string{"program", "stream", "source", "bitcoin", "physical", "schema"} {
		program, err := parseOrderedRecipeProgram(builtinUTXOProgramJSON, true)
		if err != nil {
			t.Fatal(err)
		}
		stream, source, bitcoin, physical, schema := recipeOrderedStreamProfile, recipeVerifiedEventProfile+"/native-admitted-facts-v1", "gateway-bitcoin-primitives-v1", "gateway-utxo-cow-facts-schema2-v1", utxoCheckpointSchema
		switch change {
		case "program":
			program.Steps[0].Body = p.Steps[0].Body[:len(p.Steps[0].Body)-1]
		case "stream":
			stream += "-changed"
		case "source":
			source += "-changed"
		case "bitcoin":
			bitcoin += "-changed"
		case "physical":
			physical += "-changed"
		case "schema":
			schema++
		}
		semantic := recipeNativeUTXOSemantic(program, stream, source, bitcoin, physical)
		if recipeNativeUTXOCheckEquivalence(semantic, schema) == nil {
			t.Fatalf("changed %s reused native generation", change)
		}
	}
}

func TestRecipeNativeUTXOExactPhysicalParityAndRecovery(t *testing.T) {
	ctx := context.Background()
	old, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	next, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := utxoFixtureBlock(0, "", utxoFixtureCoinbase(77000, 50))
	cb := utxoFixtureCoinbase(77001, 10, 20, 3, 4)
	cb.Outputs[2].ScriptPubKey = "6a"
	cb.Outputs[3].ScriptPubKey = strings.Repeat("51", 10001)
	b1 := utxoFixtureBlock(1, g.Hash, cb)
	spend := utxoFixtureSpend(77003, []inputView{{PrevTxID: cb.TxID}}, 6, 3)
	inBlock := utxoFixtureSpend(77004, []inputView{{PrevTxID: spend.TxID}}, 5)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77002, 1), spend, inBlock)
	for _, block := range []blockView{g, b1, b2} {
		if err = old.applyBlockLegacyOracle(ctx, block); err != nil {
			t.Fatal(err)
		}
		if err = next.applyBlockRecipe(ctx, block); err != nil {
			t.Fatalf("recipe height %d: %v", block.Height, err)
		}
		compareNativeRecipeUTXO(t, old, next)
	}
	next, err = openUTXOStoreContext(ctx, next.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*utxoStore{old, next} {
		if err = store.Reconcile(ctx, func(_ context.Context, height int64) (string, error) {
			if height == 2 {
				return testID(999900), nil
			}
			return b1.Hash, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	compareNativeRecipeUTXO(t, old, next)
	before := next.Head().Commitment
	bad := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77005, 1), utxoFixtureSpend(77006, []inputView{{PrevTxID: cb.TxID}}, 11))
	if err = next.applyBlockRecipe(ctx, bad); err == nil || next.Head().Commitment != before {
		t.Fatal("invalid recipe transition published", err)
	}
}

func TestRecipeNativeUTXOBIP30ExactJournalParity(t *testing.T) {
	for _, exception := range []struct {
		height int64
		hash   string
	}{{91842, "00000000000a4d0a398161ffc163c503763b1f4360639393e0e4c8e300e0caec"}, {91880, "00000000000743f190a18c5577a3c2d2a1f610ae9601ac046a38084ccb7cd721"}} {
		for _, scenario := range []string{"replacement", "partly_spent", "unspendable", "absent", "wrong_hash", "wrong_height", "non_coinbase"} {
			t.Run(fmt.Sprintf("%d-%s", exception.height, scenario), func(t *testing.T) {
				ctx := context.Background()
				old, _ := openUTXOStoreContext(ctx, t.TempDir())
				next, _ := openUTXOStoreContext(ctx, t.TempDir())
				g := utxoFixtureBlock(0, "", utxoFixtureCoinbase(77100, 50))
				original := utxoFixtureCoinbase(77101, 7, 9)
				b1 := utxoFixtureBlock(1, g.Hash, original)
				for _, block := range []blockView{g, b1} {
					if err := old.applyBlockLegacyOracle(ctx, block); err != nil {
						t.Fatal(err)
					}
					if err := next.applyBlockRecipe(ctx, block); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "partly_spent" {
					spent := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77102, 1), utxoFixtureSpend(77103, []inputView{{PrevTxID: original.TxID}}, 7))
					if err := old.applyBlockLegacyOracle(ctx, spent); err != nil {
						t.Fatal(err)
					}
					if err := next.applyBlockRecipe(ctx, spent); err != nil {
						t.Fatal(err)
					}
				}
				height := exception.height
				if scenario == "wrong_height" {
					height++
				}
				for _, store := range []*utxoStore{old, next} {
					parent := store.head.Checkpoint
					parent.Height = height - 1
					parent.BlockHash = testID(uint64(exception.height + 400))
					parent.PreviousBlockHash = testID(77199)
					parent.RecoveryBoundaryHeight = parent.Height
					parent.RecoveryBoundaryHash = parent.BlockHash
					parent.Commitment = utxoCheckpointDigest(parent)
					if err := store.writeRecord("commits", parent.Commitment, parent); err != nil {
						t.Fatal(err)
					}
					if err := store.publish(parent, parent.Height, parent.BlockHash); err != nil {
						t.Fatal(err)
					}
				}
				replacement := original
				replacement.Outputs = append([]outputView(nil), original.Outputs...)
				if scenario == "unspendable" {
					replacement.Outputs[1].ScriptPubKey = "6a"
				}
				if scenario == "absent" {
					replacement.Outputs = replacement.Outputs[:1]
				}
				block := utxoFixtureBlock(height, old.Head().BlockHash, replacement)
				block.Hash = exception.hash
				if scenario == "wrong_hash" {
					block.Hash = testID(77104)
				}
				if scenario == "non_coinbase" {
					duplicate := utxoFixtureSpend(77105, []inputView{{PrevTxID: original.TxID}}, 7)
					duplicate.TxID = original.TxID
					block = utxoFixtureBlock(height, old.Head().BlockHash, utxoFixtureCoinbase(77106, 1), duplicate)
					block.Hash = exception.hash
				}
				invalid := scenario != "replacement" && scenario != "partly_spent"
				a, b := old.applyBlockLegacyOracle(ctx, block), next.applyBlockRecipe(ctx, block)
				if (a == nil) != (b == nil) || (a == nil) == invalid {
					t.Fatalf("exception mismatch: %v %v", a, b)
				}
				if invalid {
					// The substituted parent height is a state fixture, not a
					// replayed undo record for the skipped historical interval.
					if !reflect.DeepEqual(old.Head(), next.Head()) {
						t.Fatal("rejected replacement changed native state")
					}
					return
				}
				compareNativeRecipeUTXO(t, old, next)
				parentHash := block.PreviousBlockHash
				for _, store := range []*utxoStore{old, next} {
					if err := store.Reconcile(ctx, func(_ context.Context, at int64) (string, error) {
						if at == height {
							return testID(77107), nil
						}
						return parentHash, nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(old.Head(), next.Head()) {
					t.Fatal("replacement undo changed native checkpoint")
				}
				coin, found, err := next.LookupOutpoint(ctx, original.TxID, 1)
				if err != nil || !found || coin.Height != 1 || coin.ValueSats != 9 {
					t.Fatalf("original incarnation not restored: %+v %t %v", coin, found, err)
				}
			})
		}
	}
}

func TestRecipeNativeUTXOInterruptedStageKeepsHead(t *testing.T) {
	ctx := context.Background()
	s, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := utxoFixtureBlock(0, "", utxoFixtureCoinbase(77200, 1))
	if err = s.applyBlockRecipe(ctx, g); err != nil {
		t.Fatal(err)
	}
	b := utxoFixtureBlock(1, g.Hash, utxoFixtureCoinbase(77201, 4))
	before := s.Head().Commitment
	stop := errors.New("interrupted")
	for _, stage := range []string{"facts_written", "pages_written", "journal_written", "commit_written", "before_head"} {
		s.commitHook = func(at string) error {
			if at == stage {
				return stop
			}
			return nil
		}
		if err = s.applyBlockRecipe(ctx, b); !errors.Is(err, stop) {
			t.Fatalf("%s: %v", stage, err)
		}
		s.commitHook = nil
		reopened, e := openUTXOStoreContext(ctx, s.root)
		if e != nil || reopened.Head().Commitment != before {
			t.Fatalf("%s advanced head: %v", stage, e)
		}
	}
	if err = s.applyBlockRecipe(ctx, b); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeNativeUTXORealGenesisAndBlockOne(t *testing.T) {
	ctx := context.Background()
	old, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	next, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blocks := recipeCoordinatorSource(t)
	for height := int64(0); height <= 1; height++ {
		block := blocks[height]
		block.Verification.HeaderChainMatch = true // fixture admission, not external authority
		if err = old.applyBlockLegacyOracle(ctx, block); err != nil {
			t.Fatal(err)
		}
		if err = next.applyBlockRecipe(ctx, block); err != nil {
			t.Fatal(err)
		}
		compareNativeRecipeUTXO(t, old, next)
	}
}

func TestRecipeNativeUTXOCancellationAtPublication(t *testing.T) {
	for _, stage := range []string{"facts_written", "pages_written", "journal_written", "commit_written", "before_head"} {
		t.Run(stage, func(t *testing.T) {
			s, err := openUTXOStoreContext(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			g := utxoFixtureBlock(0, "", utxoFixtureCoinbase(77300, 1))
			if err = s.applyBlockRecipe(context.Background(), g); err != nil {
				t.Fatal(err)
			}
			before := s.Head().Commitment
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.commitHook = func(at string) error {
				if at == stage {
					cancel()
				}
				return nil
			}
			b := utxoFixtureBlock(1, g.Hash, utxoFixtureCoinbase(77301, 2))
			if err = s.applyBlockRecipe(ctx, b); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			s.commitHook = nil
			reopened, err := openUTXOStoreContext(context.Background(), s.root)
			if err != nil || reopened.Head().Commitment != before {
				t.Fatalf("cancellation advanced head: %v", err)
			}
			if err = reopened.applyBlockRecipe(context.Background(), b); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// This compares the same physical backend on a deliberately synthetic output-
// heavy block. It measures total apply cost, not Bitcoin-chain throughput.
func TestRecipeNativeUTXOApplyMeasurement(t *testing.T) {
	if os.Getenv("GATEWAY_UTXO_RECIPE_MEASURE") != "1" {
		t.Skip("opt-in paired apply measurement")
	}
	ctx := context.Background()
	g := utxoFixtureBlock(0, "", utxoFixtureCoinbase(77400, 1))
	values := make([]uint64, 2048)
	for i := range values {
		values[i] = 1
	}
	b := utxoFixtureBlock(1, g.Hash, utxoFixtureCoinbase(77401, values...))
	stores := make([]*utxoStore, 2)
	for i, label := range []string{"legacy", "recipe"} {
		var err error
		stores[i], err = openUTXOStoreContext(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err = stores[i].applyBlockLegacyOracle(ctx, g); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		if i == 0 {
			err = stores[i].applyBlockLegacyOracle(ctx, b)
		} else {
			err = stores[i].applyBlockRecipe(ctx, b)
		}
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s outputs=%d elapsed=%s allocated=%d mallocs=%d", label, len(values), time.Since(start), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	}
	compareNativeRecipeUTXO(t, stores[0], stores[1])
}
