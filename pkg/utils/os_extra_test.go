// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package utils

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bash() *Commander { return &Commander{Command: "bash", Options: []string{"-c"}} }

func TestCallCMDSeparatesStdoutAndStderr(t *testing.T) {
	out, errOut, err := bash().CallCMD(context.Background(), []string{"echo out; echo err 1>&2"}, t.TempDir())
	if err != nil || string(out) != "out\n" || string(errOut) != "err\n" {
		t.Fatalf("out=%q err=%q %v", out, errOut, err)
	}
}

func TestCallCMDFailureKeepsOutputAndExitError(t *testing.T) {
	out, errOut, err := bash().CallCMD(context.Background(), []string{"echo partial; echo bad 1>&2; exit 3"}, t.TempDir())
	if err == nil || err.Error() != "exit status 3" || string(out) != "partial\n" || string(errOut) != "bad\n" {
		t.Fatalf("out=%q err=%q %v", out, errOut, err)
	}
}

func TestCallCMDRunsInDirectory(t *testing.T) {
	dir := t.TempDir()
	out, _, err := bash().CallCMD(context.Background(), []string{"pwd"}, dir)
	real, _ := filepath.EvalSymlinks(dir)
	if err != nil || strings.TrimSpace(string(out)) != real {
		t.Fatalf("%q %v (want %s)", out, err, real)
	}
}

func TestCallCMDMissingDirectoryAndCommand(t *testing.T) {
	if _, _, err := bash().CallCMD(context.Background(), []string{"true"}, "/does/not/exist"); err == nil {
		t.Fatal("expected error for a missing working directory")
	}
	c := &Commander{Command: "definitely-not-installed", Options: []string{"-c"}}
	if _, _, err := c.CallCMD(context.Background(), []string{"x"}, "."); err == nil {
		t.Fatal("expected error for a missing command")
	}
}

func TestCallCMDContextCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := bash().CallCMD(ctx, []string{"exec sleep 10"}, ".")
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("a cancelled context must stop the command: err=%v after %s", err, time.Since(start))
	}
}

func TestCallCMDDoesNotMutateDefaultOptions(t *testing.T) {
	c := NewCommander()
	c.Command = "bash"
	c.Options = []string{"-c"}
	for i := 0; i < 3; i++ {
		out, _, _ := c.CallCMD(context.Background(), []string{"echo hi"}, ".")
		if string(out) != "hi\n" || len(c.Options) != 1 {
			t.Fatalf("iteration %d: out=%q options=%v", i, out, c.Options)
		}
	}
}

func TestReadFileFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("content"), 0600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	b, err := ReadFile(link)
	if err != nil || string(b) != "content" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := ReadFile(filepath.Join(dir, "dangling")); err == nil {
		t.Fatal("expected error")
	}
	// a directory can be opened but not read
	if _, err := ReadFile(dir); err == nil {
		t.Fatal("expected error reading a directory")
	}
}

type certFiles struct{ dir string }

func genCerts(t *testing.T) certFiles {
	t.Helper()
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	for name, data := range map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM, "cacert": certPEM} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return certFiles{dir}
}

func TestCreateTLSConfigValid(t *testing.T) {
	c := genCerts(t)
	conf, err := CreateTLSConfig(c.dir)
	if err != nil || conf == nil {
		t.Fatalf("%v", err)
	}
	if len(conf.Certificates) != 1 || conf.RootCAs == nil || conf.InsecureSkipVerify {
		t.Fatalf("unexpected config %+v", conf)
	}
}

func TestCreateTLSConfigMissingFiles(t *testing.T) {
	for _, missing := range []string{"tls.crt", "tls.key", "cacert"} {
		c := genCerts(t)
		os.Remove(filepath.Join(c.dir, missing))
		if conf, err := CreateTLSConfig(c.dir); err == nil || conf != nil {
			t.Errorf("missing %s: expected an error, got conf=%v err=%v", missing, conf, err)
		}
	}
	if _, err := CreateTLSConfig(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for a missing directory")
	}
}

