package main

import (
	"fmt"
	"strconv"
)

// Ordinal notation follows the pinned Ord 0.29.0 ordinals crate. These
// properties describe issuance and do not assert a mined or validated location.
type satProperties077 struct {
	Rarity     string `json:"rarity"`
	Name       string `json:"name"`
	Decimal    string `json:"decimal"`
	Degree     string `json:"degree"`
	Percentile string `json:"percentile"`
}

func propertiesForSat077(number uint64, origin satlineIssuance) satProperties077 {
	rarity := "common"
	if origin.SubsidyOffset == 0 {
		switch {
		case number == 0:
			rarity = "mythic"
		case origin.Height%1260000 == 0:
			rarity = "legendary"
		case origin.Height%210000 == 0:
			rarity = "epic"
		case origin.Height%2016 == 0:
			rarity = "rare"
		default:
			rarity = "uncommon"
		}
	}
	supply := theoreticalSatSupply()
	x := supply - number
	name := make([]byte, 0, 16)
	for x > 0 {
		name = append(name, byte('a'+(x-1)%26))
		x = (x - 1) / 26
	}
	for l, r := 0, len(name)-1; l < r; l, r = l+1, r-1 {
		name[l], name[r] = name[r], name[l]
	}
	return satProperties077{
		Rarity: rarity, Name: string(name),
		Decimal:    fmt.Sprintf("%d.%d", origin.Height, origin.SubsidyOffset),
		Degree:     fmt.Sprintf("%d°%d′%d″%d‴", origin.Height/1260000, origin.Height%210000, origin.Height%2016, origin.SubsidyOffset),
		Percentile: strconv.FormatFloat(float64(number)/float64(supply-1)*100, 'f', -1, 64) + "%",
	}
}
