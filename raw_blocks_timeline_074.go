package main

func (a *app) timelineIndexedBodiesSource(headers *timelineLocalReader, problems *[]string) indexTimelineSource {
	view := a.rawCoverageSnapshot(headers, nil, nil)
	source := timelineSource("gateway_indexed_raw_bodies", "Private durable Raw producer bodies. Compact metadata coverage is reconstructed in the background; full block integrity is rechecked on use.")
	source.Ranges, source.Complete = view.Ranges, view.Complete
	if view.Reason != "" {
		source.Note += " " + view.Reason
	}
	return source
}
