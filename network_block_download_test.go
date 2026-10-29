package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func coreDownloadFixture(i int) (blockTarget, []byte) {
	raw := make([]byte, 81)
	binary.LittleEndian.PutUint32(raw, uint32(i+1))
	h := hash256(raw[:80])
	return blockTarget{Height: int64(i), HashRaw: h, HashDisplay: reverseHex(h[:]), ExpectedHeader: raw[:80]}, raw
}
func coreDownloadWait(t *testing.T, f func() bool) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("download condition not reached")
}

func TestCoreBlockDownloadSixteenHashCorrelatedRepliesAndHeaders(t *testing.T) {
	a := independentTestApp(t)
	local, remote := net.Pipe()
	defer remote.Close()
	s := sessionRepairManaged(t, a, local, "127.0.0.1:60401")
	a.network.mu.Lock()
	s.peer.services = nodeNetworkService | nodeWitnessService
	a.network.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	replies := map[[32]byte][]byte{}
	targets := []blockTarget{}
	for i := 0; i < coreBlocksPerPeer; i++ {
		target, raw := coreDownloadFixture(i)
		targets = append(targets, target)
		replies[target.HashRaw] = raw
	}
	wire := make(chan error, 1)
	go func() {
		seen := [][32]byte{}
		header := false
		for len(seen) < coreBlocksPerPeer || !header {
			m, err := readMessage(remote)
			if err != nil {
				wire <- err
				return
			}
			switch m.command {
			case "getheaders":
				header = true
				if err = writeMessage(remote, "headers", []byte{0}); err != nil {
					wire <- err
					return
				}
			case "getdata":
				var h [32]byte
				copy(h[:], m.payload[len(m.payload)-32:])
				seen = append(seen, h)
			default:
				wire <- fmt.Errorf("unexpected %s", m.command)
				return
			}
		}
		for i := len(seen) - 1; i >= 0; i-- {
			if err := writeMessage(remote, "block", replies[seen[i]]); err != nil {
				wire <- err
				return
			}
		}
		wire <- nil
	}()
	results := make(chan error, coreBlocksPerPeer+1)
	for _, target := range targets {
		go func(target blockTarget) {
			raw, _, err := a.network.fetchTargetBlockContext(ctx, target)
			if err == nil && !bytes.Equal(raw, replies[target.HashRaw]) {
				err = fmt.Errorf("wrong correlated body")
			}
			results <- err
		}(target)
	}
	go func() {
		var payload bytes.Buffer
		payload.Write([]byte{0x7f, 0x11, 0x01, 0x00})
		payload.WriteByte(0)
		payload.Write(make([]byte, 32))
		_, err := s.request(ctx, "getheaders", payload.Bytes(), func(m message) bool { return m.command == "headers" })
		results <- err
	}()
	for i := 0; i < coreBlocksPerPeer+1; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-wire; err != nil {
		t.Fatal(err)
	}
}

func TestCoreBlockDownloadCoalescingIndependentCancellationAndLateReply(t *testing.T) {
	a := independentTestApp(t)
	local, remote := net.Pipe()
	defer remote.Close()
	s := sessionRepairManaged(t, a, local, "127.0.0.1:60402")
	s.peer.services = nodeNetworkService
	target, raw := coreDownloadFixture(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window := a.network.newBlockWindow(ctx)
	defer window.Close()
	window.Update([]blockTarget{target})
	gotRequest := make(chan struct{})
	send := make(chan struct{})
	var requests atomic.Int32
	go func() {
		m, err := readMessage(remote)
		if err != nil || m.command != "getdata" {
			return
		}
		requests.Add(1)
		close(gotRequest)
		<-send
		_ = writeMessage(remote, "block", raw)
	}()
	<-gotRequest
	first, cancelFirst := context.WithCancel(ctx)
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() { _, _, err := a.network.fetchTargetBlockContext(first, target); firstResult <- err }()
	go func() {
		b, _, err := a.network.fetchTargetBlockContext(ctx, target)
		if err == nil && !bytes.Equal(b, raw) {
			err = fmt.Errorf("wrong body")
		}
		secondResult <- err
	}()
	d := a.network.blockDownloader()
	coreDownloadWait(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.jobs[target.HashRaw].waiters == 2 })
	cancelFirst()
	if err := <-firstResult; err != context.Canceled {
		t.Fatal(err)
	}
	close(send)
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("duplicate physical request", requests.Load())
	}
	d.mu.Lock()
	job := d.jobs[target.HashRaw]
	if job == nil || !job.finished || len(job.owners) != 1 || job.reservation != int64(len(raw)) {
		t.Fatal("remaining bulk owner lost accounting")
	}
	d.mu.Unlock()
	window.Close()
	coreDownloadWait(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.reserved == 0 && len(d.jobs) == 0 })
}

