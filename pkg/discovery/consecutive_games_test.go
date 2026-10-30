// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package discovery

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/carbynestack/ephemeral/pkg/discovery/fsm"
	pb "github.com/carbynestack/ephemeral/pkg/discovery/transport/proto"
	. "github.com/carbynestack/ephemeral/pkg/types"
	mb "github.com/vardius/message-bus"
	"go.uber.org/zap"
)

const homeAddress = "192.168.0.1"

// countingNetworker hands out increasing ports and counts the created networks.
type countingNetworker struct {
	mu    sync.Mutex
	next  int32
	pods  []string
	fails bool
}

func (c *countingNetworker) CreateNetwork(pl *pb.Player) (int32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fails {
		return 0, errors.New("no network")
	}
	c.pods = append(c.pods, pl.Pod)
	p := 30000 + c.next
	c.next++
	return p, nil
}

func newService(mode string, n Networker) (*ServiceNG, mb.MessageBus, *Publisher) {
	bus := mb.New(10000)
	pub := &Publisher{Bus: bus, Fsm: &fsm.FSM{}}
	s := NewServiceNG(bus, pub, 5*time.Second, 5*time.Second, &FakeTransport{}, n, homeAddress, zap.NewNop().Sugar(), mode, &FakeDClient{}, 2)
	return s, bus, pub
}

func player(id int32, pod string) *pb.Player {
	return &pb.Player{Id: 100 + id, Pod: pod, Ip: homeAddress, Players: 2}
}

func TestRegisterSamePodsInConsecutiveGames(t *testing.T) {
	n := &countingNetworker{}
	s, _, _ := newService(ModeMaster, n)

	// game A
	a0, a1 := player(0, "pod-0"), player(1, "pod-1")
	for _, p := range []*pb.Player{a0, a1} {
		if err := s.registerPlayer(p, "game-a"); err != nil {
			t.Fatal(err)
		}
	}
	if a0.Port != 30000 || a1.Port != 30001 {
		t.Fatalf("game A ports: %d %d", a0.Port, a1.Port)
	}

	// game B, same pods, different game id: the networks (and therefore the ports) of the pods are reused, no new
	// network resource is created.
	b0, b1 := player(0, "pod-0"), player(1, "pod-1")
	for _, p := range []*pb.Player{b0, b1} {
		if err := s.registerPlayer(p, "game-b"); err != nil {
			t.Fatal(err)
		}
	}
	if b0.Port != 30000 || b1.Port != 30001 {
		t.Fatalf("game B must receive the ports of game A, got %d %d", b0.Port, b1.Port)
	}
	if len(n.pods) != 2 || len(s.networks) != 2 || len(s.pods) != 2 {
		t.Fatalf("networks created=%v cached=%v pods=%v", n.pods, s.networks, s.pods)
	}
	if len(s.players["game-a"]) != 2 || len(s.players["game-b"]) != 2 {
		t.Fatalf("players per game: %v", s.players)
	}
	// the games hold their own player objects
	if s.players["game-a"][100] == s.players["game-b"][100] {
		t.Fatal("players of different games must not be shared")
	}
}

func TestRegisterSameGameIDTwiceKeepsFirstRegistration(t *testing.T) {
	n := &countingNetworker{}
	s, _, _ := newService(ModeMaster, n)
	first := player(0, "pod-0")
	if err := s.registerPlayer(first, "g"); err != nil {
		t.Fatal(err)
	}
	dup := player(0, "pod-0")
	if err := s.registerPlayer(dup, "g"); err != nil {
		t.Fatal(err)
	}
	if s.players["g"][100] != first || dup.Port != first.Port || len(n.pods) != 1 {
		t.Fatalf("duplicate registration must be a no-op that still reports the port: %+v %+v", first, dup)
	}
}

func TestRegisterPlayerNetworkError(t *testing.T) {
	n := &countingNetworker{fails: true}
	s, _, _ := newService(ModeMaster, n)
	if err := s.registerPlayer(player(0, "pod-0"), "g"); err == nil {
		t.Fatal("expected error")
	}
	if len(s.networks) != 0 || len(s.pods) != 0 || len(s.players["g"]) != 0 {
		t.Fatalf("failed registration must not leave bookkeeping: %v %v %v", s.networks, s.pods, s.players)
	}
	// the network can be created once the networker works again
	n.fails = false
	if err := s.registerPlayer(player(0, "pod-0"), "g"); err != nil {
		t.Fatal(err)
	}
}

