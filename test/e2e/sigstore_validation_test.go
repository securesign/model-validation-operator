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

var _ = Describe("Sigstore keyless validation", func() {
	It("verifies a keyless bundle with certificate identity and OIDC issuer", func() {
		const (
			modelName = "sigstore-keyless-model"
			podName   = "sigstore-keyless-pod"
			modelPath = "/fixtures/v1.1.0-sigstore"
		)

		DeferCleanup(func() {
			deleteE2EResource("pod", podName)
			deleteE2EResource("modelvalidation", modelName)
		})

		config := `    sigstoreConfig:
      certificateIdentity: stefanb@us.ibm.com
      certificateOidcIssuer: https://sigstore.verify.ibm.com/oauth2
    clientTrustConfig:
      trustConfigPath: /fixtures/keys/sigstore/trusted_root.json`
		Expect(utils.KubectlApply(authModelValidation(modelName, config, modelPath,
			modelPath+"/model.sig", ""), nil)).To(Succeed())
		Expect(utils.KubectlApply(podTemplate, authFixturePodData(podName, modelName))).To(Succeed())

		By("verifying Sigstore-specific arguments were injected")
		var pod corev1.Pod
		Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &pod)).To(Succeed())
		Expect(pod.Spec.InitContainers).To(HaveLen(1))
		Expect(pod.Spec.InitContainers[0].Args).To(ContainElements(
			"sigstore", "--identity", "stefanb@us.ibm.com", "--identity_provider",
			"https://sigstore.verify.ibm.com/oauth2", "--trust_config",
			"/fixtures/keys/sigstore/trusted_root.json"))

		expectSuccessfulValidation(podName)
	})
})
