package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	satlineMaxHops = 100000
)

// Satline is deliberately a local ordinal-movement engine. It consumes Bitcoin
// on Demand's existing block, transaction and spender capabilities and adds no
// new source of Bitcoin truth of its own.
type satlineIssuance struct {
	Height        int64  `json:"height"`
	SubsidySats   uint64 `json:"subsidy_sats"`
	SubsidyOffset uint64 `json:"subsidy_offset"`
	FirstSat      uint64 `json:"first_sat"`
}

type satlinePoint struct {
	TxID      string `json:"txid"`
	Vout      uint32 `json:"vout"`
	Offset    uint64 `json:"offset"`
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash,omitempty"`
	TxIndex   int    `json:"tx_index"`
}

func (p satlinePoint) String() string {
	if p.TxID == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", p.TxID, p.Vout, p.Offset)
}

type satlineHop struct {
	Index                int          `json:"index"`
	Type                 string       `json:"type"` // output_to_output|fee_to_coinbase
	Source               satlinePoint `json:"source"`
	SpendingTxID         string       `json:"spending_txid"`
	SpendingVin          int          `json:"spending_vin"`
	BlockHeight          int64        `json:"block_height"`
	BlockHash            string       `json:"block_hash"`
	InputStreamPosition  uint64       `json:"input_stream_position"`
	Destination          satlinePoint `json:"destination"`
	TransactionFeeSats   uint64       `json:"transaction_fee_sats,omitempty"`
	FeeOffset            uint64       `json:"fee_offset,omitempty"`
	PriorBlockFeesSats   uint64       `json:"prior_block_fees_sats,omitempty"`
	CoinbaseStreamOffset uint64       `json:"coinbase_stream_offset,omitempty"`
	VerificationState    string       `json:"verification_state"`
	SpenderProvider      string       `json:"spender_provider,omitempty"`
	Note                 string       `json:"note,omitempty"`
}

type satlineResult struct {
	AnchorStatus      string `json:"anchor_status,omitempty"`
	OperationalReason string `json:"operational_reason,omitempty"`

	Mode                   string                  `json:"mode"` // sat|satpoint
	SatNumber              *uint64                 `json:"sat_number,omitempty"`
	StartSatpoint          *satlinePoint           `json:"start_satpoint,omitempty"`
	Issuance               *satlineIssuance        `json:"issuance,omitempty"`
	BirthSatpoint          *satlinePoint           `json:"birth_satpoint,omitempty"`
	State                  string                  `json:"state"`
	CurrentSatpoint        *satlinePoint           `json:"current_satpoint,omitempty"`
	Snapshot               *chainSnapshot          `json:"snapshot,omitempty"`
	PendingMempoolSpend    string                  `json:"pending_mempool_spend,omitempty"`
	HopCount               int                     `json:"hop_count"`
	Hops                   []satlineHop            `json:"hops"`
	VerificationState      string                  `json:"verification_state"`
	ExpectedIssuanceHeight int64                   `json:"expected_issuance_height,omitempty"`
	ChainTipHeight         int64                   `json:"chain_tip_height,omitempty"`
	LostAtHeight           int64                   `json:"lost_at_height,omitempty"`
	LostAtBlockHash        string                  `json:"lost_at_block_hash,omitempty"`
	Note                   string                  `json:"note"`
	Persistence            *satlinePersistenceView `json:"persistence,omitempty"`
}

type satlineTxEvidence struct {
	Tx                transactionView
	Height            int64
	BlockHash         string
	VerificationState string
	Source            string
}

type satlineBackend interface {
	ChainAuthority() chainAuthorityView
	BlockByHeight(height int64) (blockView, error)
	Spender(txid string, vout uint32) (spenderLookupView, error)
	Transaction(txid string, beforeHeight int64) (satlineTxEvidence, error)
}

type appSatlineBackend struct{ a *app }

func (b appSatlineBackend) ChainAuthority() chainAuthorityView { return b.a.currentChainAuthority() }
func (b appSatlineBackend) BlockByHeight(height int64) (blockView, error) {
	if block, e := b.a.retainedSatBlock(context.Background(), height); e == nil {
		return block, nil
	}
	return b.a.fetchAndDecode(strconv.FormatInt(height, 10))
}
func (b appSatlineBackend) Spender(txid string, vout uint32) (spenderLookupView, error) {
	if spend, e := b.a.retainedSatSpender(context.Background(), txid, vout); e == nil {
		return spend, nil
	}
	return b.a.resolveSpender(txid, vout)
}

// Transaction resolves a transaction in chain context. The beforeHeight bound
// is important for the two historical BIP30 duplicate txids: an input can only
// refer to the incarnation that exists at or before the spending block.
func (b appSatlineBackend) Transaction(txid string, beforeHeight int64) (satlineTxEvidence, error) {
	ctx := context.Background()
	if b.a.network != nil {
		ctx = b.a.network.ctx
	}
	return b.transactionContext(ctx, txid, beforeHeight)
}