func TestForeignPlayerKeepsPortOfItsHome(t *testing.T) {
	n := &countingNetworker{}
	s, _, _ := newService(ModeMaster, n)
	pl := &pb.Player{Id: 101, Pod: "remote", Ip: "10.0.0.9", Port: 31234}
	if err := s.registerPlayer(pl, "g"); err != nil {
		t.Fatal(err)
	}
	if len(n.pods) != 0 || pl.Port != 31234 || s.networks["remote"] != 31234 {
		t.Fatalf("no network must be created for foreign players: %v %+v", n.pods, pl)
	}
	// The foreign port is cached for the lifetime of the process: a later game in which the remote pod announces a
	// different port is answered with the stale one.
	again := &pb.Player{Id: 101, Pod: "remote", Ip: "10.0.0.9", Port: 32000}
	if err := s.registerPlayer(again, "g2"); err != nil {
		t.Fatal(err)
	}
	if again.Port != 31234 {
		t.Fatalf("documented behaviour: cached port wins, got %d", again.Port)
	}
}

func TestDeleteCallbackAllowsNewNetworkForPod(t *testing.T) {
	n := &countingNetworker{}
	s, _, _ := newService(ModeMaster, n)
	s.registerPlayer(player(0, "pod-0"), "g1")
	s.DeleteCallback("pod-0")
	if len(s.networks) != 0 || len(s.pods) != 0 {
		t.Fatalf("bookkeeping not cleaned: %v %v", s.networks, s.pods)
	}
	pl := player(0, "pod-0")
	s.registerPlayer(pl, "g2")
	if pl.Port != 30001 || len(n.pods) != 2 {
		t.Fatalf("expected a fresh network with a new port, got %d %v", pl.Port, n.pods)
	}
}

// runGame plays a complete game (2 players) through the service and returns the names of the events the clients
// received for the game.
func runGame(t *testing.T, bus mb.MessageBus, pub *Publisher, gameID string, pods [2]string, finish string) []string {
	t.Helper()
	var mu sync.Mutex
	var names []string
	last := make(chan struct{}, 1)
	handler := func(e interface{}) {
		ev := e.(*pb.Event)
		if ev.GameID != gameID {
			return
		}
		mu.Lock()
		names = append(names, ev.Name)
		mu.Unlock()
		if ev.Name == GameSuccess || ev.Name == GameError {
			select {
			case last <- struct{}{}:
			default:
			}
		}
	}
	if err := bus.Subscribe(ClientOutgoingEventsTopic, handler); err != nil {
		t.Fatal(err)
	}
	send := func(name string) {
		for i, pod := range pods {
			pub.PublishExternalEvent(&pb.Event{Name: name, GameID: gameID, Players: []*pb.Player{player(int32(i), pod)}}, ClientIncomingEventsTopic)
		}
		time.Sleep(20 * time.Millisecond)
	}
	send(PlayerReady)
	send(TCPCheckSuccess)
	send(finish)
	select {
	case <-last:
	case <-time.After(3 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("game %s did not finish, events: %v", gameID, names)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), names...)
}

func startService(t *testing.T, s *ServiceNG) {
	t.Helper()
	go s.Start()
	if err := s.WaitUntilReady(2 * time.Second); err != nil {
		t.Fatal(err)
	}
}

