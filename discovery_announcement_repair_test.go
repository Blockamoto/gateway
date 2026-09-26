package main

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func announcementRepairNetwork() (*bitcoinNetwork, *atomic.Int64) {
	clock := new(atomic.Int64)
	clock.Store(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC).UnixNano())
	n := &bitcoinNetwork{sessions: map[string]*bitcoinSession{}}
	n.clock = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	return n, clock
}

func announcementRepairSession(t *testing.T, n *bitcoinNetwork, address string, conn net.Conn) *bitcoinSession {
	t.Helper()
	t.Cleanup(func() { conn.Close() })
	s := &bitcoinSession{
		n: n, peer: &peerConn{conn: conn, addr: address, wantsAddrV2: true},
		done: make(chan struct{}), slot: make(chan struct{}, 1),
		view: networkPeerView{Address: address, Role: "bitcoin_transport"},
	}
	n.mu.Lock()
	n.sessions[address] = s
	n.mu.Unlock()
	return s
}

func announcementRepairRow() advertisedPeer {
	return advertisedPeer{
		Addr: "8.8.4.4:48333", Timestamp: 1790424000,
		Services: gatewayExperimentalServiceBit | nodeNetworkService | nodeWitnessService,
	}
}

func announcementRepairReceive(remote net.Conn, want advertisedPeer) <-chan error {
	result := make(chan error, 1)
	go func() {
		m, err := readMessage(remote)
		if err == nil {
			var rows []advertisedPeer
			rows, err = parseAddressMessage(m)
			if err == nil && (len(rows) != 1 || rows[0].Addr != want.Addr || rows[0].Services != want.Services) {
				err = fmt.Errorf("unexpected announcement: %+v", rows)
			}
		}
		result <- err
	}()
	return result
}

func announcementRepairDelivered(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("announcement did not reach the peer")
	}
}

func TestAnnouncementRepairFailedWriteAllowsReconnect(t *testing.T) {
	n, clock := announcementRepairNetwork()
	local, remote := net.Pipe()
	remote.Close()
	s := announcementRepairSession(t, n, "127.0.0.1:60116", local)
	row := announcementRepairRow()
	if n.sendAnnouncement054(s, row) {
		t.Fatal("failed write counted as success")
	}
	if !n.lastAnnounce.IsZero() || !s.lastAnnouncement.IsZero() {
		t.Fatal("failed write recorded a successful announcement")
	}
	if record, ok := n.announcementHistory[s.peer.addr]; ok {
		t.Fatalf("failed first write retained a reservation or cooldown: %+v", record)
	}

	local, remote = net.Pipe()
	t.Cleanup(func() { remote.Close() })
	s = announcementRepairSession(t, n, s.peer.addr, local)
	received := announcementRepairReceive(remote, row)
	if !n.sendAnnouncement054(s, row) {
		t.Fatal("healthy replacement was suppressed after the failed write")
	}
	announcementRepairDelivered(t, received)
	if !n.lastAnnounce.Equal(n.now()) || !s.lastAnnouncement.Equal(n.now()) {
		t.Fatal("successful write did not commit announcement times")
	}
	clock.Add(int64(29 * time.Minute))
	if n.sendAnnouncement054(s, row) {
		t.Fatal("successful write lost its 30-minute recipient cooldown")
	}
	clock.Add(int64(time.Minute))
	received = announcementRepairReceive(remote, row)
	if !n.sendAnnouncement054(s, row) {
		t.Fatal("successful-write cooldown did not expire")
	}
	announcementRepairDelivered(t, received)
}