func (b appSatlineBackend) transactionContext(ctx context.Context, txid string, beforeHeight int64) (satlineTxEvidence, error) {
	a := b.a
	txid = strings.ToLower(strings.TrimSpace(txid))
	if !validHash(txid) {
		return satlineTxEvidence{}, fmt.Errorf("invalid txid")
	}
	if r, found, err := a.indexedTransactionBeforeContext(ctx, txid, beforeHeight); err == nil && found && r.TransactionVerified {
		return satlineTxEvidence{Tx: r.Transaction, Height: r.Height, BlockHash: r.BlockHash, VerificationState: r.VerificationState, Source: r.SourceNetwork}, nil
	} else if ctx.Err() != nil {
		return satlineTxEvidence{}, ctx.Err()
	}
	if !releaseFeatureAvailable("tx-locator") {
		return satlineTxEvidence{}, fmt.Errorf("input transaction is not in the local Bitcoin Blocks index; additional locator providers are locked in this testing build")
	}
	if r, err := a.retainedSatTransaction(ctx, txid, beforeHeight); err == nil && transactionFitsHeightContext(r.Height, r.Tx.Coinbase, beforeHeight) {
		return r, nil
	} else if ctx.Err() != nil {
		return satlineTxEvidence{}, ctx.Err()
	}

	// The native graph deliberately retains all canonical tx locations, making
	// it the best context-aware local locator when present.
	if locs, err := a.graphFindTx(txid); err == nil && len(locs) > 0 {
		eligible := make([]graphTxLocation, 0, len(locs))
		for _, loc := range locs {
			if transactionFitsHeightContext(loc.Height, loc.TxIndex == 0, beforeHeight) {
				eligible = append(eligible, loc)
			}
		}
		if len(eligible) > 0 {
			sort.Slice(eligible, func(i, j int) bool {
				if eligible[i].Height != eligible[j].Height {
					return eligible[i].Height > eligible[j].Height
				}
				return eligible[i].TxIndex > eligible[j].TxIndex
			})
			loc := eligible[0]
			r, err := a.verifyTxLocation(txid, txLocation{TxID: txid, BlockHash: loc.BlockHash, Height: loc.Height, TxIndex: loc.TxIndex}, "Bitcoin on Demand native graph")
			if err == nil && transactionFitsHeightContext(r.Height, r.Transaction.Coinbase, beforeHeight) {
				return satlineTxEvidence{Tx: r.Transaction, Height: r.Height, BlockHash: r.BlockHash, VerificationState: r.VerificationState, Source: "bod_native_graph"}, nil
			}
		}
	}

	// Cached tx location knowledge is also valid as a locator when its chain
	// position can be confirmed by the current authority. Do not require the
	// private header mirror when live Core already anchors the same block.
	a.cacheMu.RLock()
	cached, cachedOK := a.cacheIndex.Tx[txid]
	a.cacheMu.RUnlock()
	if cachedOK && transactionFitsHeightContext(cached.Height, cached.TxIndex == 0, beforeHeight) {
		if canon, err := a.canonicalHashAtHeight(cached.Height); err == nil && strings.EqualFold(canon, cached.BlockHash) {
			if r, err := a.verifyTxLocation(txid, cached, "verified Bitcoin on Demand tx locator"); err == nil && transactionFitsHeightContext(r.Height, r.Transaction.Coinbase, beforeHeight) {
				return satlineTxEvidence{Tx: r.Transaction, Height: r.Height, BlockHash: r.BlockHash, VerificationState: r.VerificationState, Source: "bod_tx_locator"}, nil
			}
		}
	}

	// Live Core with txindex is an immediate local fast path. getrawtransaction
	// can occasionally work without txindex for wallet/mempool data, so trying
	// it is cheap; the beforeHeight bound prevents a later BIP30 incarnation
	// from being silently substituted for an earlier one.
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if core := inspectCore(settings); core.Connected {
		if loc, err := coreTxLocation(settings, txid); err == nil && transactionFitsHeightContext(loc.Height, loc.TxIndex == 0, beforeHeight) {
			if r, err := a.verifyTxLocation(txid, loc, "Bitcoin Core tx locator"); err == nil && transactionFitsHeightContext(r.Height, r.Transaction.Coinbase, beforeHeight) {
				return satlineTxEvidence{Tx: r.Transaction, Height: r.Height, BlockHash: r.BlockHash, VerificationState: r.VerificationState, Source: "bitcoin_core"}, nil
			}
		}
	}

	// Finally ask BOD peers for a location hint and verify the transaction
	// against the returned Bitcoin block. A locator later than the consuming
	// block is unusable for prevout value calculation.
	if r, err := a.resolveTransactionViaOverlay(txid); err == nil && r.TransactionVerified && transactionFitsHeightContext(r.Height, r.Transaction.Coinbase, beforeHeight) {
		return satlineTxEvidence{Tx: r.Transaction, Height: r.Height, BlockHash: r.BlockHash, VerificationState: r.VerificationState, Source: "bod_peer"}, nil
	}
	return satlineTxEvidence{}, fmt.Errorf("transaction %s could not be located at or before height %d", txid, beforeHeight)
}

