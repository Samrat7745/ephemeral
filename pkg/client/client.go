package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/carbynestack/ephemeral/pkg/types"
	"github.com/google/uuid"
)

// Activation is the request body of the Ephemeral service.
type Activation = types.Activation

// Endpoint addresses one Ephemeral service: the Knative service URI plus the application (service) name.
type Endpoint struct {
	ServiceURI  string
	Application string
}

// ActivationURL is the request URL: the host is prefixed with the application name ("app.host:port"),
// and compile=true is added when the activation carries code (Java EphemeralEndpoint.getActivationUri).
func (e Endpoint) ActivationURL(compile bool) (string, error) {
	u, err := url.Parse(e.ServiceURI)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid service URI %q", e.ServiceURI)
	}
	if e.Application == "" {
		return "", errors.New("application must not be empty")
	}
	host := e.Application + "." + u.Hostname()
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	u.Host = host
	if compile {
		q := u.Query()
		q.Set("compile", "true")
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// ActivationError is Java's ActivationError: the service answered with a non-2xx status.
type ActivationError struct {
	ResponseCode int
	Message      string
}

func (e *ActivationError) Error() string {
	return fmt.Sprintf("activation failed with HTTP %d: %s", e.ResponseCode, e.Message)
}

// Result is the activation response. For AmphoraSecret outputs the entries are secret ids.
type Result struct {
	Response []string `json:"response"`
}

// SecretIDs parses the response entries as UUIDs (AmphoraSecret output).
func (r Result) SecretIDs() ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, len(r.Response))
	for i, s := range r.Response {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("response entry %d is not a secret id: %w", i, err)
		}
		ids[i] = id
	}
	return ids, nil
}

// BearerTokenProvider returns the token for an endpoint.
type BearerTokenProvider func(Endpoint) string

// Options configure a client.
type Options struct {
	Endpoints           []Endpoint
	TrustedCertificates []string // PEM file paths added to the system roots
	NoSSLValidation     bool
	BearerTokenProvider BearerTokenProvider
	Timeout             time.Duration // default: none (activations run as long as the MPC computation)
	// HTTPClient replaces the transport entirely (tests, custom proxies); TLS options are then ignored.
	HTTPClient *http.Client
}

func (o Options) httpClient() (*http.Client, error) {
	if o.HTTPClient != nil {
		return o.HTTPClient, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, p := range o.TrustedCertificates {
		pem, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", p)
		}
	}
	return &http.Client{Timeout: o.Timeout, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, InsecureSkipVerify: o.NoSSLValidation}}}, nil
}

// Client talks to a single Ephemeral service.
type Client struct {
	endpoint Endpoint
	token    func() string
	http     *http.Client
}

// New creates a client for the single endpoint in o.Endpoints.
func New(o Options) (*Client, error) {
	if len(o.Endpoints) != 1 {
		return nil, errors.New("exactly one endpoint is required")
	}
	return newClient(o, o.Endpoints[0])
}

func newClient(o Options, e Endpoint) (*Client, error) {
	if _, err := e.ActivationURL(false); err != nil {
		return nil, err
	}
	hc, err := o.httpClient()
	if err != nil {
		return nil, err
	}
	c := &Client{endpoint: e, http: hc}
	if o.BearerTokenProvider != nil {
		c.token = func() string { return o.BearerTokenProvider(e) }
	}
	return c, nil
}

// Endpoint returns the service this client talks to.
func (c *Client) Endpoint() Endpoint { return c.endpoint }

// Execute POSTs the activation. HTTP failures are returned as *ActivationError.
func (c *Client) Execute(ctx context.Context, a Activation) (*Result, error) {
	target, err := c.endpoint.ActivationURL(a.Code != "")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != nil {
		req.Header.Set("Authorization", "Bearer "+c.token())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &ActivationError{ResponseCode: resp.StatusCode, Message: string(raw)}
	}
	var r Result
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("unreadable activation response %q: %w", raw, err)
	}
	return &r, nil
}
