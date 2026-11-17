// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package client

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Samrat7745/ephemeral/pkg/types"
)

func TestNewRequiresExactlyOneEndpoint(t *testing.T) {
	ep := Endpoint{ServiceURI: "http://h:8080", Application: "app"}
	if _, err := New(Options{}); err == nil {
		t.Error("no endpoint")
	}
	if _, err := New(Options{Endpoints: []Endpoint{ep, ep}}); err == nil {
		t.Error("two endpoints")
	}
	c, err := New(Options{Endpoints: []Endpoint{ep}})
	if err != nil || c.Endpoint() != ep {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := New(Options{Endpoints: []Endpoint{{ServiceURI: "://", Application: "a"}}}); err == nil {
		t.Error("invalid uri")
	}
}

func TestTrustedCertificates(t *testing.T) {
	ep := Endpoint{ServiceURI: "https://h", Application: "app"}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	os.WriteFile(bad, []byte("not a certificate"), 0600)
	if _, err := New(Options{Endpoints: []Endpoint{ep}, TrustedCertificates: []string{bad}}); err == nil || !strings.Contains(err.Error(), "Unable to configure certificate management "+bad) {
		t.Errorf("invalid pem: %v", err)
	}
	if _, err := New(Options{Endpoints: []Endpoint{ep}, TrustedCertificates: []string{filepath.Join(dir, "missing.pem")}}); err == nil {
		t.Error("missing file")
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	good := filepath.Join(dir, "good.pem")
	os.WriteFile(good, pemOf(srv), 0600)
	if _, err := New(Options{Endpoints: []Endpoint{ep}, TrustedCertificates: []string{good}, NoSSLValidation: true}); err != nil {
		t.Errorf("valid pem: %v", err)
	}
}

func TestActivationErrorMessage(t *testing.T) {
	e := &ActivationError{ResponseCode: 403, Message: "nope"}
	if e.Error() != "activation failed with HTTP 403: nope" {
		t.Fatal(e.Error())
	}
}

func TestExecuteUnreadableResponseAndBearerToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte("<html>"))
	}))
	defer srv.Close()
	c, err := New(Options{
		Endpoints:           []Endpoint{{ServiceURI: "http://h:80", Application: "app"}},
		HTTPClient:          redirectClient(srv),
		BearerTokenProvider: func(Endpoint) string { return "tok" },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Execute(context.Background(), types.Activation{GameID: "g"})
	if err == nil || !strings.Contains(err.Error(), "unreadable activation response") || auth != "Bearer tok" {
		t.Fatalf("auth=%q err=%v", auth, err)
	}
}

func TestExecuteConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	hc := redirectClient(srv)
	srv.Close()
	c, _ := New(Options{Endpoints: []Endpoint{{ServiceURI: "http://h:80", Application: "app"}}, HTTPClient: hc})
	if _, err := c.Execute(context.Background(), types.Activation{}); err == nil {
		t.Fatal("expected error")
	}
}

func pemOf(srv *httptest.Server) []byte {
	cert := srv.Certificate()
	return []byte("-----BEGIN CERTIFICATE-----\n" + b64Lines(cert.Raw) + "-----END CERTIFICATE-----\n")
}

func b64Lines(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(s) > 64 {
		out.WriteString(s[:64] + "\n")
		s = s[64:]
	}
	out.WriteString(s + "\n")
	return out.String()
}
