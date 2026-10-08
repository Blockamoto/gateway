package main

import (
	"fmt"
	"strconv"
	"strings"
)

type coordinateKind string

const (
	coordTransaction coordinateKind = "transaction"
	coordOutput      coordinateKind = "output"
	coordInput       coordinateKind = "input"
	coordSatpoint    coordinateKind = "satpoint"
	coordInscription coordinateKind = "inscription"
)

type bodCoordinate struct {
	Kind             coordinateKind
	Height           int64
	TxIndex          int
	TxID             string // Nonempty for an exact transaction identity within Height.
	IOIndex          int
	SatOffset        uint64
	InscriptionIndex int
	Raw              string
}

func txCoordinate(txIndex int, height int64) string {
	if txIndex < 0 || height < 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d", txIndex, height)
}
func outputCoordinate(vout, txIndex int, height int64) string {
	if vout < 0 || txIndex < 0 || height < 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", vout, txIndex, height)
}
func inputCoordinate(vin, txIndex int, height int64) string {
	if vin < 0 || txIndex < 0 || height < 0 {
		return ""
	}
	return fmt.Sprintf("i%d.%d.%d", vin, txIndex, height)
}
func satpointCoordinate(offset uint64, vout, txIndex int, height int64) string {
	if vout < 0 || txIndex < 0 || height < 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", offset, vout, txIndex, height)
}

func parseBODCoordinate(s string) (bodCoordinate, bool) {
	raw := strings.TrimSpace(strings.ToLower(s))
	if hasHashCoordinateIdentity(raw) {
		return parseHashCoordinate(raw)
	}
	parts := strings.Split(raw, ".")
	if len(parts) == 2 {
		if tx, idx, ok := strings.Cut(parts[0], "i"); ok {
			parts = []string{tx, "i" + idx, parts[1]}
		}
	}
	parseNonNeg := func(x string) (int64, bool) {
		if x == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil && n >= 0
	}
	parseUint := func(x string) (uint64, bool) {
		if x == "" {
			return 0, false
		}
		n, err := strconv.ParseUint(x, 10, 64)
		return n, err == nil
	}
	maxInt := int64(^uint(0) >> 1)
	if len(parts) == 2 {
		tx, ok1 := parseNonNeg(parts[0])
		h, ok2 := parseNonNeg(parts[1])
		if ok1 && ok2 && tx <= maxInt {
			return bodCoordinate{Kind: coordTransaction, Height: h, TxIndex: int(tx), IOIndex: -1, Raw: raw}, true
		}
	}
	if len(parts) == 3 {
		if strings.HasPrefix(parts[0], "i") {
			vin, ok1 := parseNonNeg(strings.TrimPrefix(parts[0], "i"))
			tx, ok2 := parseNonNeg(parts[1])
			h, ok3 := parseNonNeg(parts[2])
			if ok1 && ok2 && ok3 && vin <= maxInt && tx <= maxInt {
				return bodCoordinate{Kind: coordInput, Height: h, TxIndex: int(tx), IOIndex: int(vin), Raw: raw}, true
			}
		} else {
			vout, ok1 := parseNonNeg(parts[0])
			tx, ok2 := parseNonNeg(parts[1])
			h, ok3 := parseNonNeg(parts[2])
			if ok1 && ok2 && ok3 && vout <= maxInt && tx <= maxInt {
				return bodCoordinate{Kind: coordOutput, Height: h, TxIndex: int(tx), IOIndex: int(vout), Raw: raw}, true
			}
		}
	}
	if len(parts) == 4 {
		off, ok0 := parseUint(parts[0])
		vout, ok1 := parseNonNeg(parts[1])
		tx, ok2 := parseNonNeg(parts[2])
		h, ok3 := parseNonNeg(parts[3])
		if ok0 && ok1 && ok2 && ok3 && vout <= maxInt && tx <= maxInt {
			return bodCoordinate{Kind: coordSatpoint, Height: h, TxIndex: int(tx), IOIndex: int(vout), SatOffset: off, Raw: raw}, true
		}
	}
	// Inscription occurrence: transaction-index.i<inscription-index>.block-height.
	// This is deliberately positional and Bitcoin-derived; it does not require
	// an Ord server or a transaction hash.
	if len(parts) == 3 && strings.HasPrefix(parts[1], "i") {
		tx, ok1 := parseNonNeg(parts[0])
		idx, ok2 := parseNonNeg(strings.TrimPrefix(parts[1], "i"))
		h, ok3 := parseNonNeg(parts[2])
		if ok1 && ok2 && ok3 && tx <= maxInt && idx <= maxInt {
			return bodCoordinate{Kind: coordInscription, Height: h, TxIndex: int(tx), InscriptionIndex: int(idx), IOIndex: -1, Raw: raw}, true
		}
	}
	return bodCoordinate{}, false
}

func annotateCoordinates(height int64, txs []transactionView) []transactionView {
	if height < 0 {
		return txs
	}
	for ti := range txs {
		txs[ti].Coordinate = txCoordinate(txs[ti].Index, height)
		for i := range txs[ti].Inputs {
			txs[ti].Inputs[i].Coordinate = inputCoordinate(txs[ti].Inputs[i].N, txs[ti].Index, height)
		}
		for i := range txs[ti].Outputs {
			txs[ti].Outputs[i].Coordinate = outputCoordinate(txs[ti].Outputs[i].N, txs[ti].Index, height)
		}
	}
	return txs
}
