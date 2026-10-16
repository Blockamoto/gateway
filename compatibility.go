package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	appVersion              = "0.7.7"
	bodWireVersion          = 1
	minBODWireVersion       = 1
	overlayProtocolVersion  = bodWireVersion
	minOverlayProtocol      = minBODWireVersion
	storageSchemaVersion    = 2
	headerSchemaVersion     = 1
	indexSchemaVersion      = 3
	cacheMetadataSchema     = 2
	minCompatibleAppVersion = "0.3.9"
)

type compatibilityView struct {
	AppVersion           string `json:"app_version"`
	MinimumCompatibleApp string `json:"minimum_compatible_app"`

	// New explicit dimensions.
	WireProtocol           int    `json:"wire_protocol"`
	MinimumWireProtocol    int    `json:"minimum_wire_protocol"`
	StorageSchema          int    `json:"storage_schema"`
	MinimumReadableStorage int    `json:"minimum_readable_storage_schema"`
	HeaderSchema           int    `json:"header_schema"`
	HeaderResyncRequired   bool   `json:"header_resync_required"`
	RawBlockFormat         string `json:"raw_block_format"`
	RawBlockRedownload     bool   `json:"raw_block_redownload_required"`
	IndexSchema            int    `json:"index_schema"`
	IndexRebuild           bool   `json:"index_rebuild_required"`
	BlockBodySchema        int    `json:"block_body_schema"`
	TXIOSchema             int    `json:"txio_schema"`
	UTXOSchema             int    `json:"utxo_schema"`
	CacheMetadataSchema    int    `json:"cache_metadata_schema"`
	CacheMetadataRebuild   bool   `json:"cache_metadata_rebuild_required"`
	CoreMountSchema        int    `json:"core_mount_schema"`
	GraphSchema            int    `json:"graph_schema"`
	GraphRebuild           bool   `json:"public_graph_rebuild_required"`
	SatlineStorageSchema   int    `json:"satline_storage_schema"`
	LocalAPI               int    `json:"local_api"`
	SkinAPI                int    `json:"skin_api"`
	ModuleHostAPI          int    `json:"module_host_api"`
	GatewayProtocol        int    `json:"gateway_protocol"`
	MinimumGatewayProtocol int    `json:"minimum_gateway_protocol"`

	// Retained metadata aliases used by existing development tooling.
	OverlayProtocol        int    `json:"overlay_protocol"`
	MinimumOverlayProtocol int    `json:"minimum_overlay_protocol"`
	StorageIndexRebuild    bool   `json:"storage_index_rebuild_on_upgrade"`
	BlockVerifier          int    `json:"block_verifier"`
	DerivedRecheck         bool   `json:"derived_recheck_required"`
	OrdStorage             int    `json:"ord_storage_schema"`
	ArchiveStorage         int    `json:"archive_storage_schema"`
	Note                   string `json:"note"`
}

func currentCompatibility() compatibilityView {
	return compatibilityView{
		AppVersion:             appVersion,
		MinimumCompatibleApp:   minCompatibleAppVersion,
		WireProtocol:           bodWireVersion,
		MinimumWireProtocol:    minBODWireVersion,
		StorageSchema:          storageSchemaVersion,
		MinimumReadableStorage: storageSchemaVersion,
		HeaderSchema:           headerSchemaVersion,
		HeaderResyncRequired:   false,
		RawBlockFormat:         "bitcoin-serialized-block",
		RawBlockRedownload:     false,
		IndexSchema:            indexSchemaVersion,
		IndexRebuild:           false,
		BlockBodySchema:        3,
		TXIOSchema:             3,
		UTXOSchema:             utxoCheckpointSchema,
		CacheMetadataSchema:    cacheMetadataSchema,
		CacheMetadataRebuild:   false,
		CoreMountSchema:        coreMountSchemaVersion,
		GraphSchema:            graphSchemaVersion,
		GraphRebuild:           true,
		SatlineStorageSchema:   satlineStorageSchema,
		LocalAPI:               localAPIVersion,
		SkinAPI:                skinAPIVersion,
		ModuleHostAPI:          moduleHostAPIVersion,
		GatewayProtocol:        gatewayWireVersion,
		MinimumGatewayProtocol: gatewayWireVersion,
		OverlayProtocol:        bodWireVersion,
		MinimumOverlayProtocol: minBODWireVersion,
		StorageIndexRebuild:    false,
		BlockVerifier:          blockVerifierVersion, DerivedRecheck: true, OrdStorage: 1, ArchiveStorage: 1,
		Note: "New Raw Blocks records retain serialized Bitcoin bytes in segmented body schema 3 with block-level metadata; general transaction lookup remains an explicit Locator choice. Legacy decoded and schema 2 Raw records remain readable without automatic conversion. Schema 3 bodies require this development build; an older executable may not read new records. UTXO and Sats retain their independent genesis-first state and recovery rules; positional TXIO and Addresses remain independently selected. Full consensus validation and Gateway peerhood remain deferred. This local development build is not a published release or updater promotion.",
	}
}

func writeCompatibilityMeta(dataDir string) error {
	b, err := json.MarshalIndent(currentCompatibility(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "compatibility.json"), b, 0644)
}