func TestCoreBlockDownloadWindowBoundAndCancel(t *testing.T) {
	a := independentTestApp(t)
	local, remote := net.Pipe()
	defer remote.Close()
	s := sessionRepairManaged(t, a, local, "127.0.0.1:60403")
	s.peer.services = nodeNetworkService
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	window := a.network.newBlockWindow(ctx)
	targets := []blockTarget{}
	for i := 0; i < coreBlockWindow+20; i++ {
		target, _ := coreDownloadFixture(i)
		targets = append(targets, target)
	}
	var received atomic.Int32
	go func() {
		for {
			m, err := readMessage(remote)
			if err != nil {
				return
			}
			if m.command == "getdata" {
				received.Add(1)
			}
		}
	}()
	window.Update(targets)
	d := a.network.blockDownloader()
	coreDownloadWait(t, func() bool { return received.Load() == coreBlocksPerPeer })
	d.mu.Lock()
	if len(d.jobs) != coreBlockWindow || d.reserved > gatewayBlockDownloadBudget {
		t.Fatalf("unbounded window: %d %d", len(d.jobs), d.reserved)
	}
	d.mu.Unlock()
	window.Close()
	coreDownloadWait(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.reserved == 0 && len(d.jobs) == 0 })
}

func TestCoreBlockDownloadAvailabilityAndWitnessRequirements(t *testing.T) {
	n := &bitcoinNetwork{sessions: map[string]*bitcoinSession{}}
	s := &bitcoinSession{n: n, peer: &peerConn{addr: "limited", services: nodeNetworkLimitedService | nodeWitnessService}, view: networkPeerView{State: "connected", TargetHeight: 900000}}
	n.sessions[s.peer.addr] = s
	if !blockTargetEligible(s, blockTarget{Height: 899900}) || blockTargetEligible(s, blockTarget{Height: 899700}) || blockTargetEligible(s, blockTarget{Height: -1}) {
		t.Fatal("limited peer promise misused")
	}
	s.peer.services = nodeNetworkService
	if blockTargetEligible(s, blockTarget{Height: 900000}) || blockTargetEligible(s, blockTarget{Height: -1}) || !blockTargetEligible(s, blockTarget{Height: 170}) {
		t.Fatal("witness capability ignored")
	}
}

func TestCoreBlockMessageBoundBeforeAllocation(t *testing.T) {
	var frame bytes.Buffer
	frame.Write(mainnetMagic[:])
	command := make([]byte, 12)
	copy(command, "block")
	frame.Write(command)
	_ = binary.Write(&frame, binary.LittleEndian, uint32(coreBlockMessageBytes+1))
	frame.Write(make([]byte, 4))
	if _, err := readMessage(&frame); err == nil {
		t.Fatal("oversized block accepted")
	}
}

func TestCoreBlockTimeoutAccountsForOtherDownloadingPeers(t *testing.T) {
	if coreBlockRequestTimeout(0) != 10*time.Minute || coreBlockRequestTimeout(1) != 15*time.Minute || coreBlockRequestTimeout(3) != 25*time.Minute {
		t.Fatal("Core ordinary timeout policy changed")
	}
}

