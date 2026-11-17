package client

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Samrat7745/ephemeral/pkg/types"
	"github.com/google/uuid"
)

// redirectClient sends every request to srv regardless of the (application-prefixed) host.
func redirectClient(srv *httptest.Server) *http.Client {
	addr := strings.TrimPrefix(srv.URL, "http://")
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}}
}

func TestActivationURL(t *testing.T) {
	e := Endpoint{ServiceURI: "https://vcp0.example.org:8443/x", Application: "ephemeral-generic"}
	u, err := e.ActivationURL(true)
	if err != nil || u != "https://ephemeral-generic.vcp0.example.org:8443/x?compile=true" {
		t.Fatal(err, u)
	}
	if u, _ = e.ActivationURL(false); strings.Contains(u, "compile") {
		t.Fatalf("no compile expected: %s", u)
	}
	if _, err := (Endpoint{ServiceURI: "nohost", Application: "a"}).ActivationURL(false); err == nil {
		t.Fatal("invalid URI must fail")
	}
	if _, err := (Endpoint{ServiceURI: "http://h", Application: ""}).ActivationURL(false); err == nil {
		t.Fatal("empty application must fail")
	}
}

func TestExecuteSendsActivationAndMapsErrors(t *testing.T) {
	var host, auth, query string
	var body []byte
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, auth, query = r.Host, r.Header.Get("Authorization"), r.URL.RawQuery
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		if status == 200 {
			_, _ = w.Write([]byte(`{"response":["` + uuid.NewString() + `"]}`))
		} else {
			_, _ = w.Write([]byte("game not ready"))
		}
	}))
	defer srv.Close()
	c, err := New(Options{Endpoints: []Endpoint{{ServiceURI: "http://vcp0.test:80", Application: "app"}}, HTTPClient: redirectClient(srv),
		BearerTokenProvider: func(Endpoint) string { return "tok" }})
	if err != nil {
		t.Fatal(err)
	}
	act := Activation{AmphoraParams: []string{"a", "b"}, GameID: "g1", Code: "print_ln('x')", Output: types.OutputConfig{Type: types.AmphoraSecret}}
	r, err := c.Execute(context.Background(), act)
	if err != nil {
		t.Fatal(err)
	}
	if host != "app.vcp0.test:80" || auth != "Bearer tok" || query != "compile=true" {
		t.Fatalf("host=%q auth=%q query=%q", host, auth, query)
	}
	if ids, err := r.SecretIDs(); err != nil || len(ids) != 1 {
		t.Fatal(err, r)
	}
	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil || sent["gameID"] != "g1" || sent["code"] == "" || sent["output"].(map[string]any)["type"] != "AMPHORASECRET" {
		t.Fatalf("body %s", body)
	}
	act.Code = ""
	if _, err = c.Execute(context.Background(), act); err != nil || query != "" {
		t.Fatalf("no compile without code: err=%v query=%q", err, query)
	}
	status = 409
	_, err = c.Execute(context.Background(), act)
	ae, ok := err.(*ActivationError)
	if !ok || ae.ResponseCode != 409 || ae.Message != "game not ready" {
		t.Fatalf("want ActivationError 409, got %#v", err)
	}
}

func TestMultiClientRunsAllPlayersWithSameGame(t *testing.T) {
	var mu sync.Mutex
	games := map[string]bool{}
	hosts := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var a types.Activation
		_ = json.Unmarshal(b, &a)
		mu.Lock()
		games[a.GameID] = true
		hosts[r.Host] = true
		mu.Unlock()
		if len(a.AmphoraParams) != 2 || a.Output.Type != types.AmphoraSecret {
			http.Error(w, "bad activation", 400)
			return
		}
		_, _ = w.Write([]byte(`{"response":["` + uuid.NewString() + `"]}`))
	}))
	defer srv.Close()
	m, err := NewMulti(Options{HTTPClient: redirectClient(srv), Endpoints: []Endpoint{
		{"http://vcp0.test", "app"}, {"http://vcp1.test", "app"}}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Execute(context.Background(), "code", []uuid.UUID{uuid.New(), uuid.New()})
	if err != nil || len(res) != 2 {
		t.Fatal(err, res)
	}
	if len(games) != 1 || !hosts["app.vcp0.test"] || !hosts["app.vcp1.test"] {
		t.Fatalf("games=%v hosts=%v", games, hosts)
	}
	if len(m.Endpoints()) != 2 {
		t.Fatal("endpoints")
	}
}

func TestMultiClientReportsFirstFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "app.vcp1") {
			http.Error(w, "boom", 500)
			return
		}
		_, _ = w.Write([]byte(`{"response":[]}`))
	}))
	defer srv.Close()
	m, _ := NewMulti(Options{HTTPClient: redirectClient(srv), Endpoints: []Endpoint{{"http://vcp0.test", "app"}, {"http://vcp1.test", "app"}}})
	_, err := m.Execute(context.Background(), "c", nil)
	if ae, ok := err.(*ActivationError); !ok || ae.ResponseCode != 500 {
		t.Fatalf("want first failure 500, got %#v", err)
	}
	if _, err := NewMulti(Options{}); err == nil {
		t.Fatal("no endpoints must fail")
	}
}

// The Java client serialises gameId (not gameID) and an empty secretParams array; the service must read both.
func TestServiceTypesAcceptJavaClientBody(t *testing.T) {
	javaBody := `{"amphoraParams":["u1","u2"],"secretParams":[],"gameId":"g-7","code":"x","output":{"type":"AMPHORASECRET"}}`
	var a types.Activation
	if err := json.Unmarshal([]byte(javaBody), &a); err != nil {
		t.Fatal(err)
	}
	if a.GameID != "g-7" || len(a.AmphoraParams) != 2 || a.Code != "x" || a.Output.Type != types.AmphoraSecret {
		t.Fatalf("%+v", a)
	}
}
