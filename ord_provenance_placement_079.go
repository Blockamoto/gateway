package main

import "context"

// Provenance uses Ord b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414's
// inscription_updater.rs:140-200, 261-303. Duplicate/incomplete fields,
// pushnums, stutter and pre-Jubilee envelope location affect numbering and
// curses, not the new inscription's stream offset. Unknown even fields and
// zero-value inputs still make the inscription unbound. The shared placement
// primitive applies the FIFO/pointer calculation; the explicit Jubilee value
// suppresses only its viewer-specific historical ambiguity gate.
func deriveOrdProvenancePlacement(ctx context.Context, tx transactionView, env ordEnvelope, value func(int) (uint64, error)) (inscriptionInitialPlacement, error) {
	e := env
	e.Duplicate = false
	e.Incomplete = false
	e.Pushnum = false
	e.Stutter = false
	return deriveInscriptionInitialPlacement(ctx, tx, mainnetJubileeHeight, e, value)
}