func TestCoreBlockNoPeerIsWaitingInsteadOfIndefiniteSourceWork(t *testing.T) {
	a := independentTestApp(t)
	target, _ := coreDownloadFixture(0)
	started := time.Now()
	if _, _, err := a.network.fetchTargetBlockContext(context.Background(), target); err == nil || time.Since(started) > time.Second {
		t.Fatal("missing peer did not return Waiting", err)
	}
}

func TestCoreBlockSelectedHeaderEvidenceBoundsKnownPeers(t *testing.T) {
	n := &bitcoinNetwork{}
	s := &bitcoinSession{n: n, peer: &peerConn{services: nodeNetworkService | nodeWitnessService}, view: networkPeerView{State: "connected", TargetHeight: 1_000_000}}
	if !blockTargetEligible(s, blockTarget{Height: 900000}) {
		t.Fatal("unknown archival peer was treated as disproven")
	}
	n.noteSelectedPeerHeaders(s, 800000, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if blockTargetEligible(s, blockTarget{Height: 800001}) || !blockTargetEligible(s, blockTarget{Height: 800000}) {
		t.Fatal("verified peer header availability ignored")
	}
}

func TestCoreBlockWindowStallReassignsFirstRequiredBlock(t *testing.T) {
	a := independentTestApp(t)
	badLocal, badRemote := net.Pipe()
	goodLocal, goodRemote := net.Pipe()
	defer badRemote.Close()
	defer goodRemote.Close()
	bad := sessionRepairManaged(t, a, badLocal, "127.0.0.1:60410")
	good := sessionRepairManaged(t, a, goodLocal, "127.0.0.1:60411")
	a.network.mu.Lock()
	bad.peer.services = nodeNetworkService
	good.peer.services = nodeNetworkService
	a.network.mu.Unlock()
	targets := []blockTarget{}
	bodies := map[[32]byte][]byte{}
	for i := 0; i < 8; i++ {
		target, raw := coreDownloadFixture(i)
		targets = append(targets, target)
		bodies[target.HashRaw] = raw
	}
	var held atomic.Int32
	serve := func(remote net.Conn, hold bool) {
		for {
			m, err := readMessage(remote)
			if err != nil {
				return
			}
			if m.command != "getdata" {
				continue
			}
			var hash [32]byte
			copy(hash[:], m.payload[len(m.payload)-32:])
			if hold && hash == targets[0].HashRaw {
				held.Add(1)
				continue
			}
			if writeMessage(remote, "block", bodies[hash]) != nil {
				return
			}
		}
	}
	go serve(badRemote, true)
	go serve(goodRemote, false)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	window := a.network.newBlockWindow(ctx)
	defer window.Close()
	window.Update(targets)
	started := time.Now()
	raw, provider, err := a.network.fetchTargetBlockContext(ctx, targets[0])
	if err != nil || provider != good.peer.addr || !bytes.Equal(raw, bodies[targets[0].HashRaw]) {
		t.Fatal(provider, err)
	}
	if held.Load() != 1 || time.Since(started) >= 7*time.Second {
		t.Fatal("first body waited for ordinary block timeout")
	}
	d := a.network.blockDownloader()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stalls != 1 || d.stallTimeout < 2*time.Second {
		t.Fatal("window stall not recorded", d.stalls)
	}
}

func TestCoreBlockWindowFailedEntryRecoversWithNewPeer(t *testing.T) {
	a := independentTestApp(t)
	local, remote := net.Pipe()
	defer remote.Close()
	bad := sessionRepairManaged(t, a, local, "127.0.0.1:60420")
	bad.peer.services = nodeNetworkService
	go func() {
		for {
			m, err := readMessage(remote)
			if err != nil {
				return
			}
			if writeMessage(remote, "notfound", m.payload) != nil {
				return
			}
		}
	}()
	target, raw := coreDownloadFixture(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window := a.network.newBlockWindow(ctx)
	defer window.Close()
	window.Update([]blockTarget{target})
	if _, _, err := a.network.fetchTargetBlockContext(ctx, target); err == nil {
		t.Fatal("missing block claimed available")
	}
	goodLocal, goodRemote := net.Pipe()
	defer goodRemote.Close()
	good := sessionRepairManaged(t, a, goodLocal, "127.0.0.1:60421")
	good.peer.services = nodeNetworkService
	go func() {
		if _, err := readMessage(goodRemote); err == nil {
			_ = writeMessage(goodRemote, "block", raw)
		}
	}()
	got, provider, err := a.network.fetchTargetBlockContext(ctx, target)
	if err != nil || provider != good.peer.addr || !bytes.Equal(got, raw) {
		t.Fatal("failed window entry poisoned retry", provider, err)
	}
}

func TestCoreBlockDemandPreemptsFullSpeculativeBuffer(t *testing.T) {
	a := independentTestApp(t)
	local, remote := net.Pipe()
	defer remote.Close()
	s := sessionRepairManaged(t, a, local, "127.0.0.1:60430")
	s.peer.services = nodeNetworkService
	d := a.network.blockDownloader()
	d.mu.Lock()
	d.budget = 2 * coreBlockMessageBytes
	d.mu.Unlock()
	targets := []blockTarget{}
	bodies := map[[32]byte][]byte{}
	for i := 0; i < 4; i++ {
		target, small := coreDownloadFixture(i)
		raw := make([]byte, coreBlockMessageBytes)
		copy(raw, small)
		targets = append(targets, target)
		bodies[target.HashRaw] = raw
	}
	needed, small := coreDownloadFixture(99)
	next := make(chan [32]byte, 1)
	go func() {
		for i := 0; i < 2; i++ {
			m, err := readMessage(remote)
			if err != nil {
				return
			}
			var h [32]byte
			copy(h[:], m.payload[len(m.payload)-32:])
			if writeMessage(remote, "block", bodies[h]) != nil {
				return
			}
		}
		m, err := readMessage(remote)
		if err != nil {
			return
		}
		var h [32]byte
		copy(h[:], m.payload[len(m.payload)-32:])
		next <- h
		_ = writeMessage(remote, "block", small)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window := a.network.newBlockWindow(ctx)
	defer window.Close()
	window.Update(targets)
	coreDownloadWait(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		ready := 0
		for _, j := range d.jobs {
			if j.finished && j.err == nil {
				ready++
			}
		}
		return ready == 2
	})
	got, _, err := a.network.fetchTargetBlockContext(ctx, needed)
	if err != nil || !bytes.Equal(got, small) {
		t.Fatal(err)
	}
	if h := <-next; h != needed.HashRaw {
		t.Fatal("interactive block queued behind speculative future bodies")
	}
}

func TestCoreBlockMalformedBodyFeedbackEvictsAndRetries(t *testing.T) {
	a := independentTestApp(t)
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	goodRaw := testGenesisBlockPayload(t)
	badRaw := append([]byte(nil), goodRaw...)
	badRaw[len(badRaw)-1] ^= 1
	badLocal, badRemote := net.Pipe()
	goodLocal, goodRemote := net.Pipe()
	defer badRemote.Close()
	defer goodRemote.Close()
	bad := sessionRepairManaged(t, a, badLocal, "127.0.0.1:60440")
	good := sessionRepairManaged(t, a, goodLocal, "127.0.0.1:60441")
	a.network.mu.Lock()
	bad.peer.services = nodeNetworkService
	good.peer.services = nodeNetworkService
	a.network.mu.Unlock()
	go func() {
		if _, e := readMessage(badRemote); e == nil {
			_ = writeMessage(badRemote, "block", badRaw)
		}
	}()
	go func() {
		if _, e := readMessage(goodRemote); e == nil {
			_ = writeMessage(goodRemote, "block", goodRaw)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window := a.network.newBlockWindow(ctx)
	defer window.Close()
	window.Update([]blockTarget{target})
	if _, err := a.fetchBlockWithPolicyContext(ctx, target, "ephemeral"); err == nil {
		t.Fatal("matching header with corrupted transaction accepted")
	}
	select {
	case <-bad.done:
	default:
		t.Fatal("bad full-body supplier was retained")
	}
	view, err := a.fetchBlockWithPolicyContext(ctx, target, "ephemeral")
	if err != nil || !integrityVerified(view) || view.SourcePeer != good.peer.addr {
		t.Fatal("failed cached bytes poisoned retry", view.SourcePeer, err)
	}
}

func TestCoreRunMalformedPeerBodyWaitsAndRetriesWithoutCheckpoint(t *testing.T) {
	a := independentTestApp(t)
	goodRaw := testGenesisBlockPayload(t)
	badRaw := append([]byte(nil), goodRaw...)
	badRaw[len(badRaw)-1] ^= 1
	badLocal, badRemote := net.Pipe()
	goodLocal, goodRemote := net.Pipe()
	defer badRemote.Close()
	defer goodRemote.Close()
	bad := sessionRepairManaged(t, a, badLocal, "127.0.0.1:60460")
	good := sessionRepairManaged(t, a, goodLocal, "127.0.0.1:60461")
	a.network.mu.Lock()
	bad.peer.services, good.peer.services = nodeNetworkService, nodeNetworkService
	a.network.mu.Unlock()
	go func() {
		if _, err := readMessage(badRemote); err == nil {
			_ = writeMessage(badRemote, "block", badRaw)
		}
	}()
	release := make(chan struct{}, 1)
	defer close(release)
	go func() {
		if _, err := readMessage(goodRemote); err == nil {
			<-release
			_ = writeMessage(goodRemote, "block", goodRaw)
		}
	}()
	zero := int64(0)
	req := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "blocks", Armed: true}}}
	if _, err := a.startIndexRun(context.Background(), req, ""); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		return d.Run != nil && d.Run.State == "waiting" && len(d.Run.Consumers) == 1 && d.Run.Consumers[0].State == "waiting"
	})
	head, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || head.checkpoint != nil {
		t.Fatal("invalid peer body advanced the Raw checkpoint", head, err)
	}
	select {
	case <-bad.done:
	default:
		t.Fatal("invalid body supplier remained available")
	}
	release <- struct{}{}
	done := waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 || done.Run.Consumers[0].Height != 0 || done.Run.Consumers[0].State != "complete" {
		t.Fatal("same admitted run did not recover with the next provider", done.Run)
	}
	head, err = indexStoreHead(a.dataDir, "blocks")
	goodHash := hash256(goodRaw[:80])
	if err != nil || head.checkpoint == nil || head.checkpoint.BlockHash != reverseHex(goodHash[:]) {
		t.Fatal("recovered checkpoint does not bind the verified body", head, err)
	}
}

