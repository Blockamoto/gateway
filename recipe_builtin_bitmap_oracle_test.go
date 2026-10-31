package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Frozen pre-composition evaluator, retained only for differential checks.
func (s *bitmapRunConsumer) applyBitmapOracle(ctx context.Context, block blockView, prepared *inscriptionBlockResult) error {
	unlock := s.lockWriter()
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if bitmapCanonicalChainID(s.id) {
		return s.applyCanonicalRetainedOutcome(ctx, block.Height)
	}
	if block.Height != s.NextHeight() || !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated {
		return fmt.Errorf("Bitmap requires next complete header-anchored block")
	}
	if s.head != nil && block.PreviousBlockHash != s.head.BlockHash {
		return fmt.Errorf("Bitmap source ancestry changed; reconcile before resume")
	}
	h := bitmapDevelopmentHead{Schema: 1, Index: s.id, Profile: s.instance.Profile, Height: block.Height, BlockHash: block.Hash, SegmentStart: s.instance.StartHeight, PreviousBlockHash: block.PreviousBlockHash, DistrictInstance: s.instance.DistrictInstance}
	if s.head != nil {
		h = *s.head
		h.Height = block.Height
		h.BlockHash = block.Hash
		h.Previous = s.head.Commitment
		h.PreviousBlockHash = block.PreviousBlockHash
	}
	h.ProfileHash = bitmapProfileHash(h.Profile)
	tree := func(root string) *ordinalTree {
		return newOrdinalTree(filepath.Join(s.dir, "pages"), root).withContext(ctx)
	}
	winners, candidates, targets, blocks := tree(h.WinnerRoot), tree(h.CandidateRoot), tree(h.TargetRoot), tree(h.BlockRoot)
	row := bitmapDevelopmentBlock{Height: block.Height, BlockHash: block.Hash, BlockEvaluated: true, OrderedWinnerIDs: []string{}, CompleteScope: true, PreviousCommitment: h.Previous}
	if bitmapBlocatorProfileSchema(s.instance.Profile.FingerprintSchema) {
		row.WinnerBlocators = []string{}
	}
	if bitmapConsumerKind(s.id) != "district" {
		d, err := bitmapHeadAt(ctx, s.root, s.instance.DistrictInstance, block.Height)
		if err != nil {
			return err
		}
		if d == nil {
			return s.pending(ctx, block, nil, "District evaluation is required at this block; no District build was started.")
		}
		source := d.LastBlock
		if source.Height != block.Height || d.ProfileHash != s.instance.DistrictProfileHash || source.BlockHash != block.Hash || !source.BlockEvaluated || !source.CompleteScope {
			return s.pending(ctx, block, nil, "Matching completed District evaluation is unavailable.")
		}
		h.DependencyCommitment = d.Commitment
		row.DependencyCommitment = d.Commitment
		if bitmapConsumerKind(s.id) == "chain" {
			row.OutcomeProfileHash = s.instance.DistrictProfileHash
			h.DistrictReceipt = d
			h.DistrictReceiptRef = ""
			row.OrderedWinnerIDs = append(row.OrderedWinnerIDs, source.OrderedWinnerIDs...)
			row.IsolatedHash, err = bitmapIsolatedOutcome(row.OrderedWinnerIDs)
			if err != nil {
				return err
			}
			previous := strings.Repeat("0", 64)
			if s.head != nil {
				previous = s.head.LastBlock.ChainHash
			}
			row.ChainHash, err = bitmapCumulativeOutcome(previous, row.IsolatedHash)
			if err != nil {
				return err
			}
			commit, err := bitmapScopedCommitment(h.SegmentStart, h.Height, h.BlockHash, row.ChainHash, s.instance.DistrictProfileHash)
			if err != nil {
				return err
			}
			row.ScopedCommitment = &commit
			row.Note = "District outcomes under the declared experimental profile; this digest does not prove correct evaluation or complete consensus validation."
			h.LastBlock = row
			if err := blocks.put(fmt.Sprintf("%016x", block.Height), row); err != nil {
				return err
			}
			if err := s.lockProfileEvidence(ctx); err != nil {
				return err
			}
			h.BlockRoot, err = blocks.flush()
			if err != nil {
				return err
			}
			return s.publish(ctx, h)
		}
	}
	var extraction inscriptionBlockResult
	var err error
	if prepared != nil {
		if prepared.BlockHeight != block.Height || prepared.BlockHash != block.Hash || prepared.ParserProfile != inscriptionParserProfile || prepared.ReferenceCommit != inscriptionReferenceCommit {
			return fmt.Errorf("Bitmap prepared extraction profile/source mismatch")
		}
		if err := validateCompleteInscriptionInventory(block, prepared); err != nil {
			return fmt.Errorf("Bitmap prepared extraction is not a complete authenticated inventory: %w", err)
		}
		extraction = *prepared
	} else {
		extraction, err = extractInscriptionOccurrences(block)
		if err != nil {
			return err
		}
	}
	ordered := append([]inscriptionOccurrence(nil), extraction.Occurrences...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TxIndex != ordered[j].TxIndex {
			return ordered[i].TxIndex < ordered[j].TxIndex
		}
		return ordered[i].Index < ordered[j].Index
	})
	staged := []bitmapDevelopmentCandidate{}
	unresolved := false
	for _, o := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if o.TxIndex < 0 || o.TxIndex >= len(block.Transactions) || o.TxID != block.Transactions[o.TxIndex].TxID || o.ID != fmt.Sprintf("%si%d", o.TxID, o.Index) {
			return fmt.Errorf("Bitmap occurrence transaction identity mismatch")
		}
		text, reason := bitmapDecodeProfileBody(s.instance.Profile, o.Body, o.Envelope.ContentType)
		if reason != "" && reason != "unresolved_charset" {
			text = string(o.Body)
		}
		marker := bitmapClaimMarker(text)
		if reason != "unresolved_charset" && marker < 0 {
			continue
		}
		stem := text
		if marker >= 0 {
			stem = text[:marker]
		}
		parts := strings.Split(stem, ".")
		parcel := bitmapConsumerKind(s.id) == "parcel"
		if reason != "unresolved_charset" && len(parts) != 1 && len(parts) != 2 {
			continue
		}
		if reason != "unresolved_charset" && (parcel && len(parts) != 2 || !parcel && len(parts) != 1) {
			continue
		}
		c := bitmapDevelopmentCandidate{ID: o.ID, Kind: bitmapConsumerKind(s.id), RevealCoordinate: inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height), RevealHeight: block.Height, RevealHash: block.Hash, TxIndex: o.TxIndex, InscriptionIndex: o.Index, ContentHash: o.ContentSHA256, Eligibility: "ineligible", Selection: "not-selected", Reason: reason, Parents: append([]string(nil), o.Envelope.Parents...), Ownership: "unknown"}
		if bitmapBlocatorProfileSchema(s.instance.Profile.FingerprintSchema) {
			c.Blocator = c.RevealCoordinate
			c.DeclaredParentCount = len(c.Parents)
		}
		if len(text) <= 128 {
			c.Content = text
		}
		if bitmapNamedSchema(s.instance.Profile.FingerprintSchema) || bitmapBlocatorProfileSchema(s.instance.Profile.FingerprintSchema) || s.instance.Profile.FingerprintSchema == bitmapCanonicalV1ProfileSchema {
			c.DecodeState = "pass"
			if reason == "unresolved_charset" {
				c.DecodeState = "unknown"
			} else if reason != "" {
				c.DecodeState = "fail"
			}
		}
		if s.instance.Profile.FingerprintSchema == bitmapExtendedProfileSchema {
			c.DelegateFieldState, c.DelegateFieldEvidence, err = bitmapDelegateDeclaration(o)
			if err != nil {
				return err
			}
			if c.DelegateFieldState == "" && s.instance.Profile.Answers["delegate_exclusion"] == 1 {
				c.Eligibility, c.Reason = "unresolved", "delegate_field_evidence_required"
				c.UnresolvedDependencies = []string{"authenticated own-envelope field declarations; reviewed occurrence replay required"}
				unresolved = true
				staged = append(staged, c)
				continue
			}
			if s.instance.Profile.Answers["delegate_exclusion"] == 1 && (c.DelegateFieldState == "present" || c.DelegateFieldState == "dangling") {
				c.Reason = "delegate_declaration_excluded"
				staged = append(staged, c)
				continue
			}
		}
		if reason == "unresolved_charset" {
			c.Eligibility = "unresolved"
			c.UnresolvedDependencies = []string{"declared charset interpretation"}
			unresolved = true
			staged = append(staged, c)
			continue
		}
		if reason != "" || strings.Join(parts, ".")+".bitmap" != text {
			if c.Reason == "" {
				c.Reason = "noncanonical_claim"
			}
			staged = append(staged, c)
			continue
		}
		district, ok := bitmapCanonicalDecimal(parts[len(parts)-1])
		if !ok {
			c.Reason = "noncanonical_district"
			staged = append(staged, c)
			continue
		}
		c.District = &district
		if parcel {
			tx, ok := bitmapCanonicalDecimal(parts[0])
			if !ok {
				c.Reason = "noncanonical_transaction"
				staged = append(staged, c)
				continue
			}
			c.Transaction = &tx
		}
		if district > uint64(block.Height) {
			c.Reason = "future_block"
			staged = append(staged, c)
			continue
		}
		if parcel {
			var parent bitmapDevelopmentCandidate
			d, e := bitmapHeadAt(ctx, s.root, s.instance.DistrictInstance, block.Height)
			if e != nil {
				return e
			}
			if d == nil || d.ProfileHash != s.instance.DistrictProfileHash || d.BlockHash != block.Hash || !d.LastBlock.BlockEvaluated || !d.LastBlock.CompleteScope {
				return s.pending(ctx, block, staged, "Matching completed selected District evaluation is unavailable; no dependency index was started.")
			}
			have, e := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(s.root, s.instance.DistrictInstance), "pages"), d.WinnerRoot).withContext(ctx).get(bitmapTargetKey(district, nil), &parent)
			if e != nil {
				return e
			}
			if !have {
				c.Reason = "no_recognized_district_at_reveal"
				staged = append(staged, c)
				continue
			}
			if parent.RevealHeight == block.Height && parent.TxIndex > o.TxIndex {
				c.Reason = "district_parent_not_yet_recognized"
				staged = append(staged, c)
				continue
			}
			var source blockView
			if int64(district) == block.Height {
				source = block
			} else if s.blockAt != nil {
				source, err = s.blockAt(ctx, int64(district))
			} else {
				err = fmt.Errorf("referenced block transaction count is unavailable")
			}
			if err != nil {
				c.Eligibility = "unresolved"
				c.Reason = "transaction_count_evidence_required"
				c.UnresolvedDependencies = []string{"authenticated target-block transaction count"}
				return s.pending(ctx, block, append(staged, c), "Referenced transaction-count evidence is unavailable: "+err.Error())
			}
			if source.Height != int64(district) || !integrityVerified(source) || uint64(len(source.Transactions)) != source.TransactionCount {
				return fmt.Errorf("Parcel transaction-count source mismatch")
			}
			if s.hashAt != nil {
				hash, e := s.hashAt(int64(district))
				if e != nil {
					return e
				}
				if hash != source.Hash {
					return fmt.Errorf("Parcel context is on a replaced source branch")
				}
			}
			if *c.Transaction >= source.TransactionCount {
				c.Reason = "transaction_out_of_range"
				staged = append(staged, c)
				continue
			}
			declared := false
			if len(c.Parents) > 0 && parent.ID == "" {
				parent, e = s.selectedParentIdentity(ctx, block, parent)
				if e != nil {
					c.Eligibility = "unresolved"
					c.Reason = "selected_parent_reveal_evidence_required"
					c.UnresolvedDependencies = []string{e.Error()}
					return s.pending(ctx, block, append(staged, c), "Selected District reveal evidence is unavailable; no unrelated index was started.")
				}
			}
			for _, id := range c.Parents {
				if id == parent.ID {
					declared = true
					break
				}
			}
			if !declared {
				c.Reason = "recognized_district_not_declared_parent"
				staged = append(staged, c)
				continue
			}
			if c.Blocator != "" {
				c.SelectedParentDeclared = true
				c.ParentBlocators = []string{parent.Blocator}
			}
			var proof *bitmapParentProof
			if s.id == bitmapReplayParcelID {
				proof, e = s.checkedReplayParent(ctx, block, o, parent)
			} else {
				proof, e = checkedBitmapParent(ctx, s.a, block, o, parent)
			}
			if e != nil {
				if errors.Is(e, ErrBitmapParentInvalid) {
					c.Eligibility = "ineligible"
					c.Reason = "parent_not_consumed_by_reveal"
					staged = append(staged, c)
					continue
				}
				c.Eligibility = "unresolved"
				c.Reason = "parent_proof_evidence_unavailable"
				if s.id != bitmapReplayParcelID {
					c.Reason = "saved_sat_dependency_unavailable"
				}
				c.UnresolvedDependencies = []string{e.Error()}
				return s.pending(ctx, block, append(staged, c), "Checked parent provenance is unavailable: "+e.Error())
			}
			if proof == nil {
				c.Eligibility = "unresolved"
				c.Reason = "checked_parent_provenance_required"
				c.UnresolvedDependencies = []string{"matching pre-block Sat identity and input provenance"}
				unresolved = true
				staged = append(staged, c)
				continue
			}
			c.Provenance = proof
			if proof.ReplayVersion == 3 && proof.RelationState == "rejected" {
				c.Eligibility, c.Reason = "ineligible", "parent_not_consumed_by_reveal"
				staged = append(staged, c)
				continue
			}
		}
		c.Eligibility = "eligible"
		key := bitmapTargetKey(district, c.Transaction)
		var prior bitmapDevelopmentCandidate
		have, e := winners.get(key, &prior)
		if e != nil {
			return e
		}
		if have {
			c.Reason = "already_claimed"
		} else {
			c.Selection = "selected"
			c.Reason = "first_eligible_under_development_profile"
			if bitmapBlocatorProfileSchema(s.instance.Profile.FingerprintSchema) || s.instance.Profile.FingerprintSchema == bitmapCanonicalV1ProfileSchema {
				c.Reason = "first_qualifying_canonical_reveal"
			}
			c.RuleResults = bitmapRuleAudits(s.instance.Profile, c)
			if err := winners.put(key, c); err != nil {
				return err
			}
			h.WinnerCount++
			if c.Blocator != "" {
				row.WinnerBlocators = append(row.WinnerBlocators, c.Blocator)
			} else {
				row.OrderedWinnerIDs = append(row.OrderedWinnerIDs, c.ID)
			}
		}
		staged = append(staged, c)
	}
	if unresolved {
		return s.pending(ctx, block, staged, "Winner-affecting evidence is unresolved. Complete coverage and the Bitmap Chain do not advance through this block.")
	}
	for _, c := range staged {
		c.RuleResults = bitmapRuleAudits(s.instance.Profile, c)
		key := bitmapCandidateKey(c)
		if err := candidates.put(key, c); err != nil {
			return err
		}
		if c.District != nil {
			if err := targets.put(bitmapTargetKey(*c.District, c.Transaction)+key, c); err != nil {
				return err
			}
		}
	}
	row.CandidateCount = len(staged)
	h.CandidateCount += uint64(len(staged))
	h.LastBlock = row
	if err := blocks.put(fmt.Sprintf("%016x", block.Height), row); err != nil {
		return err
	}
	if err := s.lockProfileEvidence(ctx); err != nil {
		return err
	}
	roots, err := flushBitmapTrees([4]*ordinalTree{winners, candidates, targets, blocks})
	if err != nil {
		return err
	}
	h.WinnerRoot, h.CandidateRoot, h.TargetRoot, h.BlockRoot = roots[0], roots[1], roots[2], roots[3]
	return s.publish(ctx, h)
}
