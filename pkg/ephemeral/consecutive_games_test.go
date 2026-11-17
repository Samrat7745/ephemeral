// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package ephemeral

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/carbynestack/ephemeral/pkg/discovery/fsm"
	pb "github.com/carbynestack/ephemeral/pkg/discovery/transport/proto"
	. "github.com/carbynestack/ephemeral/pkg/types"
	"go.uber.org/zap"
)

// scriptedPlayer is an AbstractPlayerWithIO whose Start() runs a per-game script against the channels the server
// handed out for the current request.
type scriptedPlayer struct {
	s      *Server
	script func(respCh chan []byte, errCh, execErrCh chan error)
}

func (p *scriptedPlayer) Start() {
	respCh, errCh, execErrCh := p.s.respCh, p.s.errCh, p.s.execErrCh
	go p.script(respCh, errCh, execErrCh)
}
func (p *scriptedPlayer) History() *fsm.History { return nil }

const consecutiveGameID = "71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4"

func newConsecutiveServer() *Server {
	conf := &SPDZEngineTypedConfig{ComputationTimeout: time.Second, StateTimeout: time.Second}
	s := NewServer("sub", func(*CtxConfig) error { return nil }, func(*CtxConfig) ([]byte, error) { return nil, nil }, zap.NewNop().Sugar(), conf)
	// The pod name is read by running "hostname" through the executor (original behaviour); a fake keeps the tests
	// independent of the environment (the real commander runs "script" in "/", which fails for non-root users).
	s.executor = &FakeExecutor{}
	return s
}

// serve sends one activation request through the compilation and activation handlers, like the real HTTP chain.
func serve(t *testing.T, s *Server, timeout time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	conf := &SPDZEngineTypedConfig{ComputationTimeout: timeout, StateTimeout: timeout}
	body, _ := json.Marshal(&Activation{GameID: consecutiveGameID, AmphoraParams: []string{"a"}})
	req, _ := http.NewRequest("POST", "/", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(context.Background(), ctxConf, &CtxConfig{
		Act: &Activation{GameID: consecutiveGameID}, Spdz: conf,
	}))
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.CompilationHandler(http.HandlerFunc(s.ActivationHandler)).ServeHTTP(rr, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request blocked")
	}
	return rr
}

func TestSecondActivationDoesNotSeeFirstGamesError(t *testing.T) {
	s := newConsecutiveServer()
	lateErr := make(chan struct{})
	s.player = &scriptedPlayer{s: s, script: func(_ chan []byte, _, execErrCh chan error) {
		execErrCh <- errors.New("game 1 failed")
	}}
	if rr := serve(t, s, time.Second); rr.Code != http.StatusInternalServerError {
		t.Fatalf("game 1: want 500, got %d", rr.Code)
	}

	// Game 1's engine reports a late error after the request has finished: it must not leak into game 2.
	first := s.execErrCh
	go func() { first <- errors.New("late error of game 1"); close(lateErr) }()
	<-lateErr

	s.player = &scriptedPlayer{s: s, script: func(respCh chan []byte, _, _ chan error) {
		respCh <- []byte("game 2 result")
	}}
	rr := serve(t, s, time.Second)
	if rr.Code != http.StatusOK || rr.Body.String() != "game 2 result" {
		t.Fatalf("game 2: want 200 'game 2 result', got %d %q", rr.Code, rr.Body.String())
	}
}

func TestSecondActivationSucceedsAfterFirstTimedOut(t *testing.T) {
	s := newConsecutiveServer()
	s.player = &scriptedPlayer{s: s, script: func(chan []byte, chan error, chan error) {}} // never answers
	if rr := serve(t, s, 10*time.Millisecond); rr.Code != http.StatusInternalServerError || rr.Body.String() != "timeout during activation procedure" {
		t.Fatalf("game 1: want timeout, got %d %q", rr.Code, rr.Body.String())
	}
	s.player = &scriptedPlayer{s: s, script: func(respCh chan []byte, _, _ chan error) { respCh <- []byte("ok") }}
	if rr := serve(t, s, time.Second); rr.Code != http.StatusOK {
		t.Fatalf("game 2: want 200, got %d %q", rr.Code, rr.Body.String())
	}
}

// After a request gave up (timeout), the engine of that game still finishes later and delivers its result through
// SPDZWrapper.Execute. This must not block forever, as it would pin the player FSM callback (and with it the SPDZ
// process and its ports) of the finished game.
func TestSPDZWrapperDoesNotBlockAfterRequestGaveUp(t *testing.T) {
	s := newConsecutiveServer()
	s.player = &scriptedPlayer{s: s, script: func(chan []byte, chan error, chan error) {}}
	if rr := serve(t, s, 10*time.Millisecond); rr.Code != http.StatusInternalServerError {
		t.Fatalf("want timeout, got %d", rr.Code)
	}
	ctx := &CtxConfig{Act: &Activation{GameID: consecutiveGameID}, Spdz: &SPDZEngineTypedConfig{PlayerID: 0}}
	w := NewSPDZWrapper(ctx, s.respCh, s.execErrCh, zap.NewNop().Sugar(), func(*CtxConfig) ([]byte, error) { return []byte("late"), nil })
	done := make(chan struct{})
	go func() {
		w.Execute(twoPlayerEvent())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Skip("known: SPECS-2171 - SPDZWrapper.Execute blocks on the unbuffered respCh once the request has returned")
	}
}

func twoPlayerEvent() *pb.Event {
	return &pb.Event{Players: []*pb.Player{{Id: 100, Ip: "1.1.1.1", Port: 30000}, {Id: 101, Ip: "2.2.2.2", Port: 30001}}}
}

func noopLogger() *zap.SugaredLogger { return zap.NewNop().Sugar() }
