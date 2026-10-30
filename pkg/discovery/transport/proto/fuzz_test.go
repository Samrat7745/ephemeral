// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package protobuf

import (
	"testing"

	proto "github.com/golang/protobuf/proto"
)

// FuzzEventUnmarshal: events are received from the network. Decoding must never panic and a decoded event must
// survive a marshal/unmarshal round trip unchanged.
func FuzzEventUnmarshal(f *testing.F) {
	valid, _ := proto.Marshal(&Event{Name: "PlayerReady", GameID: "g", Players: []*Player{{Id: 100, Ip: "1.2.3.4", Port: 30000, Pod: "pod", Players: 2}}})
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Add([]byte{0x12, 0x00, 0x12, 0x02, 0x08})
	f.Fuzz(func(t *testing.T, data []byte) {
		var ev Event
		if err := proto.Unmarshal(data, &ev); err != nil {
			return
		}
		out, err := proto.Marshal(&ev)
		if err != nil {
			t.Fatalf("marshal of a decoded event failed: %v", err)
		}
		var again Event
		if err := proto.Unmarshal(out, &again); err != nil {
			t.Fatalf("round trip failed: %v", err)
		}
		if !proto.Equal(&ev, &again) {
			t.Fatalf("round trip changed the event: %v vs %v", &ev, &again)
		}
	})
}