func TestCreateTLSConfigInvalidFiles(t *testing.T) {
	for _, bad := range []string{"tls.crt", "tls.key"} {
		c := genCerts(t)
		os.WriteFile(filepath.Join(c.dir, bad), []byte("garbage"), 0600)
		conf, err := CreateTLSConfig(c.dir)
		if err == nil || conf != nil {
			t.Errorf("invalid %s: expected an error, got conf=%v err=%v", bad, conf, err)
		}
	}
	// Original behaviour: a CA file without a valid PEM certificate yields (nil, nil).
	for _, ca := range [][]byte{[]byte("garbage"), nil} {
		c := genCerts(t)
		os.WriteFile(filepath.Join(c.dir, "cacert"), ca, 0600)
		if conf, err := CreateTLSConfig(c.dir); err != nil || conf != nil {
			t.Errorf("invalid CA %q: expected (nil, nil), got conf=%v err=%v", ca, conf, err)
		}
	}
}

func TestMockedRoundTrippers(t *testing.T) {
	m := &MockedRoundTripper{ExpectedPath: "/a", ExpectedRawQuery: "q=1", ReturnJSON: []byte(`{}`), ExpectedResponseCode: 201}
	for _, tc := range []struct {
		url  string
		code int
	}{{"http://h/a?q=1", 201}, {"http://h/b?q=1", 404}, {"http://h/a?q=2", 404}} {
		req, _ := http.NewRequest("GET", tc.url, nil)
		resp, err := m.RoundTrip(req)
		if err != nil || resp.StatusCode != tc.code {
			t.Errorf("%s: %v %v", tc.url, resp, err)
		}
	}
	if _, err := (&MockedBrokenRoundTripper{}).RoundTrip(&http.Request{}); err == nil {
		t.Error("expected error")
	}
}

func TestMockedFileIOAndFileMock(t *testing.T) {
	f := &SimpleFileMock{ReadWriteCountResponse: 3, IOError: errors.New("io"), CloseError: errors.New("close"), SetDeadlineError: errors.New("dl")}
	m := &MockedFileIO{
		CreatePathResponse: errors.New("p"), CreatePipeResponse: errors.New("pipe"), DeleteResponse: errors.New("d"),
		OpenReadResponse:          OpenReadResponse{File: f, Error: errors.New("r")},
		OpenWriteOrCreateResponse: OpenWriteOrCreateResponse{File: f},
		OpenWritePipeResponse:     OpenWritePipeResponse{Error: errors.New("wp")},
		ReadLineResponse:          ReadLineResponse{Line: "l", Error: io.EOF},
	}
	if m.CreatePath("a") == nil || m.CreatePipe("b") == nil || m.Delete("c") == nil {
		t.Fatal("responses not returned")
	}
	if got, err := m.OpenRead("r"); got != f || err == nil {
		t.Fatal("OpenRead")
	}
	if got, err := m.OpenWriteOrCreate("w"); got != f || err != nil {
		t.Fatal("OpenWriteOrCreate")
	}
	if _, err := m.OpenWritePipe("wp"); err == nil {
		t.Fatal("OpenWritePipe")
	}
	if l, err := m.ReadLine(f); l != "l" || err != io.EOF {
		t.Fatal("ReadLine")
	}
	if len(m.CreatePathCalls) != 1 || len(m.OpenReadCalls) != 1 || len(m.ReadLineCalls) != 1 || len(m.DeleteCalls) != 1 || len(m.CreatePipeCalls) != 1 {
		t.Fatal("calls not recorded")
	}
	n, err := f.Write([]byte("ab"))
	if n != 3 || err == nil {
		t.Fatal("Write")
	}
	f.WriteString("cd")
	buf := make([]byte, 3)
	if n, _ := f.Read(buf); n != 3 || !bytes.Equal(buf, []byte("abc")) {
		t.Fatalf("Read %q", buf)
	}
	if f.Close() == nil || f.SetWriteDeadline(time.Unix(1, 0)) == nil || f.WriteDeadline.IsZero() {
		t.Fatal("Close/SetWriteDeadline")
	}
}
