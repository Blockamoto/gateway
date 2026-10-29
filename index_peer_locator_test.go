package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func Test073TransactionPublicationsFrozenPrivateTailAndRevocation(t *testing.T) {
	a, _, hashes := timelineFixture(t, 5)
	a.settings.ServeGatewayData = true
	s, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	first := browseBlock073(t, 2, hashes[2])
	if err = s.appendBlock(first, 2, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	id := first.Transactions[1].TxID
	if _, found, err := a.publishedInscriptionTransactionContext(context.Background(), id, ""); err != nil || found {
		t.Fatal("private transaction receipts were shared", found, err)
	}
	manifest, err := a.storeIndexPublication("tx-locator", true, s.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	page, err := a.readIndexPeerPage(indexPeerFirstRequest(manifest, 1))
	if err != nil || len(page.Batches) != 1 || page.Batches[0].TransactionLocator.Selection != "all" {
		t.Fatal("transaction publication page failed shared receipt validation", page, err)
	}
	if _, err = a.setIndexPublication("tx-locator", true); err == nil || releaseFeatureAvailable("gateway-peerhood") {
		t.Fatal("production publication gate opened", err)
	}
	private := browseBlock073(t, 3, hashes[3])
	private.PreviousBlockHash = hashes[2]
	if err = s.appendBlock(private, 2, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if a.storedIndexPublicationMatches("tx-locator", s.checkpoint) {
		t.Fatal("publication advanced with the private head")
	}
	loc, found, err := a.publishedInscriptionTransactionContext(context.Background(), id, "")
	if err != nil || !found || loc.Height != 2 || loc.TxIndex != 1 || loc.BlockHash != hashes[2] {
		t.Fatal("frozen transaction locator stopped working after private growth", loc, found, err)
	}
	if _, found, err = a.publishedInscriptionTransactionContext(context.Background(), private.Transactions[1].TxID, ""); err != nil || found {
		t.Fatal("unpublished private tail escaped", found, err)
	}
	manifests := a.readPublishedIndexManifests()
	if len(manifests) != 1 || manifests[0].Checkpoint.Commitment != manifest.Checkpoint.Commitment {
		t.Fatal("frozen manifest changed", manifests)
	}
	if _, err = a.storeIndexPublication("tx-locator", false); err != nil {
		t.Fatal(err)
	}
	if _, found, err = a.publishedInscriptionTransactionContext(context.Background(), id, ""); err != nil || found {
		t.Fatal("revoked locator remained shareable", found, err)
	}
	if _, err = os.Stat(filepath.Join(s.dir, "head.json")); err != nil {
		t.Fatal("revocation removed private records", err)
	}
}

func Test073SparseRelatedPublicationHasExactActualPositionAndPrivateSource(t *testing.T) {
	a, _, hashes := timelineFixture(t, 4)
	a.settings.ServeGatewayData = true
	height := firstMainnetInscriptionHeight
	block := browseBlock073(t, height, hashes[2])
	installRunProtocolHeader(t, a, &block)
	source, err := openIndexStore(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	source.head.Mode = "lean"
	if err = source.appendBlock(block, height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	b, err := source.readBatch(source.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	related, err := openIndexStore(a.dataDir, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	related.head.Mode = "lean"
	if err = related.appendInscriptionLocators(b, &block, height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	m, err := a.storeIndexPublication(inscriptionLocatorIndex, true, related.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	page, err := a.readIndexPeerPage(indexPeerFirstRequest(m, 1))
	if err != nil || len(page.Batches) != 1 {
		t.Fatal(page, err)
	}
	payload := page.Batches[0].TransactionLocator
	if payload == nil || !reflect.DeepEqual(payload.Block.Transactions, []transactionLocatorEntry{{TxIndex: 1, TxID: block.Transactions[1].TxID}}) || payload.SourceCommitment != source.checkpoint.Commitment {
		t.Fatal("sparse array position was confused with actual Bitcoin position", payload)
	}
	loc, found, err := a.publishedInscriptionTransactionContext(context.Background(), block.Transactions[1].TxID, "")
	if err != nil || !found || loc.TxIndex != 1 {
		t.Fatal("related locator failed source verification", loc, found, err)
	}
	if got := a.readPublishedIndexManifests(); len(got) != 1 || got[0].Definition != inscriptionLocatorIndex {
		t.Fatal("publishing related locators implicitly exposed their inscription source", got)
	}
	if _, err = a.readIndexPeerPage(indexPeerFirstRequest(indexManifestForCheckpoint(source.definition, *source.checkpoint), 1)); err == nil {
		t.Fatal("private inscription source was served")
	}
	if _, found, err = a.publishedInscriptionTransactionContext(context.Background(), block.Transactions[0].TxID, ""); err != nil || found {
		t.Fatal("sparse related locator exposed a non-inscription transaction", found, err)
	}
}

func Test073BlocksPublicationAndNoRemoteLocatorRepair(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.ServeGatewayData = true, true
	block, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.appendBlock(block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	m, err := a.storeIndexPublication("blocks", true, s.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.readIndexPeerPage(indexPeerFirstRequest(m, 1)); err != nil {
		t.Fatal("block receipt page not valid", err)
	}
	id := block.Transactions[0].TxID
	if _, found, err := a.publishedInscriptionTransactionContext(context.Background(), id, ""); err != nil || found {
		t.Fatal("Raw publication exposed an unrequested transaction locator", found, err)
	}
	locators, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	locators.head.Mode = "lean"
	if err = locators.appendBlock(block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.publishedInscriptionTransactionContext(context.Background(), id, ""); err != nil || found {
		t.Fatal("private explicit Locator was shared without publication", found, err)
	}
	if _, err = a.storeIndexPublication("tx-locator", true, locators.checkpoint.Commitment); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.publishedInscriptionTransactionContext(context.Background(), id, ""); err != nil || !found {
		t.Fatal("published explicit Locator unavailable", found, err)
	}
	path, err := graphShardPath(locators.dir, locators.locatorKind(), id)
	if err != nil {
		t.Fatal(err)
	}
	receipt := path + ".receipt.json"
	if err = os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.publishedInscriptionTransactionContext(context.Background(), id, ""); err == nil || found || !strings.Contains(err.Error(), "repair") {
		t.Fatal("remote lookup repaired or trusted unreceipted private acceleration", found, err)
	}
	if _, err = os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatal("remote lookup wrote a locator receipt", err)
	}
}

func Test073PeerLocatorSchemaCannotBeMixedIntoInscriptionPages(t *testing.T) {
	b := indexBatch{TransactionLocator: &transactionLocatorPayload{Block: transactionLocatorBlock{Height: 0}}}
	for _, id := range []string{"inscriptions", "bitmap"} {
		if err := validateIndexPeerLocatorRecords(id, b); err == nil {
			t.Fatal("peer transaction payload passed a different index schema", id)
		}
	}
}