type satlineResolver struct {
	valuesTried  map[string]bool
	feePrefixes  map[string][]uint64
	backend      satlineBackend
	blocks       map[int64]blockView
	txs          map[string][]satlineTxEvidence
	values       map[string]uint64
	weakest      string
	progress     func(satlineResult)
	progressBase *satlineResult
	ctx          context.Context
}

func newSatlineResolver(backend satlineBackend) *satlineResolver {
	return &satlineResolver{backend: backend, blocks: map[int64]blockView{}, txs: map[string][]satlineTxEvidence{}, values: map[string]uint64{}}
}

func (r *satlineResolver) emitProgress(res satlineResult) {
	if r.progress == nil {
		return
	}
	if r.progressBase != nil {
		base := *r.progressBase
		res.Mode = base.Mode
		res.SatNumber = base.SatNumber
		res.Issuance = base.Issuance
		res.BirthSatpoint = base.BirthSatpoint
		res.StartSatpoint = base.StartSatpoint
		res.ChainTipHeight = base.ChainTipHeight
	}
	r.progress(res)
}

func subsidyAtHeight(height int64) uint64 {
	if height < 0 {
		return 0
	}
	epoch := height / 210000
	if epoch >= 33 {
		return 0
	}
	return uint64(50*100_000_000) >> uint(epoch)
}

func firstSatAtHeight(height int64) uint64 {
	if height <= 0 {
		return 0
	}
	var total uint64
	remaining := height
	for epoch := int64(0); remaining > 0 && epoch < 33; epoch++ {
		blocks := int64(210000)
		if remaining < blocks {
			blocks = remaining
		}
		subsidy := subsidyAtHeight(epoch * 210000)
		if subsidy == 0 {
			break
		}
		total += uint64(blocks) * subsidy
		remaining -= blocks
	}
	return total
}

func theoreticalSatSupply() uint64 { return firstSatAtHeight(33 * 210000) }

func issuanceForSat(sat uint64) (satlineIssuance, bool) {
	if sat >= theoreticalSatSupply() {
		return satlineIssuance{}, false
	}
	var first uint64
	for epoch := int64(0); epoch < 33; epoch++ {
		subsidy := subsidyAtHeight(epoch * 210000)
		epochCount := uint64(210000) * subsidy
		if sat < first+epochCount {
			rel := sat - first
			blockInEpoch := rel / subsidy
			offset := rel % subsidy
			height := epoch*210000 + int64(blockInEpoch)
			return satlineIssuance{Height: height, SubsidySats: subsidy, SubsidyOffset: offset, FirstSat: first + blockInEpoch*subsidy}, true
		}
		first += epochCount
	}
	return satlineIssuance{}, false
}

func verificationRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "consensus_validated":
		return 5
	case "header_anchored":
		return 4
	case "pending_header_validation", "pending_chain_anchor":
		return 3
	case "structurally_checked", "self_consistent":
		return 2
	case "received":
		return 1
	default:
		return 0
	}
}

func (r *satlineResolver) observeVerification(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if r.weakest == "" || verificationRank(s) < verificationRank(r.weakest) {
		r.weakest = s
	}
}

func (r *satlineResolver) cacheBlock(b blockView) {
	r.blocks[b.Height] = b
	r.observeVerification(b.VerificationState)
	for _, tx := range b.Transactions {
		e := satlineTxEvidence{Tx: tx, Height: b.Height, BlockHash: b.Hash, VerificationState: b.VerificationState, Source: b.SourceNetwork}
		key := strings.ToLower(tx.TxID)
		already := false
		for _, old := range r.txs[key] {
			if old.Height == e.Height && strings.EqualFold(old.BlockHash, e.BlockHash) && old.Tx.Index == e.Tx.Index {
				already = true
				break
			}
		}
		if !already {
			r.txs[key] = append(r.txs[key], e)
		}
	}
}

func (r *satlineResolver) block(height int64) (blockView, error) {
	if r.ctx != nil && r.ctx.Err() != nil {
		return blockView{}, r.ctx.Err()
	}
	if b, ok := r.blocks[height]; ok {
		return b, nil
	}
	b, err := r.backend.BlockByHeight(height)
	if err != nil {
		return blockView{}, err
	}
	r.cacheBlock(b)
	return b, nil
}