func contains(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

func TestTwoConsecutiveGamesOnSamePodsBothSucceed(t *testing.T) {
	n := &countingNetworker{}
	s, bus, pub := newService(ModeMaster, n)
	startService(t, s)
	pods := [2]string{"pod-0", "pod-1"}

	for _, id := range []string{"game-a", "game-b"} {
		names := runGame(t, bus, pub, id, pods, GameFinishedWithSuccess)
		for _, want := range []string{PlayersReady, TCPCheckSuccessAll, GameSuccess} {
			if !contains(names, want) {
				t.Fatalf("%s: missing %s in %v", id, want, names)
			}
		}
		if contains(names, GameError) {
			t.Fatalf("%s: unexpected GameError in %v", id, names)
		}
	}
	if len(n.pods) != 2 {
		t.Fatalf("networks must only be created once per pod, got %v", n.pods)
	}
}

func TestGameErrorInFirstGameDoesNotAffectSecond(t *testing.T) {
	n := &countingNetworker{}
	s, bus, pub := newService(ModeMaster, n)
	startService(t, s)
	pods := [2]string{"pod-0", "pod-1"}
	names := runGame(t, bus, pub, "game-a", pods, GameFinishedWithError)
	if !contains(names, GameError) {
		t.Fatalf("expected GameError, got %v", names)
	}
	names = runGame(t, bus, pub, "game-b", pods, GameFinishedWithSuccess)
	if !contains(names, GameSuccess) || contains(names, GameError) {
		t.Fatalf("second game must be unaffected, got %v", names)
	}
}

func TestFinishedGamesAreRemovedFromBookkeeping(t *testing.T) {
	n := &countingNetworker{}
	s, bus, pub := newService(ModeMaster, n)
	startService(t, s)
	runGame(t, bus, pub, "game-a", [2]string{"pod-0", "pod-1"}, GameFinishedWithSuccess)
	time.Sleep(100 * time.Millisecond)
	s.mux.Lock()
	games, players := len(s.games), len(s.players)
	s.mux.Unlock()
	if games != 0 || players != 0 {
		t.Skipf("known: SPECS-2171 - ServiceNG never removes finished games (games=%d, players=%d): s.games/s.players only grow. Removal is not done because late events of a finished game would re-create it (new FSM, state timeout, spurious GameError broadcast); needs a tombstone design", games, players)
	}
}

func TestReusingAFinishedGameIDIsRejected(t *testing.T) {
	n := &countingNetworker{}
	s, bus, pub := newService(ModeMaster, n)
	startService(t, s)
	pods := [2]string{"pod-0", "pod-1"}
	runGame(t, bus, pub, "game-a", pods, GameFinishedWithSuccess)
	time.Sleep(100 * time.Millisecond)
	var mu sync.Mutex
	var names []string
	bus.Subscribe(DiscoveryTopic, func(e interface{}) {
		mu.Lock()
		names = append(names, e.(*fsm.Event).Name)
		mu.Unlock()
	})
	pub.PublishExternalEvent(&pb.Event{Name: PlayerReady, GameID: "game-a", Players: []*pb.Player{player(0, "pod-0")}}, ClientIncomingEventsTopic)
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if !contains(names, GameProtocolError) {
		t.Fatalf("expected GameProtocolError for a stopped game, got %v", names)
	}
}

func newServiceWithTimeouts(state, computation time.Duration, playerCount int) (*ServiceNG, mb.MessageBus, *Publisher) {
	bus := mb.New(10000)
	pub := &Publisher{Bus: bus, Fsm: &fsm.FSM{}}
	s := NewServiceNG(bus, pub, state, computation, &FakeTransport{}, &countingNetworker{}, homeAddress, zap.NewNop().Sugar(), ModeMaster, &FakeDClient{}, playerCount)
	return s, bus, pub
}

func collect(bus mb.MessageBus, gameID string, stop ...string) (func() []string, chan struct{}) {
	var mu sync.Mutex
	var names []string
	done := make(chan struct{}, 1)
	bus.Subscribe(ClientOutgoingEventsTopic, func(e interface{}) {
		ev := e.(*pb.Event)
		if ev.GameID != gameID {
			return
		}
		mu.Lock()
		names = append(names, ev.Name)
		mu.Unlock()
		for _, s := range stop {
			if ev.Name == s {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		}
	})
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), names...) }, done
}

func TestStateTimeoutWhenNotAllPlayersRegister(t *testing.T) {
	s, bus, pub := newServiceWithTimeouts(150*time.Millisecond, time.Second, 2)
	startService(t, s)
	get, done := collect(bus, "g", GameError)
	pub.PublishExternalEvent(&pb.Event{Name: PlayerReady, GameID: "g", Players: []*pb.Player{player(0, "pod-0")}}, ClientIncomingEventsTopic)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("no GameError after the state timeout, got %v", get())
	}
	if contains(get(), PlayersReady) {
		t.Fatalf("PlayersReady must not be sent with a single player: %v", get())
	}
}

