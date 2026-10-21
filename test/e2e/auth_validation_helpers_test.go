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
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	corev1 "k8s.io/api/core/v1"

	"github.com/sigstore/model-validation-operator/internal/constants"
	utils "github.com/sigstore/model-validation-operator/test/utils"
)

func expectSuccessfulValidation(podName string) {
	By("verifying the real validation agent completes successfully")
	Eventually(func(g Gomega) {
		var pod corev1.Pod
		g.Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &pod)).To(Succeed())
		g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
		g.Expect(pod.Status.InitContainerStatuses).ToNot(BeEmpty())
		terminated := pod.Status.InitContainerStatuses[0].State.Terminated
		g.Expect(terminated).NotTo(BeNil())
		g.Expect(terminated.ExitCode).To(BeZero())
	}, 90*time.Second, 3*time.Second).Should(Succeed())

	output, err := utils.Run(exec.Command("kubectl", "logs", podName, "-n", webhookTestNamespace,
		"-c", constants.ModelValidationInitContainerName))
	Expect(err).NotTo(HaveOccurred())
	Expect(output).To(ContainSubstring("Verification succeeded"))
}

func deleteE2EResource(resource, name string) {
	_, err := utils.Run(exec.Command("kubectl", "delete", resource, name, "-n", webhookTestNamespace,
		"--ignore-not-found=true", "--timeout=45s", "--request-timeout=50s"))
	Expect(err).NotTo(HaveOccurred(), "cleanup of %s/%s failed", resource, name)
}

func authFixturePodData(podName, modelName string) utils.PodTemplateData {
	return utils.DefaultPodData(podName, webhookTestNamespace, modelName, "auth-validation")
}

func authModelValidation(name, config, modelPath, signaturePath string, continuousValidation string) []byte {
	ignorePaths := fmt.Sprintf("    ignorePaths:\n    - %s/ignore-me\n", modelPath)
	if modelPath == "/data" {
		ignorePaths = ""
	}
	return []byte(fmt.Sprintf(`apiVersion: ml.sigstore.dev/v1alpha1
kind: ModelValidation
metadata:
  name: %s
  namespace: %s
spec:
  config:
%s
  model:
    path: %s
    signaturePath: %s
%s  imagePullPolicy: IfNotPresent
%s`, name, webhookTestNamespace, config, modelPath, signaturePath, ignorePaths, continuousValidation))
}
