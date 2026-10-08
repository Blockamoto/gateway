package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type coreRPC struct {
	url        string
	user       string
	pass       string
	authSource string
	cookiePath string
	configPath string
	http       *http.Client
}

type coreStatus struct {
	Connected            bool   `json:"connected"`
	P2PReachable         bool   `json:"p2p_reachable"`
	P2PAddr              string `json:"p2p_addr,omitempty"`
	Pruned               bool   `json:"pruned"`
	PruneHeight          int64  `json:"prune_height,omitempty"`
	DataDir              string `json:"data_dir,omitempty"`
	Height               int64  `json:"height,omitempty"`
	Headers              int64  `json:"headers,omitempty"`
	InitialBlockDownload bool   `json:"initial_block_download,omitempty"`
	BestBlockHash        string `json:"best_block_hash,omitempty"`
	TxIndex              bool   `json:"txindex"` // synced/fully usable
	TxIndexEnabled       bool   `json:"txindex_enabled"`
	TxIndexHeight        int64  `json:"txindex_height,omitempty"`
	SpenderIndex         bool   `json:"txospenderindex"` // synced/authoritative for negatives
	SpenderIndexEnabled  bool   `json:"txospenderindex_enabled"`
	SpenderIndexHeight   int64  `json:"txospenderindex_height,omitempty"`
	RPCURL               string `json:"rpc_url,omitempty"`
	RPCUser              string `json:"rpc_user,omitempty"`
	AuthSource           string `json:"auth_source,omitempty"`
	PasswordDetected     bool   `json:"password_detected"`
	ConfigPath           string `json:"config_path,omitempty"`
	CookiePath           string `json:"cookie_path,omitempty"`
	RPCAuthority         bool   `json:"rpcauth_present"`
	Error                string `json:"error,omitempty"`
}

func resolveCookiePath(dataDir, configured string) string {
	if strings.TrimSpace(configured) == "" {
		return filepath.Join(dataDir, ".cookie")
	}
	p := strings.TrimSpace(configured)
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dataDir, p)
}

func cookieCredentials(path string) (string, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid Bitcoin Core cookie")
	}
	return parts[0], parts[1], nil
}

func newCoreRPC(s appSettings) (*coreRPC, error) {
	if s.CoreDisabled {
		return nil, fmt.Errorf("PROVIDER_UNAVAILABLE: Bitcoin Core is disabled by your provider choice")
	}
	dataDir := strings.TrimSpace(s.BitcoinDataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("Bitcoin Core data directory is not configured")
	}
	conf := parseBitcoinConf(dataDir)
	port := 8332
	if conf.RPCPort > 0 {
		port = conf.RPCPort
	}
	if s.RPCPort > 0 {
		port = s.RPCPort
	}
	c := &coreRPC{
		url:        fmt.Sprintf("http://127.0.0.1:%d", port),
		configPath: filepath.Join(dataDir, "bitcoin.conf"),
		cookiePath: resolveCookiePath(dataDir, conf.RPCCookieFile),
		http:       &http.Client{Timeout: 45 * time.Second, Transport: coreTransport},
	}

	mode := strings.ToLower(strings.TrimSpace(s.RPCAuthMode))
	if mode == "" {
		mode = "auto"
	}
	manualUser, manualPass := strings.TrimSpace(s.RPCUser), s.RPCPassword

	useCookie := func() error {
		user, pass, err := cookieCredentials(c.cookiePath)
		if err != nil {
			return err
		}
		c.user, c.pass, c.authSource = user, pass, "cookie"
		return nil
	}
	useUserPass := func() error {
		// Explicit GUI values win. Missing pieces may be filled from bitcoin.conf.
		user, pass := manualUser, manualPass
		if user == "" {
			user = conf.RPCUser
		}
		if pass == "" {
			pass = conf.RPCPassword
		}
		if user == "" && conf.RPCAuthUser != "" {
			user = conf.RPCAuthUser
		}
		if user == "" || pass == "" {
			if conf.HasRPCAuth && user != "" {
				return fmt.Errorf("bitcoin.conf uses rpcauth for user %q; enter that user's RPC password in Gateway", user)
			}
			return fmt.Errorf("RPC username/password not found; enter them in Gateway or configure cookie authentication")
		}
		c.user, c.pass = user, pass
		if manualUser != "" || manualPass != "" {
			c.authSource = "GUI username/password"
		} else {
			c.authSource = "bitcoin.conf rpcuser/rpcpassword"
		}
		return nil
	}

	switch mode {
	case "cookie":
		if err := useCookie(); err != nil {
			return nil, fmt.Errorf("cookie authentication: %w", err)
		}
	case "userpass", "username/password", "password":
		if err := useUserPass(); err != nil {
			return nil, err
		}
	case "auto":
		if err := useCookie(); err == nil {
			break
		}
		if err := useUserPass(); err != nil {
			return nil, fmt.Errorf("no usable Bitcoin Core RPC authentication found (cookie %s missing/unreadable; %v)", c.cookiePath, err)
		}
	default:
		return nil, fmt.Errorf("unknown RPC auth mode %q", mode)
	}
	return c, nil
}

