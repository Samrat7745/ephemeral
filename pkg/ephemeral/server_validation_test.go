// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package ephemeral

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/Samrat7745/ephemeral/pkg/types"
	"go.uber.org/zap"
)

func jwt(payload string) string {
	return "Bearer h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

func filterServer() *Server {
	return NewServer("sub", nil, nil, zap.NewNop().Sugar(), &SPDZEngineTypedConfig{})
}

func TestRequestFilterValidationMatrix(t *testing.T) {
	const id = "71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4"
	b64 := base64.StdEncoding.EncodeToString([]byte("x"))
	cases := []struct {
		name   string
		auth   string
		body   string
		status int
		msg    string
	}{
		{"valid amphora", jwt(`{"sub":"u"}`), `{"gameID":"` + id + `","amphoraParams":["a"]}`, 200, ""},
		{"valid secret params", jwt(`{"sub":"u"}`), `{"gameID":"` + id + `","secretParams":["` + b64 + `"]}`, 200, ""},
		{"no auth header", "", `{"gameID":"` + id + `","amphoraParams":["a"]}`, 401, "unauthorized request"},
		{"malformed auth header", "Bearer a.b", `{}`, 401, "unauthorized request"},
		{"auth without sub", jwt(`{"x":"u"}`), `{}`, 401, "unauthorized request"},
		{"invalid json", jwt(`{"sub":"u"}`), `{`, 400, "error decoding the request body"},
		{"json array", jwt(`{"sub":"u"}`), `[]`, 400, "error decoding the request body"},
		{"wrong field type", jwt(`{"sub":"u"}`), `{"gameID":5}`, 400, "error decoding the request body"},
		{"empty body", jwt(`{"sub":"u"}`), ``, 400, "error decoding the request body"},
		{"empty object", jwt(`{"sub":"u"}`), `{}`, 400, "GameID  is not a valid UUID"},
		{"invalid uuid", jwt(`{"sub":"u"}`), `{"gameID":"not-a-uuid","amphoraParams":["a"]}`, 400, "GameID not-a-uuid is not a valid UUID"},
		{"both params", jwt(`{"sub":"u"}`), `{"gameID":"` + id + `","amphoraParams":["a"],"secretParams":["` + b64 + `"]}`, 400, "not both of them"},
		{"no params", jwt(`{"sub":"u"}`), `{"gameID":"` + id + `"}`, 400, "none of them given"},
		{"empty param lists", jwt(`{"sub":"u"}`), `{"gameID":"` + id + `","amphoraParams":[],"secretParams":[]}`, 400, "none of them given"},
		{"invalid base64", jwt(`{"sub":"u"}`), `{"gameID":"` + id + `","secretParams":["%%%"]}`, 400, "error decoding secret parameters"},
		{"one of two base64 invalid", jwt(`{"sub":"u"}`), `{"gameID":"` + id + `","secretParams":["` + b64 + `","a"]}`, 400, "error decoding secret parameters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
			req, _ := http.NewRequest("POST", "/", strings.NewReader(tc.body))
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rr := httptest.NewRecorder()
			filterServer().RequestFilter(next).ServeHTTP(rr, req)
			if tc.status == 200 {
				if !called || rr.Code != 200 {
					t.Fatalf("expected next handler to be called, code %d body %q", rr.Code, rr.Body.String())
				}
				return
			}
			if called || rr.Code != tc.status || !strings.Contains(rr.Body.String(), tc.msg) {
				t.Fatalf("called=%v code=%d body=%q, want %d %q", called, rr.Code, rr.Body.String(), tc.status, tc.msg)
			}
		})
	}
}

func TestRequestFilterNilBody(t *testing.T) {
	req, _ := http.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", jwt(`{"sub":"u"}`))
	rr := httptest.NewRecorder()
	filterServer().RequestFilter(http.NotFoundHandler()).ServeHTTP(rr, req)
	if rr.Code != 400 || rr.Body.String() != "request body is nil" {
		t.Fatalf("%d %q", rr.Code, rr.Body.String())
	}
}

