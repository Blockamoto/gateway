package main

import "fmt"

// Inscription scans on mainnet start with the first known inscription. This is
// a write/capture guard only: previously saved historical records remain
// readable, and other consumers may independently parse a block's witnesses.
func requireInscriptionIngestionRange(network string, height, from int64) error {
	if network == "bitcoin-mainnet" && height < firstMainnetInscriptionHeight {
		return fmt.Errorf("%w: inscription block %d precedes the mainnet origin %d", errIndexRunAdmissionChanged, height, firstMainnetInscriptionHeight)
	}
	if height < from {
		return fmt.Errorf("%w: inscription block %d precedes the admitted start %d", errIndexRunAdmissionChanged, height, from)
	}
	return nil
}

// Resolved-block fanout may run outside the coordinated cohort, including
// while another track processes a much earlier block. It must honor the
// inscription consumer's actual range even when it is not a cohort owner.
func resolvedInscriptionRangeAllowed(d indexRunDocument, height int64) error {
	definition, err := findIndexDefinition("inscriptions")
	if err != nil {
		return err
	}
	from := defaultIndexFrom(definition)
	if d.Prepared.In != nil {
		from = *d.Prepared.In
	}
	if d.Run != nil {
		switch d.Run.State {
		case "running", "waiting", "pausing", "paused":
			if p, ok := indexRunProgressFor(d.Run, "inscriptions"); ok {
				// Prepared handles may already describe a later run. The active
				// admission is bound to its own request and consumer progress.
				from = defaultIndexFrom(definition)
				if d.Run.Request.In != nil {
					from = *d.Run.Request.In
				}
				if p.From > from {
					from = p.From
				}
				if p.EffectiveFrom != nil && *p.EffectiveFrom > from {
					from = *p.EffectiveFrom
				}
				selection, selected := runSelection(d.Run.Request, "inscriptions")
				if selected && (d.Run.Request.Out != nil || !selection.Live) && height > p.To {
					return fmt.Errorf("%w: inscription block %d is beyond the admitted end %d", errIndexRunAdmissionChanged, height, p.To)
				}
				return requireInscriptionIngestionRange(definition.Network, height, from)
			}
		}
	}
	if d.Prepared.Out != nil && height > *d.Prepared.Out {
		return fmt.Errorf("%w: inscription block %d is beyond the selected end %d", errIndexRunAdmissionChanged, height, *d.Prepared.Out)
	}
	return requireInscriptionIngestionRange(definition.Network, height, from)
}

// A saved instance may begin later than the default or current global In.
// Incidental capture must not silently prepend it when no run is active.
func resolvedAdmissionAllowedForRoot(root string, d indexRunDocument, id string, height int64) error {
	if err := resolvedAdmissionAllowed(d, id, height); err != nil {
		return err
	}
	if resolvedSelectionOwner(id) != "inscriptions" {
		return nil
	}
	store, err := indexStoreHead(root, "inscriptions")
	if err != nil {
		return err
	}
	if store.checkpoint != nil {
		return requireInscriptionIngestionRange(store.definition.Network, height, store.checkpoint.From)
	}
	return nil
}
