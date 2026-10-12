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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"

	"github.com/sigstore/model-validation-operator/api/v1alpha1"
	"github.com/sigstore/model-validation-operator/internal/constants"
	utils "github.com/sigstore/model-validation-operator/test/utils"
)

var _ = Describe("Negative and failure scenarios", Ordered, func() {
	It("rejects malformed ModelValidation resources at the API boundary", func() {
		malformed := []byte(fmt.Sprintf(`apiVersion: ml.sigstore.dev/v1alpha1
kind: ModelValidation
metadata:
  name: malformed-validation
  namespace: %s
spec:
  config: {}
  model:
    path: relative/path
    signaturePath: ""
`, webhookTestNamespace))

		err := utils.KubectlApply(malformed, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Or(ContainSubstring("exactly one validation method"),
			ContainSubstring("spec.model.path"), ContainSubstring("spec.model.signaturePath")))
	})

	It("fails closed when a labeled pod references a missing CR", func() {
		const podName = "missing-cr-pod"
		DeferCleanup(deleteE2EResource, "pod", podName)

		err := utils.KubectlApply(podTemplate, authFixturePodData(podName, "does-not-exist"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does-not-exist"))
	})

	It("fails closed while all webhook replicas are unavailable", func() {
		const (
			deploymentName = "model-validation-controller-manager"
			modelName      = "operator-down-model"
			podName        = "operator-down-pod"
		)

		Expect(utils.KubectlApply(modelValidationTemplate, utils.CRTemplateData{
			ModelName: modelName,
			Namespace: webhookTestNamespace,
		})).To(Succeed())
		DeferCleanup(deleteE2EResource, "modelvalidation", modelName)

		replicaText, err := utils.KubectlGet("deployment", deploymentName, operatorNamespace,
			"jsonpath={.spec.replicas}")
		Expect(err).NotTo(HaveOccurred())
		replicas, err := strconv.Atoi(replicaText)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			_, restoreErr := utils.Run(exec.Command("kubectl", "scale", "deployment", deploymentName,
				"-n", operatorNamespace, fmt.Sprintf("--replicas=%d", replicas)))
			Expect(restoreErr).NotTo(HaveOccurred())
			_, restoreErr = utils.Run(exec.Command("kubectl", "rollout", "status", "deployment/"+deploymentName,
				"-n", operatorNamespace, "--timeout=120s"))
			Expect(restoreErr).NotTo(HaveOccurred())
			_, restoreErr = utils.Run(exec.Command("kubectl", "wait", "pod",
				"-l", "control-plane=controller-manager", "-n", operatorNamespace,
				"--for=condition=Ready", "--timeout=120s"))
			Expect(restoreErr).NotTo(HaveOccurred())
			Eventually(webhookEndpointAddresses, 60*time.Second, 2*time.Second).ShouldNot(BeEmpty())
		})

		if os.Getenv("E2E_COLLECT_COVERAGE") == "true" {
			By("preserving every live manager's coverage before terminating its process")
			_, snapshotErr := utils.Run(exec.Command("make", "snapshot-e2e-coverage"))
			Expect(snapshotErr).NotTo(HaveOccurred(), "coverage-enabled runs must retain pre-disruption counters")
		}

		_, err = utils.Run(exec.Command("kubectl", "scale", "deployment", deploymentName,
			"-n", operatorNamespace, "--replicas=0"))
		Expect(err).NotTo(HaveOccurred())
		Eventually(webhookEndpointAddresses, 60*time.Second, 2*time.Second).Should(BeEmpty())

		err = utils.KubectlApply(podTemplate, authFixturePodData(podName, modelName))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Or(ContainSubstring("failed calling webhook"),
			ContainSubstring("no endpoints available")))
	})

	It("detects a model corrupted after signing", func() {
		const (
			modelName = "corrupted-model"
			podName   = "corrupted-model-pod"
		)
		DeferCleanup(func() {
			deleteE2EResource("pod", podName)
			deleteE2EResource("modelvalidation", modelName)
		})

		Expect(utils.KubectlApply(modelValidationTemplate, utils.CRTemplateData{
			ModelName: modelName,
			Namespace: webhookTestNamespace,
		})).To(Succeed())

		pod := []byte(fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    validation.ml.sigstore.dev/ml: %s
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  initContainers:
  - name: corrupt-model
    image: busybox:latest
    command: ["sh", "-c", "cp -R /source/. /data/ && echo tampered > /data/corrupted"]
    volumeMounts:
    - {name: source, mountPath: /source, readOnly: true}
    - {name: model-copy, mountPath: /data}
  containers:
  - name: workload
    image: busybox:latest
    command: ["sh", "-c", "sleep 3600"]
    volumeMounts:
    - {name: model-copy, mountPath: /data, readOnly: true}
    - {name: keys, mountPath: /keys, readOnly: true}
  volumes:
  - name: source
    hostPath: {path: /tmp/e2e-model-data, type: Directory}
  - name: keys
    hostPath: {path: /tmp/e2e-keys-data, type: Directory}
  - name: model-copy
    emptyDir: {}
`, podName, webhookTestNamespace, modelName))
		Expect(utils.KubectlApply(pod, nil)).To(Succeed())

		Eventually(func(g Gomega) {
			var current corev1.Pod
			g.Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &current)).To(Succeed())
			g.Expect(current.Status.InitContainerStatuses).To(HaveLen(2))
			validationStatus := current.Status.InitContainerStatuses[1]
			g.Expect(validationStatus.State.Terminated).NotTo(BeNil())
			g.Expect(validationStatus.State.Terminated.ExitCode).NotTo(BeZero())
		}, 90*time.Second, 3*time.Second).Should(Succeed())

		logs, err := utils.Run(exec.Command("kubectl", "logs", podName, "-n", webhookTestNamespace,
			"-c", constants.ModelValidationInitContainerName))
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(ContainSubstring("verification failed"))
	})

	It("reports Sigstore verification failure when trust services are unreachable", func() {
		const (
			modelName        = "sigstore-network-failure"
			podName          = "sigstore-network-failure-pod"
			modelPath        = "/fixtures/v1.1.0-sigstore"
			controlModelName = "sigstore-network-control"
			controlPodName   = "sigstore-network-control-pod"
		)
		DeferCleanup(func() {
			deleteE2EResource("pod", podName)
			deleteE2EResource("pod", controlPodName)
			deleteE2EResource("modelvalidation", modelName)
			deleteE2EResource("modelvalidation", controlModelName)
		})

		config := `    sigstoreConfig:
      certificateIdentity: stefanb@us.ibm.com
      certificateOidcIssuer: https://sigstore.verify.ibm.com/oauth2`
		// Both Pods have the same deliberately unavailable loopback DNS server.
		// Local trust allows the control to prove image pulls, files, identity and
		// crypto work under that exact network fault, without public-service uptime.
		localTrust := config + `
    clientTrustConfig:
      trustConfigPath: /fixtures/keys/sigstore/trusted_root.json`
		Expect(utils.KubectlApply(authModelValidation(controlModelName, localTrust, modelPath,
			modelPath+"/model.sig", ""), nil)).To(Succeed())
		podTemplateText := `apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    validation.ml.sigstore.dev/ml: %s
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  dnsPolicy: None
  dnsConfig:
    nameservers: ["127.0.0.1"]
    options:
    - {name: timeout, value: "1"}
    - {name: attempts, value: "1"}
  containers:
  - name: workload
    image: busybox:latest
    imagePullPolicy: IfNotPresent
    command: ["sh", "-c", "sleep 3600"]
    volumeMounts:
    - {name: fixtures, mountPath: /fixtures, readOnly: true}
  volumes:
  - name: fixtures
    hostPath: {path: /tmp/e2e-fixture-data, type: Directory}
`
		controlPod := []byte(fmt.Sprintf(podTemplateText, controlPodName, webhookTestNamespace, controlModelName))
		Expect(utils.KubectlApply(controlPod, nil)).To(Succeed())
		expectSuccessfulValidation(controlPodName)

		By("requiring a trust-root fetch and asserting an actual DNS/connectivity failure")
		Expect(utils.KubectlApply(authModelValidation(modelName, config, modelPath,
			modelPath+"/model.sig", ""), nil)).To(Succeed())
		pod := []byte(fmt.Sprintf(podTemplateText, podName, webhookTestNamespace, modelName))
		Expect(utils.KubectlApply(pod, nil)).To(Succeed())

		Eventually(func(g Gomega) {
			var current corev1.Pod
			g.Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &current)).To(Succeed())
			g.Expect(current.Status.InitContainerStatuses).ToNot(BeEmpty())
			terminated := current.Status.InitContainerStatuses[0].State.Terminated
			g.Expect(terminated).NotTo(BeNil())
			g.Expect(terminated.ExitCode).NotTo(BeZero())
		}, 2*time.Minute, 3*time.Second).Should(Succeed())
		logs, err := utils.Run(exec.Command("kubectl", "logs", podName, "-n", webhookTestNamespace,
			"-c", constants.ModelValidationInitContainerName))
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(ContainSubstring("failed to fetch production trust root"))
		Expect(logs).To(ContainSubstring("lookup"))
		Expect(logs).To(ContainSubstring("127.0.0.1:53"))
	})

	It("preserves Kubernetes optimistic concurrency on simultaneous CR updates", func() {
		const modelName = "concurrent-update-model"
		DeferCleanup(deleteE2EResource, "modelvalidation", modelName)

		Expect(utils.KubectlApply(modelValidationTemplate, utils.CRTemplateData{
			ModelName: modelName,
			Namespace: webhookTestNamespace,
		})).To(Succeed())

		projectDir, err := utils.GetProjectDir()
		Expect(err).NotTo(HaveOccurred())

		// Status reconciliation bumps resourceVersion, so a single attempt can
		// lose to the controller. Retry until one write wins and the other conflicts.
		Eventually(func(g Gomega) {
			var mv v1alpha1.ModelValidation
			g.Expect(utils.KubectlGetJSON("modelvalidation", modelName, webhookTestNamespace, &mv)).To(Succeed())
			mv.Spec.ImagePullPolicy = corev1.PullNever
			body, marshalErr := json.Marshal(&mv)
			g.Expect(marshalErr).NotTo(HaveOccurred())

			type result struct {
				output string
				err    error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					cmd := exec.Command("kubectl", "replace", "-f", "-")
					cmd.Dir = projectDir
					cmd.Stdin = bytes.NewReader(body)
					output, runErr := cmd.CombinedOutput()
					results <- result{output: string(output), err: runErr}
				}()
			}
			close(start)
			wg.Wait()
			close(results)

			var successes, conflicts int
			for update := range results {
				if update.err == nil {
					successes++
				} else if strings.Contains(update.output, "the object has been modified") {
					conflicts++
				}
			}
			g.Expect(successes).To(Equal(1))
			g.Expect(conflicts).To(Equal(1))
		}, 30*time.Second, time.Second).Should(Succeed())
	})
})

func webhookEndpointAddresses() ([]string, error) {
	output, err := utils.Run(exec.Command("kubectl", "get", "endpointslice",
		"-l", "kubernetes.io/service-name=model-validation-webhook",
		"-n", operatorNamespace, "--request-timeout=15s", "-o", "json"))
	if err != nil {
		return nil, err
	}
	var slices discoveryv1.EndpointSliceList
	if err := json.Unmarshal([]byte(output), &slices); err != nil {
		return nil, err
	}
	var addresses []string
	for _, slice := range slices.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
				addresses = append(addresses, endpoint.Addresses...)
			}
		}
	}
	return addresses, nil
}
