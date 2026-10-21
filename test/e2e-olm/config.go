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
	"fmt"
	"strconv"
	"strings"
)

const (
	operatorNamespace    = "model-validation-operator-system"
	webhookTestNamespace = "e2e-webhook-test-ns"
)

// upgradeConfig separates a generic next-build regression from a release
// certification. Only the latter can attest to the named tech-preview/GA inputs.
type upgradeConfig struct {
	baseCatalog, targetCatalog             string
	baseChannel, targetChannel             string
	packageName                            string
	controllerNamespace, catalogNamespace  string
	expectedBaseCSV, expectedTargetCSV     string
	expectedBaseImage, expectedTargetImage string
	gaCertification, validateModels        bool
}

func readUpgradeConfig(getenv func(string) string) (upgradeConfig, error) {
	value := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	config := upgradeConfig{
		baseCatalog:         value("TEST_BASE_CATALOG", ""),
		targetCatalog:       value("TEST_TARGET_CATALOG", ""),
		baseChannel:         value("OLM_BASE_CHANNEL", value("OLM_CHANNEL", "tech-preview")),
		packageName:         value("OLM_PACKAGE", "model-validation-operator"),
		controllerNamespace: value("OLM_CONTROLLER_NAMESPACE", ""),
		// Namespaced catalogs are visible to subscriptions in that namespace on
		// both standalone OLM and OpenShift. The OLM controller namespace is not
		// OpenShift's global catalog namespace (openshift-marketplace).
		catalogNamespace:    value("OLM_CATALOG_NAMESPACE", operatorNamespace),
		expectedBaseCSV:     value("TEST_EXPECTED_BASE_CSV", ""),
		expectedTargetCSV:   value("TEST_EXPECTED_TARGET_CSV", ""),
		expectedBaseImage:   value("TEST_EXPECTED_BASE_IMAGE", ""),
		expectedTargetImage: value("TEST_EXPECTED_TARGET_IMAGE", ""),
	}
	config.targetChannel = value("OLM_TARGET_CHANNEL", config.baseChannel)
	for key, target := range map[string]*bool{
		"TEST_GA_CERTIFICATION": &config.gaCertification,
		"TEST_VALIDATE_MODELS":  &config.validateModels,
	} {
		var err error
		*target, err = strconv.ParseBool(value(key, "false"))
		if err != nil {
			return config, fmt.Errorf("%s must be a boolean: %w", key, err)
		}
	}
	if config.baseCatalog == "" || config.targetCatalog == "" {
		return config, fmt.Errorf("TEST_BASE_CATALOG and TEST_TARGET_CATALOG are required")
	}
	if config.baseCatalog == config.targetCatalog {
		return config, fmt.Errorf("base and target catalog references must differ; use immutable release inputs")
	}
	if config.gaCertification {
		for _, key := range []string{
			"OLM_BASE_CHANNEL", "OLM_TARGET_CHANNEL",
			"TEST_EXPECTED_BASE_CSV", "TEST_EXPECTED_TARGET_CSV",
			"TEST_EXPECTED_BASE_IMAGE", "TEST_EXPECTED_TARGET_IMAGE",
		} {
			if value(key, "") == "" {
				return config, fmt.Errorf("%s is required for GA certification; obtain approved release inputs", key)
			}
		}
		if !config.validateModels {
			return config, fmt.Errorf("GA certification requires TEST_VALIDATE_MODELS=true and prepared signed model fixtures")
		}
	}
	if config.expectedBaseCSV != "" && config.expectedBaseCSV == config.expectedTargetCSV {
		return config, fmt.Errorf("expected base and target CSVs must differ")
	}
	if config.expectedBaseImage != "" && config.expectedBaseImage == config.expectedTargetImage {
		return config, fmt.Errorf("expected base and target operator images must differ")
	}
	return config, nil
}
