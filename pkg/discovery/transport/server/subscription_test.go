// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/carbynestack/ephemeral/pkg/discovery/transport/proto"
	. "github.com/carbynestack/ephemeral/pkg/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Every game opens a new stream of the ephemeral pod's discovery client. When the stream is closed (game finished),
// the server must stop forwarding broadcast events to it. Otherwise every finished game leaves a subscription behind
// that keeps writing to a dead stream (SPECS-2171).
func TestClosedStreamIsUnsubscribedFromBroadcast(t *testing.T) {
	core, recorded := observer.New(zapcore.ErrorLevel)
	in, out := make(chan *pb.Event, 10), make(chan *pb.Event)
	port := "30111"
	tr := NewTransportServer(&TransportConfig{In: in, Out: out, ErrCh: make(chan error), Port: port, Logger: zap.New(core).Sugar()})
	go tr.Run(func() {})
	defer tr.Stop()
	time.Sleep(200 * time.Millisecond)

	conn, err := grpc.Dial("localhost:"+port, grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = metadata.AppendToOutgoingContext(ctx, ConnID, "game-1", EventScope, EventScopeSelf)
	stream, err := pb.NewDiscoveryClient(conn).Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// make sure the stream is registered at the server
	if err := stream.Send(&pb.Event{GameID: "game-1"}); err != nil {
		t.Fatal(err)
	}
	<-in
	cancel()
	time.Sleep(300 * time.Millisecond)

	out <- &pb.Event{GameID: "game-1", Name: "late"}
	time.Sleep(300 * time.Millisecond)
	for _, e := range recorded.All() {
		if strings.HasPrefix(e.Message, "Error broadcasting the event") {
			t.Fatalf("event was still forwarded to the closed stream: %s", e.Message)
		}
	}
}
