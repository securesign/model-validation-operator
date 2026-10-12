//go:build upgrade

// Copyright 2026 The Sigstore Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2eolm

import (
	"strings"
	"testing"
)

const (
	trueValue      = "true"
	previewChannel = "tech-preview"
)

func TestUpgradeConfig(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{"TEST_BASE_CATALOG": "example.com/base:v1", "TEST_TARGET_CATALOG": "example.com/target:v2"}
	}
	tests := []struct {
		name      string
		modify    func(map[string]string)
		wantError string
	}{
		{name: "generic upgrade"},
		{name: "missing catalog", modify: func(e map[string]string) { delete(e, "TEST_BASE_CATALOG") },
			wantError: "are required"},
		{name: "same catalog", modify: func(e map[string]string) { e["TEST_TARGET_CATALOG"] = e["TEST_BASE_CATALOG"] },
			wantError: "must differ"},
		{name: "invalid boolean", modify: func(e map[string]string) { e["TEST_GA_CERTIFICATION"] = "yes" },
			wantError: "must be a boolean"},
		{name: "GA cannot default identity", modify: func(e map[string]string) { e["TEST_GA_CERTIFICATION"] = trueValue },
			wantError: "required for GA certification"},
		{name: "GA cannot use admission only", modify: func(e map[string]string) {
			addGAInputs(e)
			e["TEST_VALIDATE_MODELS"] = "false"
		}, wantError: "prepared signed model fixtures"},
		{name: "GA explicit same channel is valid", modify: addGAInputs},
		{name: "same CSV rejected", modify: func(e map[string]string) {
			addGAInputs(e)
			e["TEST_EXPECTED_TARGET_CSV"] = e["TEST_EXPECTED_BASE_CSV"]
		}, wantError: "CSVs must differ"},
		{name: "same operator image rejected", modify: func(e map[string]string) {
			addGAInputs(e)
			e["TEST_EXPECTED_TARGET_IMAGE"] = e["TEST_EXPECTED_BASE_IMAGE"]
		}, wantError: "images must differ"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := base()
			if tc.modify != nil {
				tc.modify(env)
			}
			config, err := readUpgradeConfig(func(key string) string { return env[key] })
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("wanted %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.catalogNamespace != operatorNamespace {
				t.Fatalf("catalog must default to subscription namespace, got %s", config.catalogNamespace)
			}
			if config.targetChannel != config.baseChannel {
				t.Fatalf("target channel should default to base")
			}
		})
	}
}

func TestUpgradeConfigNamespaceAndChannelOverrides(t *testing.T) {
	env := map[string]string{
		"TEST_BASE_CATALOG": "base:v1", "TEST_TARGET_CATALOG": "target:v2",
		"OLM_CONTROLLER_NAMESPACE": "openshift-operator-lifecycle-manager",
		"OLM_CATALOG_NAMESPACE":    "openshift-marketplace",
		"OLM_CHANNEL":              "legacy", "OLM_BASE_CHANNEL": previewChannel, "OLM_TARGET_CHANNEL": "stable",
	}
	config, err := readUpgradeConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.controllerNamespace != env["OLM_CONTROLLER_NAMESPACE"] ||
		config.catalogNamespace != env["OLM_CATALOG_NAMESPACE"] {
		t.Fatal("namespace overrides not preserved")
	}
	if config.baseChannel != previewChannel || config.targetChannel != "stable" {
		t.Fatal("channel overrides not preserved")
	}
	delete(env, "OLM_BASE_CHANNEL")
	delete(env, "OLM_TARGET_CHANNEL")
	config, err = readUpgradeConfig(func(key string) string { return env[key] })
	if err != nil || config.baseChannel != "legacy" || config.targetChannel != "legacy" {
		t.Fatalf("legacy OLM_CHANNEL fallback failed: %#v, %v", config, err)
	}
}

func addGAInputs(env map[string]string) {
	env["TEST_GA_CERTIFICATION"] = trueValue
	env["TEST_VALIDATE_MODELS"] = trueValue
	env["OLM_BASE_CHANNEL"] = previewChannel
	env["OLM_TARGET_CHANNEL"] = previewChannel
	env["TEST_EXPECTED_BASE_CSV"] = "mvo.v0.1.3"
	env["TEST_EXPECTED_TARGET_CSV"] = "mvo.v1.0.0"
	env["TEST_EXPECTED_BASE_IMAGE"] = "example.com/mvo:v0.1.3"
	env["TEST_EXPECTED_TARGET_IMAGE"] = "example.com/mvo:v1.0.0"
}