func (c *coreRPC) call(method string, params any, out any) error {
	return c.callContext(context.Background(), method, params, out)
}
func (c *coreRPC) callContext(ctx context.Context, method string, params any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "bod", "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 64<<20 {
		return fmt.Errorf("Core RPC response exceeded 64 MiB budget")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("Bitcoin Core rejected the RPC credentials (HTTP 401)")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return fmt.Errorf("Core RPC response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("Core RPC %s: %s", method, envelope.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return err
		}
	}
	return nil
}

func inspectCoreFresh(s appSettings) coreStatus {
	if s.CoreDisabled {
		return coreStatus{DataDir: s.BitcoinDataDir, Height: -1, Error: "Bitcoin Core is disabled; Gateway uses its independent providers."}
	}
	st := coreStatus{DataDir: s.BitcoinDataDir, ConfigPath: filepath.Join(s.BitcoinDataDir, "bitcoin.conf")}
	conf := parseBitcoinConf(s.BitcoinDataDir)
	if addr, ok := coreP2PAddr(s); ok {
		st.P2PAddr = addr
		if conn, err := net.DialTimeout("tcp", addr, 450*time.Millisecond); err == nil {
			st.P2PReachable = true
			_ = conn.Close()
		}
	}
	st.RPCAuthority = conf.HasRPCAuth
	st.PasswordDetected = conf.RPCPassword != "" || s.RPCPassword != ""
	if s.RPCUser != "" {
		st.RPCUser = s.RPCUser
	} else if conf.RPCUser != "" {
		st.RPCUser = conf.RPCUser
	} else {
		st.RPCUser = conf.RPCAuthUser
	}
	st.CookiePath = resolveCookiePath(s.BitcoinDataDir, conf.RPCCookieFile)
	c, err := newCoreRPC(s)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	// Status/onboarding probes must never make the UI feel frozen. Local Core
	// should answer essentially immediately; heavier RPC operations retain the
	// normal 12s client timeout when they create their own coreRPC.
	c.http.Timeout = 3 * time.Second
	st.RPCURL, st.RPCUser, st.AuthSource, st.CookiePath, st.ConfigPath = c.url, c.user, c.authSource, c.cookiePath, c.configPath
	st.PasswordDetected = c.pass != ""
	var bc struct {
		Blocks               int64  `json:"blocks"`
		Headers              int64  `json:"headers"`
		BestBlockHash        string `json:"bestblockhash"`
		Pruned               bool   `json:"pruned"`
		PruneHeight          int64  `json:"pruneheight"`
		InitialBlockDownload bool   `json:"initialblockdownload"`
	}
	if err := c.call("getblockchaininfo", []any{}, &bc); err != nil {
		st.Error = err.Error()
		return st
	}
	st.Connected, st.Height, st.Headers, st.InitialBlockDownload, st.BestBlockHash, st.Pruned, st.PruneHeight = true, bc.Blocks, bc.Headers, bc.InitialBlockDownload, strings.ToLower(bc.BestBlockHash), bc.Pruned, bc.PruneHeight
	var idx map[string]struct {
		Synced bool  `json:"synced"`
		Best   int64 `json:"best_block_height"`
	}
	if err := c.call("getindexinfo", []any{}, &idx); err == nil {
		if v, ok := idx["txindex"]; ok {
			st.TxIndexEnabled, st.TxIndex, st.TxIndexHeight = true, v.Synced, v.Best
		}
		if v, ok := idx["txospenderindex"]; ok {
			st.SpenderIndexEnabled, st.SpenderIndex, st.SpenderIndexHeight = true, v.Synced, v.Best
		}
	}
	return st
}

