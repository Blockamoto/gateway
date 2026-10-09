package main

type appStatus struct {
	HeaderBootstrapState  string  `json:"header_bootstrap_state,omitempty"`
	HeaderBootstrapHeight int64   `json:"header_bootstrap_height,omitempty"`
	HeaderBootstrapError  string  `json:"header_bootstrap_error,omitempty"`
	HeaderState           string  `json:"header_state"`
	HeaderTargetSource    string  `json:"header_target_source,omitempty"`
	HeaderLastProgress    string  `json:"header_last_progress,omitempty"`
	HeaderSpeed           float64 `json:"header_speed,omitempty"`

	Ready                bool   `json:"ready"`
	Syncing              bool   `json:"syncing"`
	HeaderCount          int64  `json:"header_count"`
	HeaderHeight         int64  `json:"header_height"`
	HeaderTargetHeight   int64  `json:"header_target_height,omitempty"`
	TipHash              string `json:"tip_hash,omitempty"`
	HeaderSource         string `json:"header_source,omitempty"`
	ChainAuthority       string `json:"chain_authority,omitempty"`
	ChainAuthorityHeight int64  `json:"chain_authority_height,omitempty"`
	ChainAuthorityHash   string `json:"chain_authority_hash,omitempty"`
	HeaderMirrorRequired bool   `json:"header_mirror_required"`
	Message              string `json:"message"`
	Error                string `json:"error,omitempty"`
}

type verificationView struct {
	VerifierVersion    int  `json:"verifier_version"`
	WitnessCommitment  bool `json:"witness_commitment"`
	WitnessPresent     bool `json:"witness_present"`
	P2PChecksum        bool `json:"p2p_checksum"`
	HeaderHash         bool `json:"header_hash"`
	HeaderChainMatch   bool `json:"header_chain_match"`
	ProofOfWork        bool `json:"proof_of_work"`
	MerkleRoot         bool `json:"merkle_root"`
	TransactionsParsed bool `json:"transactions_parsed"`
	ConsensusValidated bool `json:"consensus_validated"`
}

type savedFilesView struct {
	Block string `json:"block"`
	Hex   string `json:"hex"`
	JSON  string `json:"json"`
}

type blockView struct {
	Evidence           *resourceEvidence `json:"evidence,omitempty"`
	Coordinate         string            `json:"coordinate,omitempty"`
	Height             int64             `json:"height"`
	Hash               string            `json:"hash"`
	PreviousBlockHash  string            `json:"previous_block_hash"`
	MerkleRoot         string            `json:"merkle_root"`
	ComputedMerkleRoot string            `json:"computed_merkle_root"`
	Version            int32             `json:"version"`
	Time               uint32            `json:"time"`
	TimeISO            string            `json:"time_iso"`
	Bits               string            `json:"bits"`
	Nonce              uint32            `json:"nonce"`
	TransactionCount   uint64            `json:"transaction_count"`
	SerializedBytes    int               `json:"serialized_bytes"`
	StrippedBytes      int               `json:"stripped_bytes"`
	Weight             int               `json:"weight"`
	VSize              int               `json:"vsize"`
	InputCount         int               `json:"input_count"`
	OutputCount        int               `json:"output_count"`
	TotalOutputSats    uint64            `json:"total_output_sats"`
	SegwitTransactions int               `json:"segwit_transactions"`
	SourcePeer         string            `json:"source_peer"`
	SourceNetwork      string            `json:"source_network,omitempty"` // bitcoin|bod|cache|core|core_mount
	LocatorPeer        string            `json:"locator_peer,omitempty"`
	FromCache          bool              `json:"from_cache"`
	CacheVisibility    string            `json:"cache_visibility,omitempty"` // public|private for BOD cache objects
	VerificationState  string            `json:"verification_state"`         // structurally_checked|pending_header_validation|header_anchored|consensus_validated
	Verification       verificationView  `json:"verification"`
	SavedFiles         savedFilesView    `json:"saved_files"`
	Transactions       []transactionView `json:"transactions"`
	Note               string            `json:"note"`
}

type transactionView struct {
	Coordinate   string       `json:"coordinate,omitempty"`
	Index        int          `json:"index"`
	TxID         string       `json:"txid"`
	WTxID        string       `json:"wtxid"`
	Version      int32        `json:"version"`
	LockTime     uint32       `json:"lock_time"`
	Segwit       bool         `json:"segwit"`
	Coinbase     bool         `json:"coinbase"`
	Size         int          `json:"size"`
	StrippedSize int          `json:"stripped_size"`
	Weight       int          `json:"weight"`
	VSize        int          `json:"vsize"`
	InputCount   int          `json:"input_count"`
	OutputCount  int          `json:"output_count"`
	OutputSats   uint64       `json:"output_sats"`
	Inputs       []inputView  `json:"inputs"`
	Outputs      []outputView `json:"outputs"`
	_txHash      [32]byte
}

type inputView struct {
	Coordinate string   `json:"coordinate,omitempty"`
	N          int      `json:"n"`
	PrevTxID   string   `json:"prev_txid,omitempty"`
	PrevVout   uint32   `json:"prev_vout"`
	ScriptSig  string   `json:"script_sig"`
	Sequence   uint32   `json:"sequence"`
	Witness    []string `json:"witness,omitempty"`
	Coinbase   bool     `json:"coinbase"`
}

type outputView struct {
	Coordinate   string `json:"coordinate,omitempty"`
	N            int    `json:"n"`
	ValueSats    uint64 `json:"value_sats"`
	ScriptPubKey string `json:"script_pub_key"`
	Type         string `json:"type"`
	Address      string `json:"address,omitempty"`
}

type p2pStatusView struct {
	Version         string                `json:"version"`
	Compatibility   compatibilityView     `json:"compatibility"`
	StorageMode     string                `json:"storage_mode"`
	Sharing         bool                  `json:"sharing"`
	PeerID          string                `json:"peer_id,omitempty"`
	ListenPort      int                   `json:"listen_port"`
	BitcoinP2P      bitcoinP2PServingView `json:"bitcoin_p2p"`
	DiscoveredPeers []overlayPeer         `json:"discovered_peers"`
	Core            coreStatus            `json:"core"`
	CoreStore       coreStoreStatus       `json:"core_store"`
	Settings        publicSettings        `json:"settings"`
	CacheBytes      int64                 `json:"cache_bytes"`
	CacheBlocks     int                   `json:"cache_blocks"`
}
