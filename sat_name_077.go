package main

import "strings"

// Ordinal names use bijective base 26 for supply minus the Sat number,
// matching the inverse of propertiesForSat077. Name decoding is entirely
// local: it does not depend on the current tip, indexes or a trace.
func satNumberForName077(name string) (uint64, bool) {
	if name == "" || len(name) > 11 {
		return 0, false
	}
	supply := theoreticalSatSupply()
	var encoded uint64
	for _, c := range []byte(name) {
		if c < 'a' || c > 'z' {
			return 0, false
		}
		digit := uint64(c-'a') + 1
		if encoded > (supply-digit)/26 {
			return 0, false
		}
		encoded = encoded*26 + digit
	}
	return supply - encoded, true
}

func parseSatAddress077(input string) (uint64, bool) {
	q := strings.TrimSpace(input)
	if number, ok := parseSatQuery077(q); ok {
		return number, true
	}
	if !strings.HasSuffix(q, ".sat") {
		return 0, false
	}
	return satNumberForName077(strings.TrimSuffix(q, ".sat"))
}