type txLocation struct {
	TxID      string `json:"txid"`
	BlockHash string `json:"block_hash"`
	Height    int64  `json:"height"`
	TxIndex   int    `json:"tx_index"` // -1 when the provider did not compute it
}

func coreTxIndexUsable(status coreStatus) bool {
	return status.Connected && status.TxIndexEnabled && status.TxIndex && !status.InitialBlockDownload && status.Height >= 0 && status.TxIndexHeight >= status.Height && validHash(status.BestBlockHash)
}

func coreTxLocation(s appSettings, txid string) (txLocation, error) {
	// A bare historical transaction ID needs Core's complete txindex. Known
	// Gateway block locations use block RPC directly and do not need this index.
	status := inspectCore(s)
	if !coreTxIndexUsable(status) {
		return txLocation{}, fmt.Errorf("PROVIDER_UNAVAILABLE: historical transaction lookup requires Bitcoin Core with a synced txindex at its current tip")
	}
	c, err := newCoreRPC(s)
	if err != nil {
		return txLocation{}, err
	}
	var tx struct {
		TxID      string `json:"txid"`
		BlockHash string `json:"blockhash"`
	}
	if err := c.call("getrawtransaction", []any{txid, true}, &tx); err != nil {
		return txLocation{}, err
	}
	if tx.BlockHash == "" {
		return txLocation{}, fmt.Errorf("transaction is not confirmed")
	}
	var hdr struct {
		Height int64 `json:"height"`
	}
	if err := c.call("getblockheader", []any{tx.BlockHash, true}, &hdr); err != nil {
		return txLocation{}, err
	}
	idx := -1
	return txLocation{TxID: strings.ToLower(txid), BlockHash: strings.ToLower(tx.BlockHash), Height: hdr.Height, TxIndex: idx}, nil
}

type spendLocation struct {
	Outpoint     string `json:"outpoint"`
	Found        bool   `json:"found"`
	SpendingTxID string `json:"spending_txid,omitempty"`
	BlockHash    string `json:"block_hash,omitempty"`
	Height       int64  `json:"height,omitempty"`
	InputIndex   int    `json:"input_index"` // -1 when not supplied by the provider
}

func coreSpendQuery(s appSettings, txid string, vout uint32, mempoolOnly bool) (spendLocation, error) {
	c, err := newCoreRPC(s)
	if err != nil {
		return spendLocation{}, err
	}
	var rows []struct {
		TxID         string `json:"txid"`
		Vout         uint32 `json:"vout"`
		SpendingTxID string `json:"spendingtxid"`
		BlockHash    string `json:"blockhash"`
	}
	params := []any{[]any{map[string]any{"txid": txid, "vout": vout}}, map[string]any{"mempool_only": mempoolOnly}}
	if err := c.call("gettxspendingprevout", params, &rows); err != nil {
		return spendLocation{}, err
	}
	out := spendLocation{Outpoint: fmt.Sprintf("%s:%d", strings.ToLower(txid), vout), InputIndex: -1}
	if len(rows) == 0 || rows[0].SpendingTxID == "" {
		return out, nil
	}
	out.Found, out.SpendingTxID, out.BlockHash = true, strings.ToLower(rows[0].SpendingTxID), strings.ToLower(rows[0].BlockHash)
	if out.BlockHash != "" {
		var hdr struct {
			Height int64 `json:"height"`
		}
		if err := c.call("getblockheader", []any{out.BlockHash, true}, &hdr); err == nil {
			out.Height = hdr.Height
		}
	}
	return out, nil
}

