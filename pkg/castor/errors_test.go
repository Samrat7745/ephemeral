// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package castor

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGetTuplesErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		errPart string
	}{
		{"not found", 404, "none", "#404: none"},
		{"server error", 500, "boom", "#500: boom"},
		{"malformed json", 200, "{", "invalid response body"},
		{"empty body", 200, "", "invalid response body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			c, _ := NewClient(*u)
			if _, err := c.GetTuples(1, BitGfp, uuid.New()); err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("want %q, got %v", tc.errPart, err)
			}
		})
	}
}

func TestGetTuplesRequestAndResponse(t *testing.T) {
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/intra-vcp/tuples" || q.Get("tupletype") != "BIT_GFP" || q.Get("count") != "7" || q.Get("reservationId") != id.String() {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"tuples":[{"shares":[{"value":"v","mac":"m"}]}]}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c, _ := NewClient(*u)
	l, err := c.GetTuples(7, BitGfp, id)
	if err != nil || len(l.Tuples) != 1 || l.Tuples[0].Shares[0].Mac != "m" {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestGetTuplesConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(srv.URL)
	srv.Close()
	c, _ := NewClient(*u)
	if _, err := c.GetTuples(1, BitGfp, uuid.New()); err == nil || !strings.Contains(err.Error(), "communication with castor failed") {
		t.Fatalf("%v", err)
	}
}

func TestNewClientValidatesURL(t *testing.T) {
	if _, err := NewClient(url.URL{}); err == nil {
		t.Fatal("expected invalid url error")
	}
}
