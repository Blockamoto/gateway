package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Lean records retain only the district/winner pair. Checkpoints retain chain
// and rule identity independently. Existing full records remain readable.
func (r bitmapRecord) MarshalJSON() ([]byte, error) {
	if r.Lean {
		if r.District == nil || !r.Accepted || !isInscriptionID(r.Inscription) {
			return nil, fmt.Errorf("invalid lean Bitmap claim")
		}
		return json.Marshal(strconv.FormatUint(*r.District, 10) + ":" + r.Inscription)
	}
	type full bitmapRecord
	return json.Marshal(full(r))
}

func (r *bitmapRecord) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		pair := strings.SplitN(value, ":", 2)
		if len(pair) != 2 {
			return fmt.Errorf("invalid lean claim")
		}
		n, err := strconv.ParseUint(pair[0], 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != pair[0] || !isInscriptionID(pair[1]) {
			return fmt.Errorf("invalid lean claim")
		}
		*r = bitmapRecord{Lean: true, District: &n, Inscription: pair[1], Accepted: true}
		return nil
	}
	type full bitmapRecord
	var f full
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*r = bitmapRecord(f)
	return nil
}
