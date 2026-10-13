package main

import (
	"context"
	"path/filepath"
)

const utxoSharedMaintenanceBlocks = int64(4096)

type utxoSharedMaintenance struct {
	Height     int64  `json:"height"`
	Commitment string `json:"commitment"`
	Checksum   string `json:"checksum"`
}

func utxoSharedMaintenanceHash(m utxoSharedMaintenance) string {
	m.Checksum = ""
	return indexDigest(m)
}

func utxoSharedMaintenanceDue(s *utxoStore) bool {
	cp := s.Head()
	if cp == nil {
		return false
	}
	var marker utxoSharedMaintenance
	if err := utxoReadJSON(context.Background(), filepath.Join(s.dir, "fact-maintenance.json"), 16<<10, &marker); err != nil || marker.Height < 0 || !validHash(marker.Commitment) || marker.Checksum != utxoSharedMaintenanceHash(marker) {
		return true
	}
	return cp.Height < marker.Height || cp.Height-marker.Height >= utxoSharedMaintenanceBlocks
}

func recordUTXOSharedMaintenance(s *utxoStore) error {
	cp := s.Head()
	if cp == nil {
		return nil
	}
	marker := utxoSharedMaintenance{Height: cp.Height, Commitment: cp.Commitment}
	marker.Checksum = utxoSharedMaintenanceHash(marker)
	if err := atomicWriteJSON(filepath.Join(s.dir, "fact-maintenance.json"), marker); err != nil {
		return err
	}
	return syncDirectory(s.dir)
}
