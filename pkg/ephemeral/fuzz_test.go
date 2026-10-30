// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package ephemeral

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/carbynestack/ephemeral/pkg/types"
)

// FuzzGetUserFromAuthHeader: arbitrary headers and claim paths must never panic; a successful result has to be
// derivable from the claims.
func FuzzGetUserFromAuthHeader(f *testing.F) {
	enc := base64.RawURLEncoding.EncodeToString
	f.Add("Bearer h."+enc([]byte(`{"sub":"alice"}`))+".s", "sub")
	f.Add("Bearer h."+enc([]byte(`{"a":{"b":"c"}}`))+".s", "a.b")
	f.Add("Bearer h."+base64.URLEncoding.EncodeToString([]byte(`{"sub":"x"}`))+".s", "sub")
	f.Add("Bearer a.b", "sub")
	f.Add("", "")
	f.Add("Bearer h."+enc([]byte(`null`))+".s", "")
	f.Add("Bearer h."+enc([]byte(`{"sub":null}`))+".s", "sub..x")
	f.Fuzz(func(t *testing.T, header, field string) {
		user, err := GetUserFromAuthHeader(header, field)
		if err != nil && user != "" {
			t.Fatalf("user %q returned together with error %v", user, err)
		}
		if err == nil {
			parts := strings.Split(strings.TrimPrefix(header, "Bearer "), ".")
			if len(parts) != 3 {
				t.Fatalf("success for a token with %d parts", len(parts))
			}
		}
	})
}

// FuzzActivationBody: whatever the client sends, the request filter answers with 200 (only for valid activations) or a
// 4xx status and never panics.
func FuzzActivationBody(f *testing.F) {
	const id = "71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4"
	f.Add(`{"gameID":"` + id + `","amphoraParams":["a"],"output":{"type":"PLAINTEXT"}}`)
	f.Add(`{"gameID":"` + id + `","secretParams":["eA=="]}`)
	f.Add(`{"gameID":"` + id + `","secretParams":["%%"],"amphoraParams":["a"]}`)
	f.Add(`{"gameID":5}`)
	f.Add(`null`)
	f.Add(``)
	f.Add(`{"amphoraParams":[null]}`)
	f.Add("{\"gameID\":\"\xff\"}")
	s := NewServer("sub", nil, nil, noopLogger(), &SPDZEngineTypedConfig{})
	auth := "Bearer h." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u"}`)) + ".s"
	f.Fuzz(func(t *testing.T, body string) {
		var act Activation
		_ = json.Unmarshal([]byte(body), &act) // must not panic either
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			conf := r.Context().Value(ctxConf).(*CtxConfig)
			if !isValidUUID(conf.Act.GameID) {
				t.Fatalf("invalid game id %q passed the filter", conf.Act.GameID)
			}
			if (len(conf.Act.AmphoraParams) > 0) == (len(conf.Act.SecretParams) > 0) {
				t.Fatalf("exactly one parameter kind must pass the filter: %+v", conf.Act)
			}
			for _, p := range conf.Act.SecretParams {
				if _, err := base64.StdEncoding.DecodeString(p); err != nil {
					t.Fatalf("invalid base64 passed the filter: %q", p)
				}
			}
		})
		req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Authorization", auth)
		rr := httptest.NewRecorder()
		s.RequestFilter(next).ServeHTTP(rr, req)
		if called {
			if rr.Code != 200 {
				t.Fatalf("code %d", rr.Code)
			}
			return
		}
		if rr.Code < 400 || rr.Code > 499 {
			t.Fatalf("invalid input must be answered with 4xx, got %d for %q", rr.Code, body)
		}
	})
}