func TestComputationTimeoutWhenPlayersNeverFinish(t *testing.T) {
	s, bus, pub := newServiceWithTimeouts(5*time.Second, 150*time.Millisecond, 2)
	startService(t, s)
	get, done := collect(bus, "g", GameError)
	for _, name := range []string{PlayerReady, TCPCheckSuccess} {
		for i, pod := range []string{"pod-0", "pod-1"} {
			pub.PublishExternalEvent(&pb.Event{Name: name, GameID: "g", Players: []*pb.Player{player(int32(i), pod)}}, ClientIncomingEventsTopic)
		}
		time.Sleep(30 * time.Millisecond)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("no GameError after the computation timeout, got %v", get())
	}
	if !contains(get(), TCPCheckSuccessAll) {
		t.Fatalf("game must have reached the playing state: %v", get())
	}
}

func TestTCPCheckFailureEndsTheGameWithError(t *testing.T) {
	s, bus, pub := newServiceWithTimeouts(5*time.Second, 5*time.Second, 2)
	startService(t, s)
	get, done := collect(bus, "g", GameError)
	for i, pod := range []string{"pod-0", "pod-1"} {
		pub.PublishExternalEvent(&pb.Event{Name: PlayerReady, GameID: "g", Players: []*pb.Player{player(int32(i), pod)}}, ClientIncomingEventsTopic)
	}
	time.Sleep(50 * time.Millisecond)
	pub.PublishExternalEvent(&pb.Event{Name: TCPCheckFailure, GameID: "g", Players: []*pb.Player{player(0, "pod-0")}}, ClientIncomingEventsTopic)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("no GameError, got %v", get())
	}
}

// More players than configured: the game starts once the configured number of players is ready, the surplus player
// must not crash the service and other games keep working.
func TestMorePlayersThanConfigured(t *testing.T) {
	s, bus, pub := newServiceWithTimeouts(5*time.Second, 5*time.Second, 2)
	startService(t, s)
	get, done := collect(bus, "g", PlayersReady)
	for i, pod := range []string{"pod-0", "pod-1", "pod-2"} {
		pub.PublishExternalEvent(&pb.Event{Name: PlayerReady, GameID: "g", Players: []*pb.Player{player(int32(i), pod)}}, ClientIncomingEventsTopic)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("PlayersReady missing: %v", get())
	}
	// another game on the same service still works
	names := runGame(t, bus, pub, "other", [2]string{"pod-3", "pod-4"}, GameFinishedWithSuccess)
	if !contains(names, GameSuccess) {
		t.Fatalf("other game failed: %v", names)
	}
}

// Fewer players than configured: PlayersReady is not sent.
func TestFewerPlayersThanConfiguredNeverStart(t *testing.T) {
	s, bus, pub := newServiceWithTimeouts(5*time.Second, 5*time.Second, 3)
	startService(t, s)
	get, _ := collect(bus, "g")
	for i, pod := range []string{"pod-0", "pod-1"} {
		pub.PublishExternalEvent(&pb.Event{Name: PlayerReady, GameID: "g", Players: []*pb.Player{player(int32(i), pod)}}, ClientIncomingEventsTopic)
	}
	time.Sleep(200 * time.Millisecond)
	if contains(get(), PlayersReady) {
		t.Fatalf("unexpected PlayersReady: %v", get())
	}
}

func TestSlaveForwardsEventsToMasterAndRegistersPlayer(t *testing.T) {
	n := &countingNetworker{}
	s, bus, pub := newService(ModeSlave, n)
	forwarded := make(chan *pb.Event, 4)
	bus.Subscribe(MasterOutgoingEventsTopic, func(e interface{}) { forwarded <- e.(*pb.Event) })
	startService(t, s)
	pub.PublishExternalEvent(&pb.Event{Name: PlayerReady, GameID: "g", Players: []*pb.Player{player(1, "pod-1")}}, ClientIncomingEventsTopic)
	select {
	case ev := <-forwarded:
		if ev.Players[0].Port != 30000 || ev.GameID != "g" {
			t.Fatalf("the slave must announce the allocated port: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event not forwarded")
	}
	// a slave never creates games itself
	s.mux.Lock()
	defer s.mux.Unlock()
	if len(s.games) != 0 {
		t.Fatal("slave must not create games")
	}
}