func (r *satlineResolver) transaction(txid string, beforeHeight int64) (satlineTxEvidence, error) {
	if r.ctx != nil && r.ctx.Err() != nil {
		return satlineTxEvidence{}, r.ctx.Err()
	}
	key := strings.ToLower(txid)
	var best *satlineTxEvidence
	for i := range r.txs[key] {
		v := r.txs[key][i]
		if beforeHeight >= 0 && v.Height > beforeHeight {
			continue
		}
		if best == nil || v.Height > best.Height || (v.Height == best.Height && v.Tx.Index > best.Tx.Index) {
			vv := v
			best = &vv
		}
	}
	if best != nil {
		return *best, nil
	}
	v, err := r.backend.Transaction(key, beforeHeight)
	if err != nil {
		return satlineTxEvidence{}, err
	}
	r.observeVerification(v.VerificationState)
	r.txs[key] = append(r.txs[key], v)
	return v, nil
}

func satlineOutpointKey(txid string, vout uint32) string {
	return fmt.Sprintf("%s:%d", strings.ToLower(txid), vout)
}

func (r *satlineResolver) prevoutValue(txid string, vout uint32, beforeHeight int64) (uint64, error) {
	key := satlineOutpointKey(txid, vout)
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	tx, err := r.transaction(txid, beforeHeight)
	if err != nil {
		return 0, err
	}
	if int(vout) >= len(tx.Tx.Outputs) {
		return 0, fmt.Errorf("prevout %s:%d does not exist", txid, vout)
	}
	v := tx.Tx.Outputs[vout].ValueSats
	r.values[key] = v
	return v, nil
}

func addUint64(a, b uint64) (uint64, error) {
	if math.MaxUint64-a < b {
		return 0, fmt.Errorf("satoshi value overflow")
	}
	return a + b, nil
}