func TestRequestFilterPassesAuthorizedUserAndKeepsBody(t *testing.T) {
	body := `{"gameID":"71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4","amphoraParams":["a"],"code":"x"}`
	req, _ := http.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Authorization", jwt(`{"sub":"alice"}`))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conf := r.Context().Value(ctxConf).(*CtxConfig)
		if conf.AuthorizedUser != "alice" || conf.Act.Code != "x" {
			t.Errorf("%+v", conf)
		}
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		if buf.String() != body {
			t.Errorf("body not preserved: %q", buf.String())
		}
	})
	filterServer().RequestFilter(next).ServeHTTP(httptest.NewRecorder(), req)
}

func TestMethodFilterMatrix(t *testing.T) {
	cases := []struct {
		method, ctype string
		status        int
	}{
		{"POST", "application/json", 200},
		{"POST", "application/json; charset=utf-8", 200},
		{"POST", "text/plain", 415},
		{"POST", "", 415},
		{"POST", ";;", 415},
		{"GET", "application/json", 405},
		{"PUT", "application/json", 405},
		{"DELETE", "", 405},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, "/", nil)
		if tc.ctype != "" {
			req.Header.Set("Content-Type", tc.ctype)
		}
		rr := httptest.NewRecorder()
		filterServer().MethodFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rr, req)
		if rr.Code != tc.status {
			t.Errorf("%s %q: got %d want %d", tc.method, tc.ctype, rr.Code, tc.status)
		}
	}
}

