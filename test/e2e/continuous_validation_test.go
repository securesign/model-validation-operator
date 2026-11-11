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
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	corev1 "k8s.io/api/core/v1"

	"github.com/sigstore/model-validation-operator/internal/constants"
	utils "github.com/sigstore/model-validation-operator/test/utils"
)

var _ = Describe("ContinuousValidation lifecycle", func() {
	It("injects a native sidecar, re-validates at the configured interval, and terminates cleanly", func() {
		const (
			modelName = "continuous-validation-model"
			podName   = "continuous-validation-pod"
		)

		DeferCleanup(func() {
			deleteE2EResource("pod", podName)
			deleteE2EResource("modelvalidation", modelName)
		})

		continuous := `  continuousValidation:
    enabled: true
    interval: 1m
`
		config := `    publicKeyConfig:
      keyPath: /keys/test_public_key.pub`
		Expect(utils.KubectlApply(authModelValidation(modelName, config, "/data", "/data/model.sig",
			continuous), nil)).To(Succeed())
		Expect(utils.KubectlApply(podTemplate, authFixturePodData(podName, modelName))).To(Succeed())

		By("verifying the native sidecar contract")
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			g.Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &pod)).To(Succeed())
			g.Expect(pod.Spec.InitContainers).To(HaveLen(1))
			sidecar := pod.Spec.InitContainers[0]
			g.Expect(sidecar.RestartPolicy).NotTo(BeNil())
			g.Expect(*sidecar.RestartPolicy).To(Equal(corev1.ContainerRestartPolicyAlways))
			g.Expect(sidecar.Args).To(ContainElement("--interval=1m"))
			g.Expect(sidecar.ReadinessProbe).NotTo(BeNil())
			g.Expect(sidecar.ReadinessProbe.HTTPGet).NotTo(BeNil())
			g.Expect(sidecar.LivenessProbe).NotTo(BeNil())
			g.Expect(sidecar.LivenessProbe.HTTPGet).NotTo(BeNil())
			g.Expect(sidecar.ReadinessProbe.HTTPGet.Path).To(Equal("/ready"))
			g.Expect(sidecar.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
			g.Expect(pod.Annotations).To(HaveKeyWithValue(constants.ContinuousValidationAnnotationKey, "true"))
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
		}, 90*time.Second, 3*time.Second).Should(Succeed())

		By("waiting for a periodic re-validation after the configured one-minute interval")
		Eventually(func(g Gomega) {
			output, err := utils.Run(exec.Command("kubectl", "logs", podName, "-n", webhookTestNamespace,
				"-c", constants.ModelValidationInitContainerName))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(ContainSubstring("Running periodic validation"))
			g.Expect(output).To(ContainSubstring("Validation successful"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("deleting the pod and verifying the sidecar and tracking finalizer do not block cleanup")
		deleteE2EResource("pod", podName)
		Eventually(func() (bool, error) {
			return utils.KubectlResourceAbsent("pod", podName, webhookTestNamespace)
		}, 45*time.Second, 2*time.Second).Should(BeTrue())
	})
})
