package main

import "context"

// Optional source retention is controlled by the persisted timeline, including
// ordinary explorer reads. A legacy cache setting, caller-specific retention
// string, or an unconfigured profile never implicitly arms a storage consumer.
func timelineTrackArmed(root, id string) (bool, error) {
	d, err := readIndexRunDocument(root)
	if err != nil {
		return false, err
	}
	if !d.Configured {
		return false, nil
	}
	choice, ok := runSelection(d.Prepared, id)
	return ok && choice.Armed, nil
}

func timelineRawBlocksOn(root string) bool {
	on, err := timelineTrackArmed(root, "blocks")
	return err == nil && on
}

// Recheck immediately before making retained body membership visible. The
// shared acquisition may still be useful to another caller after one caller
// cancels or its own index is switched Off; that caller cannot publish bytes.
func checkTimelineRawBlocksOn(ctx context.Context, root string) error {
	return checkTimelineTrackArmed(ctx, root, "blocks")
}

func checkTimelineTrackArmed(ctx context.Context, root, id string) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	on, err := timelineTrackArmed(root, id)
	if err != nil {
		return err
	}
	if !on {
		return errIndexRunConsumerStopped
	}
	return nil
}
