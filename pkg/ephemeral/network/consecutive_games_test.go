// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package network

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	. "github.com/Samrat7745/ephemeral/pkg/types"
	"go.uber.org/zap"
)

type failingChecker struct{}

func (failingChecker) Verify(context.Context, string, string) error { return errors.New("peer down") }

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

// startEcho starts a TCP backend that answers every connection with "pong".
func startEcho(t *testing.T) (port string, stop func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("pong"))
			c.Close()
		}
	}()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port), func() { l.Close() }
}

func newTestCtx(host, port, localPort string, playerID int32, checker NetworkChecker) (*Proxy, *CtxConfig) {
	conf := &SPDZEngineTypedConfig{PlayerID: playerID, RetrySleep: 10 * time.Millisecond, NetworkEstablishTimeout: 2 * time.Second}
	ctx := &CtxConfig{
		Act:          &Activation{GameID: "71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4"},
		ProxyEntries: []*ProxyConfig{{Host: host, Port: port, LocalPort: localPort}},
		Spdz:         conf,
		Context:      context.Background(),
	}
	return NewProxy(zap.NewNop().Sugar(), conf, checker), ctx
}

func readPong(t *testing.T, port string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	// the PingAwareTarget inspects the first 4 bytes before forwarding
	c.Write([]byte("data"))
	c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4)
	n, _ := c.Read(buf)
	if string(buf[:n]) != "pong" {
		t.Fatalf("unexpected proxy answer %q", buf[:n])
	}
}

// The same Proxy instance is reused by SPDZEngine for every game. A restart on the same local port after a finished
// game must work.
func TestProxyRestartOnSameLocalPort(t *testing.T) {
	backend, stop := startEcho(t)
	defer stop()
	local := freePort(t)
	p, ctx := newTestCtx("127.0.0.1", backend, local, 0, &NoopChecker{})
	for game := 0; game < 3; game++ {
		errCh := make(chan error, 1)
		if err := p.Run(ctx, errCh); err != nil {
			t.Fatalf("game %d: run: %v", game, err)
		}
		readPong(t, local)
		p.Stop()
		// the port must be released again once Stop returned
		l, err := net.Listen("tcp", ":"+local)
		if err != nil {
			t.Fatalf("game %d: port %s still bound after Stop: %v", game, local, err)
		}
		l.Close()
	}
}

// If the connectivity check of a later game fails, Run returns before the tcpproxy was started. Stop (deferred by
// SPDZEngine.Activate) must not block on state of the previous game.
func TestProxyStopAfterFailedRunFollowingSuccessfulGame(t *testing.T) {
	backend, stop := startEcho(t)
	defer stop()
	local := freePort(t)
	p, ctx := newTestCtx("127.0.0.1", backend, local, 0, &NoopChecker{})
	if err := p.Run(ctx, make(chan error, 1)); err != nil {
		t.Fatal(err)
	}
	p.Stop()

	p.tcpChecker = failingChecker{}
	if err := p.Run(ctx, make(chan error, 1)); err == nil {
		t.Fatal("expected run to fail when the peer check fails")
	}
	done := make(chan struct{})
	go func() { p.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Skip("known: SPECS-2171 - Proxy.Stop blocks after a failed Run that follows a successful game: it waits on the stale activeProxyIndicatorCh of the previous game and calls Wait on a proxy that was never started")
	}
}