func TestCoreBlockCanceledLateReplyCannotCompleteDifferentHash(t *testing.T) {
	a := independentTestApp(t)
	local, remote := net.Pipe()
	defer remote.Close()
	s := sessionRepairManaged(t, a, local, "127.0.0.1:60450")
	s.peer.services = nodeNetworkService
	old, oldRaw := coreDownloadFixture(1)
	next, nextRaw := coreDownloadFixture(2)
	firstRead := make(chan struct{})
	later := make(chan struct{})
	wire := make(chan error, 1)
	go func() {
		if _, err := readMessage(remote); err != nil {
			wire <- err
			return
		}
		close(firstRead)
		<-later
		if _, err := readMessage(remote); err != nil {
			wire <- err
			return
		}
		if err := writeMessage(remote, "block", oldRaw); err != nil {
			wire <- err
			return
		}
		wire <- writeMessage(remote, "block", nextRaw)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, cancelFirst := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, _, err := a.network.fetchTargetBlockContext(first, old); result <- err }()
	<-firstRead
	cancelFirst()
	if err := <-result; err != context.Canceled {
		t.Fatal(err)
	}
	close(later)
	raw, _, err := a.network.fetchTargetBlockContext(ctx, next)
	if err != nil || !bytes.Equal(raw, nextRaw) {
		t.Fatal("late canceled reply reached another request", err)
	}
	if err := <-wire; err != nil {
		t.Fatal(err)
	}
}
