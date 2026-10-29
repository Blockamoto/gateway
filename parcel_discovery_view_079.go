package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Discovery is deliberately separate from the validated Bitmap snapshot.
// Counts describe what was discovered, not what is still awaiting validation.
type parcelDiscoverySnapshotView079 struct {
	Height        int64  `json:"height"`
	BlockHash     string `json:"block_hash"`
	Commitment    string `json:"commitment"`
	ProfileHash   string `json:"profile_hash"`
	DistrictState string `json:"district_state"`
}

type parcelDiscoveryViewCursor079 struct {
	Snapshot    string `json:"snapshot"`
	Kind        string `json:"kind"`
	District    uint64 `json:"district,omitempty"`
	Transaction uint64 `json:"transaction,omitempty"`
	Height      int64  `json:"height,omitempty"`
	Before      string `json:"before,omitempty"`
	Offset      int    `json:"offset,omitempty"`
	Digest      string `json:"digest"`
}

func parcelDiscoveryViewCursorDigest079(c parcelDiscoveryViewCursor079) string {
	c.Digest = ""
	return indexDigest(c)
}
func encodeParcelDiscoveryViewCursor079(c parcelDiscoveryViewCursor079) string {
	c.Digest = parcelDiscoveryViewCursorDigest079(c)
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Populate only separately labelled provisional data. Each page is bounded and
// independently anchored; it never changes the validated winners or counts.
func (a *app) populateParcelDiscoveryView079(ctx context.Context, out *bitmapDevelopmentView, validated *bitmapDevelopmentHead, district, transaction, height *uint64, cursor string, limit int) error {
	head, err := readParcelDiscoveryHead079(ctx, a.dataDir)
	if err != nil {
		return err
	}
	if head == nil {
		return nil
	}
	selected := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	if err := ordRequireSelected(selected, head.Height, head.BlockHash); err != nil {
		out.DiscoveryState = "stale"
		out.DiscoveryNote = "Saved Parcel discovery is not anchored to the selected chain; reconciliation is required."
		return nil
	}
	if head.DistrictState == "complete" {
		d, e := bitmapHeadAt(ctx, a.dataDir, bitmapStandardDistrictID, head.Height)
		if e != nil || d == nil || d.Commitment != head.DistrictCommitment || d.BlockHash != head.BlockHash || d.ProfileHash != head.DistrictProfile {
			out.DiscoveryState = "stale"
			out.DiscoveryNote = "Saved Parcel discovery no longer matches its District interpretation; reconciliation is required."
			return nil
		}
	}
	out.DiscoverySnapshot = &parcelDiscoverySnapshotView079{Height: head.Height, BlockHash: head.BlockHash, Commitment: head.Commitment, ProfileHash: head.ProfileHash, DistrictState: head.DistrictState}
	out.DiscoveryCoverage = []heightInterval{{From: head.BaseHeight + 1, To: head.Height}}
	out.ProvisionalDiscoveredCount, out.InvalidSyntaxCount = head.ProvisionalCount, head.InvalidCount
	out.DiscoveryNote = "Discovery counts are cumulative. Claims shown as not yet validated are not winners or verified children."
	validatedHeight := head.BaseHeight
	if validated != nil {
		validatedHeight = validated.Height
		out.ValidationCoverage = []heightInterval{{From: validated.SegmentStart, To: validated.Height}}
	} else {
		out.State = "provisional"
	}
	if head.Height > validatedHeight {
		out.DiscoveryState = "not_yet_validated"
	} else {
		out.DiscoveryState = "validated"
	}
	if limit < 1 || limit > 100 {
		return fmt.Errorf("invalid Parcel discovery page limit")
	}
	c := parcelDiscoveryViewCursor079{Snapshot: head.Commitment}
	if district != nil && transaction != nil {
		c.Kind, c.District, c.Transaction = "target", *district, *transaction
	} else if height != nil {
		c.Kind, c.Height = "block", head.Height
		if *height > uint64(^uint64(0)>>1) {
			return fmt.Errorf("invalid Parcel discovery height")
		}
		c.Height = int64(*height)
	} else {
		c.Kind = "all"
	}
	if cursor != "" {
		if len(cursor) > 2048 {
			return fmt.Errorf("Parcel discovery cursor exceeds budget")
		}
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		var saved parcelDiscoveryViewCursor079
		if e != nil || json.Unmarshal(raw, &saved) != nil || saved.Digest != parcelDiscoveryViewCursorDigest079(saved) || saved.Snapshot != c.Snapshot || saved.Kind != c.Kind || saved.District != c.District || saved.Transaction != c.Transaction || saved.Height != c.Height || saved.Offset < 0 {
			return fmt.Errorf("Parcel discovery cursor no longer matches this snapshot or query")
		}
		c = saved
	}
	var rows []parcelDiscoveryCandidate079
	if c.Kind == "target" {
		var next string
		rows, next, err = parcelDiscoveryTarget079(ctx, a.dataDir, head.Commitment, c.District, c.Transaction, c.Before, limit)
		if next != "" {
			c.Before = next
			out.ProvisionalNextCursor = encodeParcelDiscoveryViewCursor079(c)
		}
	} else if c.Kind == "all" {
		var next string
		rows, next, err = parcelDiscoveryAll079(ctx, a.dataDir, head.Commitment, c.Before, limit)
		if next != "" {
			c.Before = next
			out.ProvisionalNextCursor = encodeParcelDiscoveryViewCursor079(c)
		}
	} else {
		var next int
		rows, next, err = parcelDiscoveryBlockPage079(ctx, a.dataDir, head.Commitment, c.Height, c.Offset, limit)
		if next > 0 {
			c.Offset = next
			out.ProvisionalNextCursor = encodeParcelDiscoveryViewCursor079(c)
		}
	}
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.RevealHeight <= validatedHeight {
			// Backlog and target indexes are newest-first. Once validation has
			// reached a row, every later page is already validated too.
			out.ProvisionalNextCursor = ""
			break
		}
		b, e := readParcelDiscoveryBlock079(ctx, a.dataDir, row.RevealHeight)
		if e != nil || b == nil || !strings.EqualFold(b.BlockHash, row.RevealHash) {
			return fmt.Errorf("Parcel discovery candidate block unavailable")
		}
		if err := ordRequireSelected(selected, b.Height, b.BlockHash); err != nil {
			return fmt.Errorf("Parcel discovery candidate is not on the selected chain")
		}
		if b.DistrictState == "complete" {
			d, e := bitmapHeadAt(ctx, a.dataDir, bitmapStandardDistrictID, b.Height)
			if e != nil || d == nil || d.Commitment != b.DistrictCommitment || d.BlockHash != b.BlockHash || d.ProfileHash != b.DistrictProfile {
				return fmt.Errorf("Parcel discovery candidate District interpretation changed")
			}
		}
		out.ProvisionalCandidates = append(out.ProvisionalCandidates, row)
	}
	return nil
}
