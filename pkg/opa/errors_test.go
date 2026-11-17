// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package opa_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Samrat7745/ephemeral/pkg/opa"
	"go.uber.org/zap"
)

func opaClient(t *testing.T, h http.HandlerFunc) (*opa.Client, func()) {
	srv := httptest.NewServer(h)
	c, err := opa.NewClient(zap.NewNop().Sugar(), srv.URL, "carbynestack.def")
	if err != nil {
		t.Fatal(err)
	}
	return c, srv.Close
}

func TestCanExecuteDecisions(t *testing.T) {
	for body, want := range map[string]bool{`{"result":true}`: true, `{"result":false}`: false, `{}`: false} {
		c, stop := opaClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/data/carbynestack/def/execute" || r.Method != "POST" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			w.Write([]byte(body))
		})
		got, err := c.CanExecute(map[string]string{"a": "b"})
		stop()
		if err != nil || got != want {
			t.Errorf("%s: got %v %v, want %v", body, got, err, want)
		}
	}
}

func TestCanExecuteErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		errPart string
	}{
		{"500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, "(Code 500)"},
		{"404 policy missing", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }, "(Code 404)"},
		{"malformed json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{")) }, "failed to unmarshal"},
		{"wrong result type", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"result":"yes"}`)) }, "failed to unmarshal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, stop := opaClient(t, tc.handler)
			defer stop()
			allowed, err := c.CanExecute(nil)
			if err == nil || !strings.Contains(err.Error(), tc.errPart) || allowed {
				t.Fatalf("allowed=%v err=%v", allowed, err)
			}
		})
	}
}

func TestCanExecuteTimeout(t *testing.T) {
	release := make(chan struct{})
	c, stop := opaClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer stop()
	defer close(release)
	c.HttpClient.Timeout = 100 * time.Millisecond
	allowed, err := c.CanExecute(nil)
	if err == nil || allowed || !strings.Contains(err.Error(), "failed to check \"execute\" access") {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
}

func TestCanExecuteUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	c, _ := opa.NewClient(zap.NewNop().Sugar(), srv.URL, "p")
	srv.Close()
	if allowed, err := c.CanExecute(nil); err == nil || allowed {
		t.Fatalf("%v %v", allowed, err)
	}
}

func TestCanExecuteUnmarshalableInput(t *testing.T) {
	c, stop := opaClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"result":true}`)) })
	defer stop()
	if _, err := c.CanExecute(make(chan int)); err == nil || !strings.Contains(err.Error(), "invalid opa input") {
		t.Fatalf("%v", err)
	}
}

func TestGenerateTagsErrors(t *testing.T) {
	c, stop := opaClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	defer stop()
	if tags, err := c.GenerateTags(nil); err == nil || tags != nil {
		t.Fatalf("%v %v", tags, err)
	}
	c2, stop2 := opaClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":[{"key":"k","value":"v","valueType":"STRING"}]}`))
	})
	defer stop2()
	tags, err := c2.GenerateTags(nil)
	if err != nil || len(tags) != 1 || tags[0].Key != "k" {
		t.Fatalf("%v %v", tags, err)
	}
}

func TestNewClientValidation(t *testing.T) {
	logger := zap.NewNop().Sugar()
	for _, ep := range []string{"", "not a url", "://x"} {
		if _, err := opa.NewClient(logger, ep, "p"); err == nil {
			t.Errorf("endpoint %q must be rejected", ep)
		}
	}
	for _, pkg := range []string{"", "   "} {
		if _, err := opa.NewClient(logger, "http://opa:8181", pkg); err == nil {
			t.Errorf("policy package %q must be rejected", pkg)
		}
	}
	c, err := opa.NewClient(logger, "http://opa:8181/", " a.b ")
	if err != nil || c.PolicyPackage != "a.b" {
		t.Fatalf("%v %+v", err, c)
	}
}