func TestGetUserFromAuthHeaderTable(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	padded := base64.URLEncoding.EncodeToString
	stdRaw := base64.RawStdEncoding.EncodeToString
	claims := `{"sub":"alice","n":5,"nested":{"id":"bob","deep":{"x":"y"}}}`
	// payload whose URL-safe encoding contains '-' or '_' (and the standard one '+' or '/')
	special := `{"sub":"a?b>c~~"}`
	cases := []struct {
		name, header, field, want, errPart string
	}{
		{"valid", "Bearer h." + enc([]byte(claims)) + ".s", "sub", "alice", ""},
		{"no bearer prefix is accepted", "h." + enc([]byte(claims)) + ".s", "sub", "alice", ""},
		{"nested", "Bearer h." + enc([]byte(claims)) + ".s", "nested.id", "bob", ""},
		{"deeply nested", "Bearer h." + enc([]byte(claims)) + ".s", "nested.deep.x", "y", ""},
		{"url safe alphabet", "Bearer h." + enc([]byte(special)) + ".s", "sub", "a?b>c~~", ""},
		{"missing header", "", "sub", "", "no token provided"},
		{"only bearer", "Bearer ", "sub", "", "no token provided"},
		{"two parts", "Bearer a.b", "sub", "", "invalid JWT format"},
		{"four parts", "Bearer a.b.c.d", "sub", "", "invalid JWT format"},
		{"garbage", "Bearer garbage", "sub", "", "invalid JWT format"},
		{"empty claims part", "Bearer h..s", "sub", "", "error unmarshalling JWT claims"},
		{"invalid base64", "Bearer h.%%%.s", "sub", "", "error decoding JWT claims"},
		{"padded base64 is rejected", "Bearer h." + padded([]byte(`{"sub":"x"}`)) + ".s", "sub", "", "error decoding JWT claims"},
		{"standard alphabet is rejected", "Bearer h." + stdRaw([]byte(special)) + ".s", "sub", "", "error decoding JWT claims"},
		{"non json payload", "Bearer h." + enc([]byte("hello")) + ".s", "sub", "", "error unmarshalling JWT claims"},
		{"json array payload", "Bearer h." + enc([]byte(`["sub"]`)) + ".s", "sub", "", "error unmarshalling JWT claims"},
		{"json null payload", "Bearer h." + enc([]byte(`null`)) + ".s", "sub", "", "is not a string"},
		{"no sub claim", "Bearer h." + enc([]byte(`{"other":"x"}`)) + ".s", "sub", "", "field sub is not a string"},
		{"sub is a number", "Bearer h." + enc([]byte(claims)) + ".s", "n", "", "field n is not a string"},
		{"sub is null", "Bearer h." + enc([]byte(`{"sub":null}`)) + ".s", "sub", "", "is not a string"},
		{"missing nested object", "Bearer h." + enc([]byte(claims)) + ".s", "other.id", "", "field other not found"},
		{"nested path through a string", "Bearer h." + enc([]byte(claims)) + ".s", "sub.id", "", "field sub not found"},
		{"empty field name", "Bearer h." + enc([]byte(claims)) + ".s", "", "", "is not a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GetUserFromAuthHeader(tc.header, tc.field)
			if tc.errPart == "" {
				if err != nil || got != tc.want {
					t.Fatalf("got %q %v, want %q", got, err, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errPart) || got != "" {
				t.Fatalf("got %q %v, want error containing %q", got, err, tc.errPart)
			}
		})
	}
}

type hostnameExecutor struct{ cmd []string }

func (h *hostnameExecutor) CallCMD(_ context.Context, cmd []string, _ string) ([]byte, []byte, error) {
	h.cmd = cmd
	return []byte("pod-1\r\n"), nil, nil
}

// Original behaviour: the pod name is the output of "hostname", run through the executor, without line ending.
func TestGetPodNameRunsHostname(t *testing.T) {
	s := filterServer()
	h := &hostnameExecutor{}
	s.executor = h
	got, err := s.getPodName()
	if err != nil || got != "pod-1" || len(h.cmd) != 1 || h.cmd[0] != "hostname" {
		t.Fatalf("%q %v %v", got, err, h.cmd)
	}
	s.executor = &BrokenFakeExecutor{}
	if _, err := s.getPodName(); err == nil {
		t.Fatal("expected the executor error")
	}
}

func TestIsValidUUID(t *testing.T) {
	for in, want := range map[string]bool{
		"71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4":          true,
		"71B2A100-F3F6-11E9-81B4-2A2AE2DBCCE4":          true,
		"urn:uuid:71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4": true,
		"71b2a100f3f611e981b42a2ae2dbcce4":              true,
		"":                                              false,
		"123":                                           false,
		"71b2a100-f3f6-11e9-81b4-2a2ae2dbcce":           false,
		"g1b2a100-f3f6-11e9-81b4-2a2ae2dbcce4":          false,
	} {
		if isValidUUID(in) != want {
			t.Errorf("%q: want %v", in, want)
		}
	}
}

// The OPA denial surfaces as an error of the MPC execution: HTTP 500 with the message of the policy decision.
func TestActivationReportsOpaDecisionAsExecutionError(t *testing.T) {
	msgs := []string{
		"unauthorized: program cannot be executed",
		"failed to check if program can be executed: failed to check 'execute' access (Code 500)",
		"failed to check if program can be executed: timeout",
	}
	for _, msg := range msgs {
		cause := errors.New(msg)
		s := newConsecutiveServer()
		s.player = &scriptedPlayer{s: s, script: func(respCh chan []byte, _, execErrCh chan error) {
			w := NewSPDZWrapper(&CtxConfig{Act: &Activation{GameID: consecutiveGameID}, Spdz: &SPDZEngineTypedConfig{}}, respCh, execErrCh,
				zap.NewNop().Sugar(), func(*CtxConfig) ([]byte, error) { return nil, cause })
			w.Execute(twoPlayerEvent())
		}}
		rr := serve(t, s, time.Second)
		want := fmt.Sprintf("error during MPC execution: %s", msg)
		if rr.Code != http.StatusInternalServerError || rr.Body.String() != want {
			t.Errorf("got %d %q, want 500 %q", rr.Code, rr.Body.String(), want)
		}
	}
}
