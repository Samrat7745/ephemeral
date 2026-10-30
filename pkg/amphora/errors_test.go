// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package amphora

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func clientFor(t *testing.T, h http.HandlerFunc) (*Client, func()) {
	srv := httptest.NewServer(h)
	u, _ := url.Parse(srv.URL)
	c, err := NewClient(*u)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv.Close
}

func TestGetSecretShareErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		errPart string
	}{
		{"not found", 404, `{"error":"nope"}`, "#404"},
		{"server error", 500, "boom", "#500: boom"},
		{"malformed json", 200, "{not json", "invalid response body"},
		{"empty body", 200, "", "invalid response body"},
		{"wrong type", 200, `{"secretId":5}`, "invalid response body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, stop := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			defer stop()
			if _, err := c.GetSecretShare("id", "prog"); err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("want %q, got %v", tc.errPart, err)
			}
		})
	}
}

func TestGetSecretShareSendsProgramAndPath(t *testing.T) {
	c, stop := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/intra-vcp/secret-shares/the-id" || r.URL.Query().Get("programId") != "p&q" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"secretId":"the-id","data":"x","tags":[{"key":"k","value":"v","valueType":"STRING"}]}`))
	})
	defer stop()
	s, err := c.GetSecretShare("the-id", "p&q")
	if err != nil || s.Data != "x" || s.Tags[0].Key != "k" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestCreateSecretShareErrors(t *testing.T) {
	for _, status := range []int{200, 400, 409, 500} {
		c, stop := clientFor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write([]byte("no")) })
		if err := c.CreateSecretShare(&SecretShare{SecretID: "a"}); err == nil || !strings.Contains(err.Error(), "unexpected response code") {
			t.Errorf("status %d: %v", status, err)
		}
		stop()
	}
	c, stop := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" || r.Method != "POST" {
			t.Errorf("bad request %v", r.Header)
		}
		w.WriteHeader(201)
	})
	defer stop()
	if err := c.CreateSecretShare(&SecretShare{SecretID: "a"}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(srv.URL)
	srv.Close()
	c, _ := NewClient(*u)
	if _, err := c.GetSecretShare("a", "b"); err == nil || !strings.Contains(err.Error(), "http client failed") {
		t.Errorf("%v", err)
	}
	if err := c.CreateSecretShare(&SecretShare{}); err == nil {
		t.Error("expected error")
	}
}

func TestCreateSecretShareInvalidURLDoesNotPanic(t *testing.T) {
	// Regression: the request headers were set before the request creation error was checked (nil pointer).
	c := &Client{URL: url.URL{Scheme: "http", Host: "h\x7f"}}
	if err := c.CreateSecretShare(&SecretShare{}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := c.GetSecretShare("a", "b"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewClientValidatesURL(t *testing.T) {
	if _, err := NewClient(url.URL{}); err == nil {
		t.Fatal("expected invalid url error")
	}
	if _, err := NewClient(url.URL{Scheme: "http", Host: "amphora.default:8080"}); err != nil {
		t.Fatal(err)
	}
}
