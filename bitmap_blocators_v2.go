package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// V1 bytes are frozen. V2 uses a positional inscription blocator, while raw
// Bitcoin inscription IDs exist only during evidence checking in memory.
func (c bitmapDevelopmentCandidate) MarshalJSON() ([]byte, error) {
	type plain bitmapDevelopmentCandidate
	if c.Blocator == "" {
		return json.Marshal(plain(c))
	}
	c.ID, c.Parents, c.RevealCoordinate = "", nil, ""
	raw, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	delete(fields, "id")
	delete(fields, "parents")
	delete(fields, "reveal_coordinate")
	return json.Marshal(fields)
}

func (r bitmapDevelopmentBlock) MarshalJSON() ([]byte, error) {
	type plain bitmapDevelopmentBlock
	if r.WinnerBlocators == nil {
		return json.Marshal(plain(r))
	}
	r.OrderedWinnerIDs = nil
	raw, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	delete(fields, "ordered_winner_ids")
	fields["winner_blocators"], err = json.Marshal(r.WinnerBlocators)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func bitmapValidInscriptionBlocator(value string) bool {
	c, ok := parseBODCoordinate(value)
	return ok && c.Kind == coordInscription && c.TxID == "" && c.Height >= 0 && c.TxIndex >= 0 && c.InscriptionIndex >= 0 && value == inscriptionCoordinate(uint32(c.TxIndex), uint32(c.InscriptionIndex), c.Height)
}

func (s *bitmapRunConsumer) selectedParentIdentity(ctx context.Context, block blockView, parent bitmapDevelopmentCandidate) (bitmapDevelopmentCandidate, error) {
	if parent.ID != "" {
		return parent, nil
	}
	if !bitmapValidInscriptionBlocator(parent.Blocator) {
		return parent, fmt.Errorf("selected District blocator is unavailable")
	}
	source := block
	if parent.RevealHeight != block.Height {
		if s.blockAt == nil {
			return parent, fmt.Errorf("selected District reveal transaction evidence is unavailable")
		}
		var err error
		source, err = s.blockAt(ctx, parent.RevealHeight)
		if err != nil {
			return parent, err
		}
	}
	if source.Height != parent.RevealHeight || source.Hash != parent.RevealHash || !integrityVerified(source) || parent.TxIndex < 0 || parent.TxIndex >= len(source.Transactions) {
		return parent, fmt.Errorf("selected District reveal evidence does not match its blocator")
	}
	if s.hashAt != nil {
		hash, err := s.hashAt(source.Height)
		if err != nil {
			return parent, err
		}
		if hash != source.Hash {
			return parent, fmt.Errorf("selected District reveal is not on selected Bitcoin branch")
		}
	}
	parent.ID = fmt.Sprintf("%si%d", source.Transactions[parent.TxIndex].TxID, parent.InscriptionIndex)
	return parent, nil
}
