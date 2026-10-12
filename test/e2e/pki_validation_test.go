//go:build integration

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

package e2e

import (
	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	corev1 "k8s.io/api/core/v1"

	utils "github.com/sigstore/model-validation-operator/test/utils"
)

var _ = Describe("PKI validation", func() {
	It("verifies a model signature against an X.509 certificate chain", func() {
		const (
			modelName = "pki-chain-model"
			podName   = "pki-chain-pod"
			modelPath = "/fixtures/v1.1.0-certificate"
			caPath    = "/fixtures/keys/certificate/ca-cert.pem"
		)

		DeferCleanup(func() {
			deleteE2EResource("pod", podName)
			deleteE2EResource("modelvalidation", modelName)
		})

		config := `    pkiConfig:
      certificateAuthority: ` + caPath
		Expect(utils.KubectlApply(authModelValidation(modelName, config, modelPath,
			modelPath+"/model.sig", ""), nil)).To(Succeed())
		Expect(utils.KubectlApply(podTemplate, authFixturePodData(podName, modelName))).To(Succeed())

		By("verifying PKI-specific arguments were injected")
		var pod corev1.Pod
		Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &pod)).To(Succeed())
		Expect(pod.Spec.InitContainers).To(HaveLen(1))
		Expect(pod.Spec.InitContainers[0].Args).To(ContainElements(
			"certificate", "--certificate_chain", caPath))

		expectSuccessfulValidation(podName)
	})
})
