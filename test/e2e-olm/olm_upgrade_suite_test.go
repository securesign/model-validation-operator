//go:build upgrade

// Copyright 2025 The Sigstore Authors.
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
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	utils "github.com/sigstore/model-validation-operator/test/utils"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

var (
	olmNamespace    string
	upgradeSettings upgradeConfig
)

func detectOLMNamespace() (string, error) {
	for _, namespace := range []string{"openshift-operator-lifecycle-manager", "olm"} {
		output, err := getResourceIfPresent("namespace", namespace, "")
		if err != nil {
			return "", err
		}
		if output != "" {
			return namespace, nil
		}
	}
	return "hosted/external (no local controller namespace)", nil
}

func TestOLMUpgrade(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting OLM upgrade test suite\n")

	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(5 * time.Second)

	RunSpecs(t, "OLM Upgrade Suite")
}

var _ = BeforeSuite(func() {
	var err error
	upgradeSettings, err = readUpgradeConfig(os.Getenv)
	Expect(err).NotTo(HaveOccurred())

	By("detecting OLM namespace")
	olmNamespace = upgradeSettings.controllerNamespace
	if olmNamespace == "" {
		olmNamespace, err = detectOLMNamespace()
		Expect(err).NotTo(HaveOccurred())
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "OLM controller location (diagnostic only): %s; catalog namespace: %s\n",
		olmNamespace, upgradeSettings.catalogNamespace)

	By("verifying established OLM APIs are available")
	// ROSA hosted control planes do not expose OLM controller Pods in the
	// workload cluster. Do not confuse missing local Pods with missing OLM.
	// Actual controller readiness is proved by catalog resolution and CSV
	// installation below, not merely by the presence of these APIs.
	for _, name := range []string{
		"catalogsources.operators.coreos.com", "subscriptions.operators.coreos.com",
		"clusterserviceversions.operators.coreos.com", "installplans.operators.coreos.com",
	} {
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(utils.KubectlGetJSON("crd", name, "", &crd)).To(Succeed(), "OLM API %s must be installed", name)
		established := false
		for _, condition := range crd.Status.Conditions {
			if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
				established = true
			}
		}
		Expect(established).To(BeTrue(), "OLM API %s must be established", name)
	}

	By("verifying operator, test and catalog namespaces exist")
	for _, namespace := range []string{operatorNamespace, webhookTestNamespace, upgradeSettings.catalogNamespace} {
		var ns corev1.Namespace
		Expect(utils.KubectlGetJSON("namespace", namespace, "", &ns)).To(Succeed(),
			"Namespace %s must exist before running the suite", namespace)
	}
})

var _ = AfterSuite(func() {
	_, _ = fmt.Fprintf(GinkgoWriter, "OLM upgrade suite finished; see the spec and cleanup results above\n")
})

var _ = JustAfterEach(func() {
	if !CurrentSpecReport().Failed() {
		return
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "----------------------- Dumping operator resources -----------------------\n")
	if csvOutput, err := utils.KubectlGet("csv", "", operatorNamespace, "wide"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "CSVs: %s\n", csvOutput)
	}
	if podOutput, err := utils.KubectlGet("pods", "", operatorNamespace, "wide"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Pods: %s\n", podOutput)
	}
})
