// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package io

import (
	"context"
	"errors"
	"testing"

	"github.com/Samrat7745/ephemeral/pkg/amphora"
	. "github.com/Samrat7745/ephemeral/pkg/types"
	"go.uber.org/zap"
)

type scriptedOpa struct {
	allowed bool
	err     error
	input   interface{}
}

func (o *scriptedOpa) GenerateTags(interface{}) ([]amphora.Tag, error) { return nil, nil }
func (o *scriptedOpa) CanExecute(in interface{}) (bool, error) {
	o.input = in
	return o.allowed, o.err
}

type shareClient struct{ tags []amphora.Tag }

func (c *shareClient) GetSecretShare(id, _ string) (amphora.SecretShare, error) {
	return amphora.SecretShare{SecretID: id, Data: "d", Tags: c.tags}, nil
}
func (c *shareClient) CreateSecretShare(*amphora.SecretShare) error { return nil }

func feederWith(opa *scriptedOpa, tags []amphora.Tag) (*AmphoraFeeder, *Activation, *CtxConfig) {
	act := &Activation{AmphoraParams: []string{"a"}, GameID: "71b2a100-f3f6-11e9-81b4-2a2ae2dbcce4", Output: OutputConfig{Type: PlainText}}
	f := &AmphoraFeeder{
		conf:    &SPDZEngineTypedConfig{AmphoraClient: &shareClient{tags}, OpaClient: opa},
		carrier: &FakeCarrier{},
		logger:  zap.NewNop().Sugar(),
	}
	return f, act, &CtxConfig{Act: act, Context: context.TODO(), Spdz: &SPDZEngineTypedConfig{PlayerCount: 2}, AuthorizedUser: "alice"}
}

func TestExecutionDeniedByOpa(t *testing.T) {
	f, act, ctx := feederWith(&scriptedOpa{allowed: false}, nil)
	res, err := f.LoadFromSecretStoreAndFeed(act, "", ctx)
	if err == nil || err.Error() != "unauthorized: program cannot be executed" || res != nil {
		t.Fatalf("res=%q err=%v", res, err)
	}
}

func TestOpaFailureIsReported(t *testing.T) {
	f, act, ctx := feederWith(&scriptedOpa{err: errors.New("failed to check \"execute\" access (Code 500)")}, nil)
	_, err := f.LoadFromSecretStoreAndFeed(act, "", ctx)
	if err == nil || err.Error() != "failed to check if program can be executed: failed to check \"execute\" access (Code 500)" {
		t.Fatalf("%v", err)
	}
}

func TestOpaInputContainsExecutorAndEmptyPolicyWithoutTag(t *testing.T) {
	opa := &scriptedOpa{allowed: true}
	f, act, ctx := feederWith(opa, []amphora.Tag{{Key: "owner", Value: "bob"}})
	if _, err := f.LoadFromSecretStoreAndFeed(act, "", ctx); err != nil {
		t.Fatal(err)
	}
	in := opa.input.(map[string]interface{})
	inputs := in["inputs"].([]ActivationInput)
	if in["executor"] != "alice" || inputs[0].Owner != "bob" {
		t.Fatalf("%+v", in)
	}
	// Original behaviour: a secret without accessPolicy tag is passed to OPA with an empty access policy.
	if inputs[0].AccessPolicy != "" {
		t.Fatalf("access policy %q, want empty", inputs[0].AccessPolicy)
	}
}

func TestOpaInputUsesTaggedPolicy(t *testing.T) {
	opa := &scriptedOpa{allowed: true}
	f, act, ctx := feederWith(opa, []amphora.Tag{{Key: "accessPolicy", Value: "custom"}})
	f.LoadFromSecretStoreAndFeed(act, "", ctx)
	if p := opa.input.(map[string]interface{})["inputs"].([]ActivationInput)[0].AccessPolicy; p != "custom" {
		t.Fatalf("%q", p)
	}
}
