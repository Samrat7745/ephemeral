// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/Samrat7745/ephemeral/pkg/types"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

const base = `"frontendURL":"1.2.3.4","playerCount":2,"stateTimeout":"10s","computationTimeout":"20s","connectTimeout":"3s"`

func TestParseConfigTable(t *testing.T) {
	cases := []struct {
		name, json, errPart string
	}{
		{"master", `{` + base + `}`, ""},
		{"slave", `{` + base + `,"slave":true,"masterHost":"m","masterPort":"80"}`, ""},
		{"invalid json", `{`, "unexpected end"},
		{"empty object", `{}`, "FrontendURL"},
		{"slave without host", `{` + base + `,"slave":true,"masterPort":"80"}`, "MasterHost"},
		{"slave without port", `{` + base + `,"slave":true,"masterHost":"m"}`, "MasterPort"},
		{"no player count", `{"frontendURL":"x"}`, "PlayerCount must be defined"},
		{"one player", `{"frontendURL":"x","playerCount":1}`, "2 or higher"},
		{"wrong type", `{"frontendURL":1}`, "cannot unmarshal"},
		{"missing timeouts", `{"frontendURL":"x","playerCount":2}`, "state timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conf, err := ParseConfig(writeConfig(t, c.json))
			if c.errPart == "" {
				if err != nil || conf == nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.errPart) {
				t.Fatalf("want error containing %q, got %v", c.errPart, err)
			}
		})
	}
}

func TestParseConfigValues(t *testing.T) {
	conf, err := ParseConfig(writeConfig(t, `{`+base+`,"slave":true,"masterHost":"m","masterPort":"80","port":"9","busSize":7,"portRange":"1:2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if conf.StateTimeout != 10*time.Second || conf.ComputationTimeout != 20*time.Second || conf.ConnectTimeout != 3*time.Second ||
		!conf.Slave || conf.MasterHost != "m" || conf.Port != "9" || conf.BusSize != 7 || conf.PortRange != "1:2" {
		t.Fatalf("unexpected %+v", conf)
	}
}

func TestParseConfigMissingFilePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("documented behaviour: a missing config file panics")
		}
	}()
	ParseConfig(filepath.Join(t.TempDir(), "missing.json"))
}

func TestSetDefaultsKeepsExplicitValues(t *testing.T) {
	conf := &DiscoveryTypedConfig{Port: "1", BusSize: 2, PortRange: "3:4"}
	SetDefaults(conf)
	if conf.Port != "1" || conf.BusSize != 2 || conf.PortRange != "3:4" {
		t.Fatalf("explicit values were overwritten: %+v", conf)
	}
	empty := &DiscoveryTypedConfig{}
	SetDefaults(empty)
	if empty.Port != DefaultPort || empty.BusSize != DefaultBusSize || empty.PortRange != DefaultPortRange {
		t.Fatalf("defaults missing: %+v", empty)
	}
}