func sumOutputs(tx transactionView) (uint64, error) {
	var total uint64
	for _, out := range tx.Outputs {
		var err error
		total, err = addUint64(total, out.ValueSats)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func (r *satlineResolver) inputValue(tx transactionView, beforeHeight int64) (uint64, error) {
	r.primeInputValues(tx, beforeHeight)
	if tx.Coinbase {
		return 0, fmt.Errorf("coinbase has no ordinary prevout input value")
	}
	var total uint64
	for _, in := range tx.Inputs {
		if in.Coinbase {
			continue
		}
		v, err := r.prevoutValue(in.PrevTxID, in.PrevVout, beforeHeight)
		if err != nil {
			return 0, fmt.Errorf("input %d prevout %s:%d: %w", in.N, in.PrevTxID, in.PrevVout, err)
		}
		total, err = addUint64(total, v)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func (r *satlineResolver) transactionFee(tx transactionView, height int64) (uint64, error) {
	if tx.Coinbase {
		return 0, nil
	}
	in, err := r.inputValue(tx, height)
	if err != nil {
		return 0, err
	}
	out, err := sumOutputs(tx)
	if err != nil {
		return 0, err
	}
	if out > in {
		return 0, fmt.Errorf("transaction %s outputs %d sats exceed inputs %d sats", tx.TxID, out, in)
	}
	return in - out, nil
}

func mapStreamPosition(position uint64, outputs []outputView) (uint32, uint64, bool) {
	remain := position
	for i, out := range outputs {
		if remain < out.ValueSats {
			return uint32(i), remain, true
		}
		remain -= out.ValueSats
	}
	return 0, 0, false
}

func (r *satlineResolver) inputPosition(tx transactionView, vin int, sourceOffset uint64, height int64) (uint64, error) {
	r.primeInputValues(tx, height)
	if vin < 0 || vin >= len(tx.Inputs) {
		return 0, fmt.Errorf("spending vin %d is outside transaction inputs", vin)
	}
	var position uint64
	for i := 0; i < vin; i++ {
		in := tx.Inputs[i]
		if in.Coinbase {
			return 0, fmt.Errorf("ordinary spending transaction unexpectedly contains coinbase input")
		}
		v, err := r.prevoutValue(in.PrevTxID, in.PrevVout, height)
		if err != nil {
			return 0, err
		}
		position, err = addUint64(position, v)
		if err != nil {
			return 0, err
		}
	}
	in := tx.Inputs[vin]
	if in.Coinbase {
		return 0, fmt.Errorf("satline cannot traverse an ordinary sat through a coinbase input")
	}
	sourceValue, err := r.prevoutValue(in.PrevTxID, in.PrevVout, height)
	if err != nil {
		return 0, err
	}
	if sourceOffset >= sourceValue {
		return 0, fmt.Errorf("source satpoint offset %d is outside spent output value %d", sourceOffset, sourceValue)
	}
	return addUint64(position, sourceOffset)
}

func (r *satlineResolver) priorBlockFees(block blockView, beforeTxIndex int) (uint64, error) {
	if beforeTxIndex < 1 || beforeTxIndex > len(block.Transactions) {
		if beforeTxIndex == 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("transaction index %d outside block", beforeTxIndex)
	}
	if prefix, ok := r.coreFeePrefix(block); ok && beforeTxIndex < len(prefix) {
		return prefix[beforeTxIndex], nil
	}
	var total uint64
	for i := 1; i < beforeTxIndex; i++ {
		fee, err := r.transactionFee(block.Transactions[i], block.Height)
		if err != nil {
			return 0, fmt.Errorf("fee for preceding transaction %d: %w", i, err)
		}
		total, err = addUint64(total, fee)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// The two historical BIP30 duplicate coinbase transaction pairs are part of
// ordinal semantics because a later duplicate destroys any still-unspent sats
// in the displaced outputs of the first incarnation.
func bip30ReplacementHeight(originHeight int64) (int64, bool) {
	switch originHeight {
	case 91722:
		return 91880, true
	case 91812:
		return 91842, true
	default:
		return 0, false
	}
}

func (r *satlineResolver) spendBeforeReplacement(point satlinePoint, replacementHeight int64) (*spenderLookupView, error) {
	for h := point.Height + 1; h < replacementHeight; h++ {
		b, err := r.block(h)
		if err != nil {
			return nil, err
		}
		for ti := 1; ti < len(b.Transactions); ti++ {
			tx := b.Transactions[ti]
			for vi, in := range tx.Inputs {
				if strings.EqualFold(in.PrevTxID, point.TxID) && in.PrevVout == point.Vout {
					txCopy := tx
					inCopy := in
					return &spenderLookupView{
						Outpoint: satlineOutpointKey(point.TxID, point.Vout), ConfirmedState: "confirmed_spent", Found: true,
						SpendingTxID: tx.TxID, SpendingVin: vi, Height: h, BlockHash: b.Hash,
						Provider: "bip30_context_scan", VerificationState: b.VerificationState, Transaction: &txCopy, SpendingInput: &inCopy,
						Note: "Satline scanned the finite pre-replacement BIP30 window so the original duplicate-txid incarnation cannot be confused with the later one.",
					}, nil
				}
			}
		}
	}
	return nil, nil
}

func (r *satlineResolver) bip30Disposition(point satlinePoint) (*spenderLookupView, *blockView, error) {
	replacementHeight, ok := bip30ReplacementHeight(point.Height)
	if !ok {
		return nil, nil, nil
	}
	auth := r.backend.ChainAuthority()
	if auth.Height < replacementHeight {
		return nil, nil, nil
	}
	replacement, err := r.block(replacementHeight)
	if err != nil {
		return nil, nil, err
	}
	if len(replacement.Transactions) == 0 || !strings.EqualFold(replacement.Transactions[0].TxID, point.TxID) {
		// The height is one of the historical pairs, but the current point is not
		// the duplicated coinbase txid. Nothing special to do.
		return nil, nil, nil
	}
	spend, err := r.spendBeforeReplacement(point, replacementHeight)
	if err != nil {
		return nil, nil, err
	}
	if spend != nil {
		return spend, nil, nil
	}
	return nil, &replacement, nil
}

func weakerVerification(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if verificationRank(a) <= verificationRank(b) {
		return a
	}
	return b
}

func (r *satlineResolver) traverse(start satlinePoint, maxHops int) satlineResult {
	if maxHops <= 0 || maxHops > satlineMaxHops {
		maxHops = satlineMaxHops
	}
	res := satlineResult{Mode: "satpoint", StartSatpoint: &start, CurrentSatpoint: &start, Hops: []satlineHop{}, VerificationState: r.weakest, Note: "Satline follows confirmed Bitcoin ordinal FIFO movement on demand. It does not advance through mempool transactions."}
	current := start
	for len(res.Hops) < maxHops {
		if r.ctx != nil && r.ctx.Err() != nil {
			res.State = "PAUSED"
			res.Note = "Resolution stopped at the last completed checkpoint."
			break
		}
		// Historical BIP30 duplicate txids need source-incarnation context before
		// generic outpoint lookup, because the outpoint string itself is reused.
		spendOverride, destroyedBy, err := r.bip30Disposition(current)
		if err != nil {
			res.State = "UNRESOLVED"
			res.Note = err.Error()
			break
		}
		if destroyedBy != nil {
			res.State = "LOST_DUPLICATE_TXID"
			res.LostAtHeight, res.LostAtBlockHash = destroyedBy.Height, destroyedBy.Hash
			res.CurrentSatpoint = &current
			res.VerificationState = weakerVerification(r.weakest, destroyedBy.VerificationState)
			res.Note = fmt.Sprintf("The original transaction output was still unspent when Bitcoin's historical duplicate txid at block %d displaced it, destroying the sats in that UTXO under ordinal semantics.", destroyedBy.Height)
			break
		}

		var spend spenderLookupView
		if spendOverride != nil {
			spend = *spendOverride
		} else {
			spend, err = r.backend.Spender(current.TxID, current.Vout)
			if err != nil {
				res.State = "UNRESOLVED"
				res.Note = err.Error()
				break
			}
		}
		r.observeVerification(spend.VerificationState)
		switch spend.ConfirmedState {
		case "unspent_at_snapshot":
			res.State = "CURRENTLY_UNSPENT"
			res.CurrentSatpoint = &current
			res.Snapshot = spend.Snapshot
			if spend.MempoolState == "spent" {
				res.PendingMempoolSpend = spend.MempoolSpendingTxID
			}
			res.Note = "Confirmed lineage terminates at this satpoint through the stated chain snapshot. Mempool movement, if present, is reported separately and is not canonical Satline history."
			res.VerificationState = weakerVerification(r.weakest, spend.VerificationState)
			r.emitProgress(res)
			return res
		case "unknown", "provider_disagreement", "":
			res.State = "UNRESOLVED"
			res.CurrentSatpoint = &current
			res.Note = "Spender knowledge is not complete enough to prove the next confirmed hop: " + spend.Note
			res.VerificationState = weakerVerification(r.weakest, spend.VerificationState)
			r.emitProgress(res)
			return res
		case "confirmed_spent":
			// continue below
		default:
			res.State = "UNRESOLVED"
			res.Note = "Unsupported spender state: " + spend.ConfirmedState
			return res
		}
		if spend.Transaction == nil {
			res.State = "UNRESOLVED"
			res.Note = "Spender provider returned a confirmed spend without the verified spending transaction."
			return res
		}
		tx := *spend.Transaction
		block, err := r.block(spend.Height)
		if err != nil {
			res.State = "UNRESOLVED"
			res.Note = err.Error()
			return res
		}
		// Prefer the transaction's actual block index from the fetched block. A
		// provider's tx object may not have retained a reliable Index field.
		txIndex := -1
		for i := range block.Transactions {
			if strings.EqualFold(block.Transactions[i].TxID, tx.TxID) {
				tx = block.Transactions[i]
				txIndex = i
				break
			}
		}
		if txIndex < 0 {
			res.State = "UNRESOLVED"
			res.Note = "Spending transaction was not present in its claimed block."
			return res
		}
		vin := spend.SpendingVin
		if vin < 0 || vin >= len(tx.Inputs) || !strings.EqualFold(tx.Inputs[vin].PrevTxID, current.TxID) || tx.Inputs[vin].PrevVout != current.Vout {
			vin = -1
			for i, in := range tx.Inputs {
				if strings.EqualFold(in.PrevTxID, current.TxID) && in.PrevVout == current.Vout {
					vin = i
					break
				}
			}
		}
		if vin < 0 {
			res.State = "UNRESOLVED"
			res.Note = "Verified spending transaction does not contain the expected source outpoint."
			return res
		}
		position, err := r.inputPosition(tx, vin, current.Offset, block.Height)
		if err != nil {
			res.State = "UNRESOLVED"
			res.Note = err.Error()
			return res
		}
		outTotal, err := sumOutputs(tx)
		if err != nil {
			res.State = "UNRESOLVED"
			res.Note = err.Error()
			return res
		}
		hop := satlineHop{Index: len(res.Hops), Source: current, SpendingTxID: tx.TxID, SpendingVin: vin, BlockHeight: block.Height, BlockHash: block.Hash, InputStreamPosition: position, VerificationState: weakerVerification(block.VerificationState, spend.VerificationState), SpenderProvider: spend.Provider}
		if position < outTotal {
			vout, off, ok := mapStreamPosition(position, tx.Outputs)
			if !ok {
				res.State = "UNRESOLVED"
				res.Note = "FIFO output mapping failed despite position being inside output total."
				return res
			}
			next := satlinePoint{TxID: tx.TxID, Vout: vout, Offset: off, Height: block.Height, BlockHash: block.Hash, TxIndex: txIndex}
			hop.Type, hop.Destination = "output_to_output", next
			hop.Note = "Sat mapped from the concatenated transaction input stream into outputs in FIFO order."
			res.Hops = append(res.Hops, hop)
			res.HopCount = len(res.Hops)
			res.VerificationState = r.weakest
			current = next
			res.CurrentSatpoint = &current
			r.emitProgress(res)
			res.CurrentSatpoint = &current
			continue
		}

		// Position beyond normal outputs is a fee sat. Calculate its exact place
		// in the coinbase's implicit [subsidy, fee1, fee2, ...] input stream.
		feeOffset := position - outTotal
		fee, err := r.transactionFee(tx, block.Height)
		if err != nil {
			res.State = "UNRESOLVED"
			res.Note = err.Error()
			return res
		}
		if feeOffset >= fee {
			res.State = "UNRESOLVED"
			res.Note = fmt.Sprintf("fee-relative offset %d is outside transaction fee %d", feeOffset, fee)
			return res
		}
		priorFees, err := r.priorBlockFees(block, txIndex)
		if err != nil {
			res.State = "UNRESOLVED"
			res.Note = err.Error()
			return res
		}
		coinPos, err := addUint64(subsidyAtHeight(block.Height), priorFees)
		if err == nil {
			coinPos, err = addUint64(coinPos, feeOffset)
		}
		if err != nil {
			res.State = "UNRESOLVED"
			res.Note = err.Error()
			return res
		}
		if len(block.Transactions) == 0 || !block.Transactions[0].Coinbase {
			res.State = "UNRESOLVED"
			res.Note = "Containing block has no decodable coinbase transaction."
			return res
		}
		coinbase := block.Transactions[0]
		vout, off, ok := mapStreamPosition(coinPos, coinbase.Outputs)
		hop.Type = "fee_to_coinbase"
		hop.TransactionFeeSats = fee
		hop.FeeOffset = feeOffset
		hop.PriorBlockFeesSats = priorFees
		hop.CoinbaseStreamOffset = coinPos
		if !ok {
			res.Hops = append(res.Hops, hop)
			res.HopCount = len(res.Hops)
			res.State = "LOST_UNCLAIMED_COINBASE"
			res.CurrentSatpoint = &current
			res.LostAtHeight, res.LostAtBlockHash = block.Height, block.Hash
			res.VerificationState = weakerVerification(r.weakest, block.VerificationState)
			res.Note = "The sat entered the block fee stream, but the coinbase transaction claimed too little value for that stream position to reach any coinbase output."
			r.emitProgress(res)
			return res
		}
		next := satlinePoint{TxID: coinbase.TxID, Vout: vout, Offset: off, Height: block.Height, BlockHash: block.Hash, TxIndex: 0}
		hop.Destination = next
		hop.Note = "Sat became a transaction fee and was appended to the coinbase implicit input after the subsidy and fees of preceding block transactions."
		res.Hops = append(res.Hops, hop)
		res.HopCount = len(res.Hops)
		res.VerificationState = r.weakest
		current = next
		res.CurrentSatpoint = &current
		r.emitProgress(res)
		res.CurrentSatpoint = &current
	}
	res.HopCount = len(res.Hops)
	res.VerificationState = r.weakest
	if res.State == "" {
		res.State = "STEP_LIMIT"
		res.Note = fmt.Sprintf("Traversal paused after %d hop(s). Continue from current_satpoint to resolve further.", len(res.Hops))
	}
	r.emitProgress(res)
	return res
}

func (r *satlineResolver) resolveSat(sat uint64, maxHops int) satlineResult {
	res := satlineResult{Mode: "sat", SatNumber: &sat, Hops: []satlineHop{}, ChainTipHeight: -1}
	iss, ok := issuanceForSat(sat)
	if !ok {
		res.State = "INVALID"
		res.Note = fmt.Sprintf("Sat number is outside the theoretical Bitcoin ordinal supply (0..%d).", theoreticalSatSupply()-1)
		return res
	}
	res.Issuance = &iss
	auth := r.backend.ChainAuthority()
	res.ChainTipHeight = auth.Height
	if auth.Height < iss.Height {
		res.State = "UNMINED"
		res.ExpectedIssuanceHeight = iss.Height
		res.Note = fmt.Sprintf("This is a valid theoretical ordinal, but its issuance block %d is beyond the current selected chain tip %d.", iss.Height, auth.Height)
		return res
	}
	birthBlock, err := r.block(iss.Height)
	if err != nil {
		res.State = "UNRESOLVED"
		res.Note = "Could not obtain issuance block: " + err.Error()
		return res
	}
	if len(birthBlock.Transactions) == 0 || !birthBlock.Transactions[0].Coinbase {
		res.State = "UNRESOLVED"
		res.Note = "Issuance block has no decodable coinbase transaction."
		return res
	}
	coinbase := birthBlock.Transactions[0]
	vout, off, mapped := mapStreamPosition(iss.SubsidyOffset, coinbase.Outputs)
	if !mapped {
		res.State = "LOST_AT_BIRTH"
		res.LostAtHeight, res.LostAtBlockHash = birthBlock.Height, birthBlock.Hash
		res.VerificationState = birthBlock.VerificationState
		res.Note = "The theoretical subsidy position was not claimed by any coinbase output. Underpaying a subsidy does not renumber later ordinals, so this sat is lost rather than reassigned."
		return res
	}
	birth := satlinePoint{TxID: coinbase.TxID, Vout: vout, Offset: off, Height: birthBlock.Height, BlockHash: birthBlock.Hash, TxIndex: 0}
	res.BirthSatpoint = &birth
	if maxHops == -1 {
		res.CurrentSatpoint = &birth
		res.State = "STEP_LIMIT"
		res.VerificationState = r.weakest
		res.Note = "Birth located. Ready for the next confirmed hop."
		return res
	}
	base := res
	base.BirthSatpoint = &birth
	r.progressBase = &base
	walk := r.traverse(birth, maxHops)
	walk.Mode = "sat"
	walk.SatNumber = &sat
	walk.Issuance = &iss
	walk.BirthSatpoint = &birth
	walk.StartSatpoint = nil
	walk.ChainTipHeight = auth.Height
	return walk
}

func parseRawSatpoint(s string) (string, uint32, uint64, error) {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(s)), ":")
	if len(parts) != 3 || !validHash(parts[0]) {
		return "", 0, 0, fmt.Errorf("satpoint must be txid:vout:offset")
	}
	vout, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid satpoint vout")
	}
	off, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid satpoint offset")
	}
	return parts[0], uint32(vout), off, nil
}

func (r *satlineResolver) resolveStartPoint(input string) (satlinePoint, error) {
	input = strings.TrimSpace(input)
	// A positional Bitcoin on Demand satpoint carries chain context and is the
	// preferred human-readable form for historical duplicate-txid safety.
	norm := input
	if v := normalizeBrowserResourceAddress(input); v.Valid && v.Namespace == ".bitcoin" {
		norm = strings.TrimSuffix(v.Address, ".bitcoin")
	}
	if c, ok := parseBODCoordinate(norm); ok && c.Kind == coordSatpoint {
		b, err := r.block(c.Height)
		if err != nil {
			return satlinePoint{}, err
		}
		if c.TxIndex < 0 || c.TxIndex >= len(b.Transactions) {
			return satlinePoint{}, fmt.Errorf("transaction index %d does not exist in block %d", c.TxIndex, c.Height)
		}
		tx := b.Transactions[c.TxIndex]
		if c.IOIndex < 0 || c.IOIndex >= len(tx.Outputs) {
			return satlinePoint{}, fmt.Errorf("output %d does not exist", c.IOIndex)
		}
		if c.SatOffset >= tx.Outputs[c.IOIndex].ValueSats {
			return satlinePoint{}, fmt.Errorf("satpoint offset %d is outside output value %d", c.SatOffset, tx.Outputs[c.IOIndex].ValueSats)
		}
		return satlinePoint{TxID: tx.TxID, Vout: uint32(c.IOIndex), Offset: c.SatOffset, Height: b.Height, BlockHash: b.Hash, TxIndex: c.TxIndex}, nil
	}

	txid, vout, off, err := parseRawSatpoint(input)
	if err != nil {
		return satlinePoint{}, err
	}
	auth := r.backend.ChainAuthority()
	tx, err := r.transaction(txid, auth.Height)
	if err != nil {
		return satlinePoint{}, err
	}
	// A raw satpoint lacks origin-height context. If the located transaction is
	// one of the known BIP30 duplicates, refuse to guess which incarnation the
	// caller intended; the positional .bitcoin form can express that context.
	if repl, ok := bip30ReplacementHeight(tx.Height); ok {
		if b, e := r.block(repl); e == nil && len(b.Transactions) > 0 && strings.EqualFold(b.Transactions[0].TxID, txid) {
			return satlinePoint{}, fmt.Errorf("AMBIGUOUS_HISTORICAL_CONTEXT: txid is one of Bitcoin's BIP30 duplicates; use a positional satpoint with block height")
		}
	}
	if tx.Tx.Index == 0 {
		for _, origin := range []int64{91722, 91812} {
			if b, e := r.block(origin); e == nil && len(b.Transactions) > 0 && strings.EqualFold(b.Transactions[0].TxID, txid) && tx.Height != origin {
				return satlinePoint{}, fmt.Errorf("AMBIGUOUS_HISTORICAL_CONTEXT: txid is one of Bitcoin's BIP30 duplicates; use a positional satpoint with block height")
			}
		}
	}
	if int(vout) >= len(tx.Tx.Outputs) {
		return satlinePoint{}, fmt.Errorf("vout %d does not exist in transaction", vout)
	}
	if off >= tx.Tx.Outputs[vout].ValueSats {
		return satlinePoint{}, fmt.Errorf("satpoint offset %d is outside output value %d", off, tx.Tx.Outputs[vout].ValueSats)
	}
	return satlinePoint{TxID: txid, Vout: vout, Offset: off, Height: tx.Height, BlockHash: tx.BlockHash, TxIndex: tx.Tx.Index}, nil
}

func (r *satlineResolver) follow(input string, maxHops int) satlineResult {
	p, err := r.resolveStartPoint(input)
	if err != nil {
		state := "INVALID"
		if strings.Contains(err.Error(), "AMBIGUOUS_HISTORICAL_CONTEXT") {
			state = "AMBIGUOUS_HISTORICAL_CONTEXT"
		}
		return satlineResult{Mode: "satpoint", State: state, Hops: []satlineHop{}, Note: err.Error()}
	}
	base := satlineResult{Mode: "satpoint", StartSatpoint: &p, CurrentSatpoint: &p, Hops: []satlineHop{}, ChainTipHeight: r.backend.ChainAuthority().Height}
	r.progressBase = &base
	if maxHops == -1 {
		base.State = "STEP_LIMIT"
		base.VerificationState = r.weakest
		base.Note = "Starting satpoint located. Birth history has not been resolved."
		return base
	}
	return r.traverse(p, maxHops)
}

func (a *app) resolveSatlineSat(sat uint64, maxHops int) satlineResult {
	return a.resolveSatlineSatPersistent(sat, maxHops, false)
}

func (a *app) followSatlineSatpoint(input string, maxHops int) satlineResult {
	return a.followSatlinePersistent(input, maxHops, false)
}
