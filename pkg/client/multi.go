package client

import (
	"context"
	"errors"
	"sync"

	"github.com/Samrat7745/ephemeral/pkg/types"
	"github.com/google/uuid"
)

// MultiClient runs one game on all parties' Ephemeral services at once.
type MultiClient struct {
	clients []*Client
}

// NewMulti creates a client for every endpoint in o.Endpoints.
func NewMulti(o Options) (*MultiClient, error) {
	if len(o.Endpoints) == 0 {
		return nil, errors.New("At least one Ephemeral service endpoint has to be provided.")
	}
	m := &MultiClient{}
	for _, e := range o.Endpoints {
		c, err := newClient(o, e)
		if err != nil {
			return nil, err
		}
		m.clients = append(m.clients, c)
	}
	return m, nil
}

// Endpoints returns the configured endpoints in order.
func (m *MultiClient) Endpoints() []Endpoint {
	out := make([]Endpoint, len(m.clients))
	for i, c := range m.clients {
		out[i] = c.endpoint
	}
	return out
}

// Execute runs code on all services with a random game id; the inputs are Amphora secret ids and the output
// is stored in Amphora (types.AmphoraSecret), like Java's EphemeralMultiClient.
func (m *MultiClient) Execute(ctx context.Context, code string, inputSecretIDs []uuid.UUID) ([]Result, error) {
	return m.ExecuteGame(ctx, code, uuid.New(), inputSecretIDs)
}

// ExecuteGame is Execute with an explicit game id.
func (m *MultiClient) ExecuteGame(ctx context.Context, code string, gameID uuid.UUID, inputSecretIDs []uuid.UUID) ([]Result, error) {
	params := make([]string, len(inputSecretIDs))
	for i, id := range inputSecretIDs {
		params[i] = id.String()
	}
	acts := make([]Activation, len(m.clients))
	for i := range acts {
		acts[i] = Activation{AmphoraParams: params, SecretParams: []string{}, GameID: gameID.String(), Code: code,
			Output: types.OutputConfig{Type: types.AmphoraSecret}}
	}
	return m.ExecuteActivations(ctx, acts)
}

// ExecuteActivations sends activations[i] to endpoint i in parallel. Results are in endpoint order; if any call failed the
// error of the first failing endpoint is returned (the other results are discarded, as in the Java client).
func (m *MultiClient) ExecuteActivations(ctx context.Context, activations []Activation) ([]Result, error) {
	if len(activations) != len(m.clients) {
		return nil, errors.New("one activation per endpoint is required")
	}
	res := make([]*Result, len(m.clients))
	errs := make([]error, len(m.clients))
	var wg sync.WaitGroup
	for i, c := range m.clients {
		wg.Add(1)
		go func(i int, c *Client) {
			defer wg.Done()
			res[i], errs[i] = c.Execute(ctx, activations[i])
		}(i, c)
	}
	wg.Wait()
	out := make([]Result, len(res))
	for i := range res {
		if errs[i] != nil {
			return nil, errs[i]
		}
		out[i] = *res[i]
	}
	return out, nil
}