func TestAnnouncementRepairFailedChangePreservesPriorSuccess(t *testing.T) {
	n, clock := announcementRepairNetwork()
	local, remote := net.Pipe()
	t.Cleanup(func() { remote.Close() })
	s := announcementRepairSession(t, n, "127.0.0.1:60117", local)
	row := announcementRepairRow()
	received := announcementRepairReceive(remote, row)
	if !n.sendAnnouncement054(s, row) {
		t.Fatal("initial announcement failed")
	}
	announcementRepairDelivered(t, received)
	prior := n.announcementHistory[s.peer.addr]

	changed := row
	changed.Addr = "1.1.1.1:49333"
	clock.Add(int64(4 * time.Minute))
	if n.sendAnnouncement054(s, changed) {
		t.Fatal("changed endpoint bypassed the five-minute limit")
	}
	clock.Add(int64(time.Minute))
	broken, closed := net.Pipe()
	closed.Close()
	s = announcementRepairSession(t, n, s.peer.addr, broken)
	if n.sendAnnouncement054(s, changed) {
		t.Fatal("failed changed announcement counted as success")
	}
	if got := n.announcementHistory[s.peer.addr]; got != prior {
		t.Fatalf("failure changed the preceding success: got %+v, want %+v", got, prior)
	}

	healthy, peer := net.Pipe()
	t.Cleanup(func() { peer.Close() })
	s = announcementRepairSession(t, n, s.peer.addr, healthy)
	if n.sendAnnouncement054(s, row) {
		t.Fatal("failure erased the preceding same-key cooldown")
	}
	received = announcementRepairReceive(peer, changed)
	if !n.sendAnnouncement054(s, changed) {
		t.Fatal("failed change consumed the changed-key retry opportunity")
	}
	announcementRepairDelivered(t, received)
	if got := n.announcementHistory[s.peer.addr]; got.Key == prior.Key || !got.At.Equal(n.now()) || got.InFlight != nil {
		t.Fatalf("successful changed announcement not committed: %+v", got)
	}
}

type announcementRepairObservedConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *announcementRepairObservedConn) Write(b []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(b)
}

func TestAnnouncementRepairInFlightSurvivesExpiryAndReconnect(t *testing.T) {
	n, clock := announcementRepairNetwork()
	local, remote := net.Pipe()
	t.Cleanup(func() { remote.Close() })
	observed := &announcementRepairObservedConn{Conn: local, started: make(chan struct{})}
	s := announcementRepairSession(t, n, "127.0.0.1:60118", observed)
	row := announcementRepairRow()
	finished := make(chan bool, 1)
	go func() { finished <- n.sendAnnouncement054(s, row) }()
	select {
	case <-observed.started:
	case <-time.After(time.Second):
		t.Fatal("first write did not start")
	}

	// Advance the test clock while net.Pipe holds the write. Cleanup of old
	// success history must not remove the active recipient reservation.
	clock.Add(int64(31 * time.Minute))
	var competitors sync.WaitGroup
	results := make(chan bool, 16)
	for i := 0; i < cap(results); i++ {
		competitors.Add(1)
		go func() {
			defer competitors.Done()
			results <- n.sendAnnouncement054(s, row)
		}()
	}
	for i := 0; i < cap(results); i++ {
		select {
		case sent := <-results:
			if sent {
				t.Fatal("concurrent announcement bypassed the reservation")
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent announcement attempted a second blocked write")
		}
	}
	competitors.Wait()
	n.mu.Lock()
	record := n.announcementHistory[s.peer.addr]
	uncommitted := n.lastAnnounce.IsZero() && s.lastAnnouncement.IsZero()
	n.mu.Unlock()
	if record.InFlight != s || !record.At.IsZero() || !uncommitted {
		t.Fatal("in-flight write was expired or recorded as successful")
	}

	replacementConn, replacementPeer := net.Pipe()
	t.Cleanup(func() { replacementPeer.Close() })
	replacement := announcementRepairSession(t, n, s.peer.addr, replacementConn)
	changed := row
	changed.Addr = "1.1.1.1:49333"
	if n.sendAnnouncement054(replacement, changed) {
		t.Fatal("replacement session with a changed endpoint bypassed an active recipient reservation")
	}
	received := announcementRepairReceive(remote, row)
	select {
	case ok := <-finished:
		if !ok {
			t.Fatal("first write failed")
		}
	case <-time.After(time.Second):
		t.Fatal("first write did not complete")
	}
	announcementRepairDelivered(t, received)
	if got := n.announcementHistory[s.peer.addr]; got.InFlight != nil || !got.At.Equal(n.now()) {
		t.Fatalf("cooldown did not start at successful completion: %+v", got)
	}
	if n.sendAnnouncement054(replacement, row) {
		t.Fatal("replacement session bypassed the completed recipient cooldown")
	}
}
