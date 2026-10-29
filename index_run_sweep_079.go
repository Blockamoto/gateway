package main

// eligible contains the ready consumers only. Their order is immaterial to
// the selected height; ties are processed together by the coordinator.
// The caller supplies at least one eligible consumer.
func lowestReadyIndexRunFrontier079(eligible []int, nexts map[int]int64) int {
	chosen := eligible[0]
	for _, i := range eligible[1:] {
		if nexts[i] < nexts[chosen] {
			chosen = i
		}
	}
	return chosen
}
