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
	_ "embed"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/sigstore/model-validation-operator/internal/constants"

	utils "github.com/sigstore/model-validation-operator/test/utils"
)

const (
	pkiModelPath = "/fixtures/v1.1.0-certificate"
	pkiCAPath    = "/fixtures/keys/certificate/ca-cert.pem"
)

// This unrelated public test CA is never trusted by the signed model fixture.
// No private key is retained or required.
//
//go:embed testdata/untrusted-ca.pem
var untrustedTestCA string

var _ = Describe("PKI validation", func() {
	It("verifies a model signature against an X.509 certificate chain", func() {
		const (
			modelName = "pki-chain-model"
			podName   = "pki-chain-pod"
		)

		DeferCleanup(func() {
			deleteE2EResource("pod", podName)
			deleteE2EResource("modelvalidation", modelName)
		})

		config := `    pkiConfig:
      certificateAuthority: ` + pkiCAPath
		Expect(utils.KubectlApply(authModelValidation(modelName, config, pkiModelPath,
			pkiModelPath+"/model.sig", ""), nil)).To(Succeed())
		Expect(utils.KubectlApply(podTemplate, authFixturePodData(podName, modelName))).To(Succeed())

		By("verifying PKI-specific arguments were injected")
		var pod corev1.Pod
		Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &pod)).To(Succeed())
		Expect(pod.Spec.InitContainers).To(HaveLen(1))
		Expect(pod.Spec.InitContainers[0].Args).To(ContainElements(
			"certificate", "--certificate_chain", pkiCAPath))

		expectSuccessfulValidation(podName)
	})

	It("rejects a certificate-signed model when its CA is not trusted", func() {
		const (
			modelName   = "pki-untrusted-model"
			podName     = "pki-untrusted-pod"
			caConfigMap = "pki-unrelated-test-ca"
			caVolume    = "unrelated-ca"
			caDirectory = "/unrelated-ca"
		)
		DeferCleanup(func() {
			deleteE2EResource("pod", podName)
			deleteE2EResource("modelvalidation", modelName)
			deleteE2EResource("configmap", caConfigMap)
		})
		ca := corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Name: caConfigMap, Namespace: webhookTestNamespace},
			Data:       map[string]string{"ca.pem": untrustedTestCA},
		}
		body, err := json.Marshal(ca)
		Expect(err).NotTo(HaveOccurred())
		Expect(utils.KubectlApply(body, nil)).To(Succeed())

		config := `    pkiConfig:
      certificateAuthority: /unrelated-ca/ca.pem`
		Expect(utils.KubectlApply(authModelValidation(modelName, config, pkiModelPath,
			pkiModelPath+"/model.sig", ""), nil)).To(Succeed())

		// Keep the known-good signed model unchanged; change only the trusted
		// CA mounted into the validation agent through the workload's volumes.
		pod := corev1.Pod{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
			ObjectMeta: metav1.ObjectMeta{
				Name: podName, Namespace: webhookTestNamespace,
				Labels: map[string]string{constants.ModelValidationLabel: modelName},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{
					Name: "workload", Image: "busybox:latest", Command: []string{"sh", "-c", "sleep 3600"},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "fixtures", MountPath: "/fixtures", ReadOnly: true},
						{Name: caVolume, MountPath: caDirectory, ReadOnly: true},
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "fixtures", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
						Path: "/tmp/e2e-fixture-data", Type: ptr.To(corev1.HostPathDirectory),
					}}},
					{Name: caVolume, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: caConfigMap},
					}}},
				},
			},
		}
		body, err = json.Marshal(pod)
		Expect(err).NotTo(HaveOccurred())
		Expect(utils.KubectlApply(body, nil)).To(Succeed())
		expectFailedValidation(podName, "certificate chain verification failed", "unknown authority")
	})

})