// coreMempoolSpend asks only the live mempool. It is an overlay on top of the
// confirmed graph and never changes a confirmed-chain state by itself.
func coreMempoolSpend(s appSettings, txid string, vout uint32) (spendLocation, error) {
	return coreSpendQuery(s, txid, vout, true)
}

// coreConfirmedSpend may return a mempool-only row when the output is not yet
// spent in the confirmed chain. Callers distinguish that by BlockHash == "".
// A negative is authoritative only when inspectCore reports a fully synced
// txospenderindex.
func coreConfirmedSpend(s appSettings, txid string, vout uint32) (spendLocation, error) {
	return coreSpendQuery(s, txid, vout, false)
}

// coreConfirmedUnspent proves that the queried outpoint actually exists in
// Core's confirmed UTXO set. A negative txospenderindex result by itself is not
// enough because a nonexistent outpoint also has no spender. include_mempool=false
// keeps the confirmed-chain state separate from the mempool overlay.
func coreConfirmedUnspent(s appSettings, txid string, vout uint32) (bool, string, error) {
	c, err := newCoreRPC(s)
	if err != nil {
		return false, "", err
	}
	var row *struct {
		BestBlock string `json:"bestblock"`
	}
	if err := c.call("gettxout", []any{strings.ToLower(txid), vout, false}, &row); err != nil {
		return false, "", err
	}
	if row == nil {
		return false, "", nil
	}
	return true, strings.ToLower(row.BestBlock), nil
}

// Legacy helper retained for the BOD wire v1 spendloc path.
func coreSpendLocation(s appSettings, txid string, vout uint32) (spendLocation, error) {
	return coreConfirmedSpend(s, txid, vout)
}

type addressUTXO struct {
	TxID          string `json:"txid"`
	Vout          uint32 `json:"vout"`
	Outpoint      string `json:"outpoint"`
	ValueSats     uint64 `json:"value_sats"`
	ScriptPubKey  string `json:"script_pub_key"`
	Descriptor    string `json:"descriptor,omitempty"`
	BlockHash     string `json:"block_hash,omitempty"`
	Height        int64  `json:"height,omitempty"`
	Confirmations int64  `json:"confirmations,omitempty"`
}

type addressUTXOSet struct {
	Address               string        `json:"address"`
	Success               bool          `json:"success"`
	ScanHeight            int64         `json:"scan_height"`
	BestBlock             string        `json:"best_block"`
	TxOuts                int64         `json:"txouts"`
	TotalSats             uint64        `json:"total_sats"`
	UTXOs                 []addressUTXO `json:"utxos"`
	Source                string        `json:"source,omitempty"`
	AuthoritativeNegative bool          `json:"authoritative_negative"`
}

func btcFloatToSats(v float64) uint64 {
	if v <= 0 {
		return 0
	}
	return uint64(v*100000000 + 0.5)
}

