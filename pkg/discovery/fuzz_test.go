// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package discovery

import "testing"

// FuzzNewPortsState: the port range parser must never panic, reject everything outside of 1..65535 and never hand out
// ports outside of an accepted range.
func FuzzNewPortsState(f *testing.F) {
	for _, s := range []string{"30000:30100", "1:65535", "5:3", "0:10", "1:70000", "a:b", ":", "1:", ":1", "1:2:3", "", "-1:5", "99999999999999999999:1", " 1:2", "1: 2", "+1:2"} {
		f.Add(s, int32(0), int32(0))
	}
	f.Fuzz(func(t *testing.T, rng string, u1, u2 int32) {
		ps, err := NewPortsState(rng, nil)
		if err != nil {
			return
		}
		if ps.start < 1 || ps.end > 65535 || ps.start > ps.end {
			t.Fatalf("range %q accepted as %d:%d", rng, ps.start, ps.end)
		}
		ps.Sync([]int32{u1, u2})
		seen := map[int32]bool{}
		for i := 0; i < 20; i++ {
			p, err := ps.GetFreePort()
			if err != nil {
				return
			}
			if p < ps.start || p > ps.end {
				t.Fatalf("port %d outside of %q", p, rng)
			}
			if seen[p] {
				t.Fatalf("port %d handed out twice for %q", p, rng)
			}
			seen[p] = true
		}
	})
}