// coreAddressUTXOs asks Bitcoin Core's current UTXO set for outputs matching an
// address descriptor. This is intentionally current-state only: it does not
// pretend to be an address-history index.
func coreAddressUTXOs(s appSettings, address string) (addressUTXOSet, error) {
	address = strings.TrimSpace(address)
	if address == "" || len(address) > 160 {
		return addressUTXOSet{}, fmt.Errorf("enter a Bitcoin address")
	}
	c, err := newCoreRPC(s)
	if err != nil {
		return addressUTXOSet{}, err
	}
	var scan struct {
		Success     bool    `json:"success"`
		TxOuts      int64   `json:"txouts"`
		Height      int64   `json:"height"`
		BestBlock   string  `json:"bestblock"`
		TotalAmount float64 `json:"total_amount"`
		Unspents    []struct {
			TxID          string  `json:"txid"`
			Vout          uint32  `json:"vout"`
			ScriptPubKey  string  `json:"scriptPubKey"`
			Desc          string  `json:"desc"`
			Amount        float64 `json:"amount"`
			BlockHash     string  `json:"blockhash"`
			Confirmations int64   `json:"confirmations"`
		} `json:"unspents"`
	}
	desc := fmt.Sprintf("addr(%s)", address)
	if err := c.call("scantxoutset", []any{"start", []any{desc}}, &scan); err != nil {
		return addressUTXOSet{}, err
	}
	out := addressUTXOSet{
		Address: address, Success: scan.Success, ScanHeight: scan.Height,
		BestBlock: strings.ToLower(scan.BestBlock), TxOuts: scan.TxOuts,
		TotalSats: btcFloatToSats(scan.TotalAmount), Source: "Bitcoin Core current UTXO set",
		// A synced full node's scantxoutset result is authoritative for that node's
		// current chainstate. Light peers still treat it as a peer-supplied state claim.
		AuthoritativeNegative: true,
	}
	for _, u := range scan.Unspents {
		h := int64(0)
		if scan.Height > 0 && u.Confirmations > 0 {
			h = scan.Height - u.Confirmations + 1
		}
		out.UTXOs = append(out.UTXOs, addressUTXO{
			TxID: strings.ToLower(u.TxID), Vout: u.Vout,
			Outpoint:  fmt.Sprintf("%s:%d", strings.ToLower(u.TxID), u.Vout),
			ValueSats: btcFloatToSats(u.Amount), ScriptPubKey: strings.ToLower(u.ScriptPubKey),
			Descriptor: u.Desc, BlockHash: strings.ToLower(u.BlockHash), Height: h, Confirmations: u.Confirmations,
		})
	}
	return out, nil
}

func coreBlockLocationByHeight(s appSettings, height int64) (blockLocation, error) {
	if height < 0 {
		return blockLocation{}, fmt.Errorf("negative block height")
	}
	c, err := newCoreRPC(s)
	if err != nil {
		return blockLocation{}, err
	}
	var hash string
	if err := c.call("getblockhash", []any{height}, &hash); err != nil {
		return blockLocation{}, err
	}
	return blockLocation{Height: height, BlockHash: strings.ToLower(hash)}, nil
}

func coreBlockLocationByHash(s appSettings, hash string) (blockLocation, error) {
	return coreBlockLocationByHashContext(context.Background(), s, hash)
}

func coreBlockLocationByHashContext(ctx context.Context, s appSettings, hash string) (blockLocation, error) {
	if !validHash(hash) {
		return blockLocation{}, fmt.Errorf("invalid block hash")
	}
	c, err := newCoreRPC(s)
	if err != nil {
		return blockLocation{}, err
	}
	var hdr struct {
		Height        int64 `json:"height"`
		Confirmations int64 `json:"confirmations"`
	}
	if err := c.callContext(ctx, "getblockheader", []any{strings.ToLower(hash), true}, &hdr); err != nil {
		return blockLocation{}, err
	}
	if hdr.Confirmations < 0 {
		return blockLocation{}, fmt.Errorf("block is known to Bitcoin Core but is not on its active chain")
	}
	return blockLocation{Height: hdr.Height, BlockHash: strings.ToLower(hash)}, nil
}

// coreP2PAddr returns the local Bitcoin Core P2P listener address inferred
// from bitcoin.conf. It is intentionally independent of RPC authentication:
// if the user configured a Core data directory, BOD can try the local node as
// a preferred header peer even when RPC is unavailable or still being set up.
func coreP2PAddr(s appSettings) (string, bool) {
	if s.CoreDisabled {
		return "", false
	}
	dataDir := strings.TrimSpace(s.BitcoinDataDir)
	if dataDir == "" {
		return "", false
	}
	if _, err := os.Stat(dataDir); err != nil {
		return "", false
	}
	conf := parseBitcoinConf(dataDir)
	port := conf.P2PPort
	if port <= 0 {
		port = 8333
	}
	return fmt.Sprintf("127.0.0.1:%d", port), true
}
