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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/sigstore/model-validation-operator/internal/constants"
	utils "github.com/sigstore/model-validation-operator/test/utils"
)

// --ignore-not-found suppresses only the API's NotFound result. Authentication,
// transport and authorization errors remain errors, not successful deletion.
func getResourceIfPresent(resource, name, namespace string) (string, error) {
	args := []string{"get", resource, name, "--ignore-not-found=true", "--request-timeout=30s", "-o", "json"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	output, err := utils.Run(exec.CommandContext(ctx, "kubectl", args...))
	return strings.TrimSpace(output), err
}

type cleanupTarget struct {
	resource, name, namespace string
}

// Do not fail fast during recovery. Every exact owned target gets one bounded
// attempt, in the specified order; callers assert the aggregate result once.
func cleanupAllResources(targets []cleanupTarget, remove func(string, string, string) error) error {
	var failures []error
	for _, target := range targets {
		if target.name == "" {
			failures = append(failures, fmt.Errorf("refusing unnamed %s cleanup", target.resource))
			continue
		}
		if err := remove(target.resource, target.name, target.namespace); err != nil {
			failures = append(failures, fmt.Errorf("cleaning up %s %s/%s: %w",
				target.resource, target.namespace, target.name, err))
		}
	}
	return errors.Join(failures...)
}

func subscriptionCSVNames(output string) ([]string, error) {
	var subscription unstructured.Unstructured
	if err := json.Unmarshal([]byte(output), &subscription); err != nil {
		return nil, fmt.Errorf("decoding cleanup Subscription: %w", err)
	}
	var names []string
	var failures []error
	for _, field := range []string{"installedCSV", "currentCSV"} {
		name, _, err := unstructured.NestedString(subscription.Object, "status", field)
		if err != nil {
			failures = append(failures, fmt.Errorf("reading cleanup Subscription status.%s: %w", field, err))
		} else if name != "" {
			names = append(names, name)
		}
	}
	return names, errors.Join(failures...)
}

func ensureResourceAbsent(resource, name, namespace string) error {
	output, err := getResourceIfPresent(resource, name, namespace)
	if err != nil {
		return err
	}
	if output != "" {
		return fmt.Errorf("refusing to overwrite pre-existing %s %s/%s", resource, namespace, name)
	}
	return nil
}

// AllNamespaces operators create informational CSV copies in other namespaces.
// They are not installations owned by this namespace and must not be deleted.
// Still refuse a copied MVO: its source installation would conflict with ours.
func ensureNoConflictingCSVs(csvs []unstructured.Unstructured, namespace, packageName string) error {
	for _, csv := range csvs {
		source := csv.GetLabels()["olm.copiedFrom"]
		reason, _, err := unstructured.NestedString(csv.Object, "status", "reason")
		if err != nil {
			return fmt.Errorf("cannot classify CSV %s: %w", csv.GetName(), err)
		}
		if source == "" || source == namespace || csv.GetNamespace() != namespace || reason != "Copied" {
			return fmt.Errorf("refusing existing CSV %s/%s: not a confirmed copy from another namespace",
				csv.GetNamespace(), csv.GetName())
		}
		if strings.HasPrefix(csv.GetName(), packageName+".") {
			return fmt.Errorf("MVO CSV %s is copied from %s; remove the conflicting MVO installation first",
				csv.GetName(), source)
		}
		owned, _, err := unstructured.NestedSlice(csv.Object, "spec", "customresourcedefinitions", "owned")
		if err != nil {
			return fmt.Errorf("cannot inspect CRD ownership of CSV %s: %w", csv.GetName(), err)
		}
		for _, item := range owned {
			crd, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("cannot inspect malformed CRD ownership of CSV %s", csv.GetName())
			}
			if crd["name"] == "modelvalidations.ml.sigstore.dev" {
				return fmt.Errorf("CSV %s copied from %s already owns the ModelValidation API", csv.GetName(), source)
			}
		}
	}
	return nil
}

func isPodReady(pod corev1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func controllerRolledOut(expectedImage string) error {
	var deployment appsv1.Deployment
	err := utils.KubectlGetJSON("deployment", "model-validation-controller-manager", operatorNamespace, &deployment)
	if err != nil {
		return err
	}
	return validateControllerRollout(deployment, expectedImage)
}

func validateControllerRollout(deployment appsv1.Deployment, expectedImage string) error {
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < 1 {
		return fmt.Errorf("controller deployment must have at least one replica")
	}
	desired := *deployment.Spec.Replicas
	status := deployment.Status
	if status.ObservedGeneration < deployment.Generation || status.UpdatedReplicas != desired ||
		status.Replicas != desired || status.ReadyReplicas != desired ||
		status.AvailableReplicas != desired || status.UnavailableReplicas != 0 {
		return fmt.Errorf("controller rollout incomplete: generation %d, observed %d, desired %d, status %+v",
			deployment.Generation, status.ObservedGeneration, desired, status)
	}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "manager" {
			if expectedImage != "" && container.Image != expectedImage {
				return fmt.Errorf("manager image %q does not match expected %q", container.Image, expectedImage)
			}
			return nil
		}
	}
	return fmt.Errorf("manager container missing from controller deployment")
}

func jsonMergePatch(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	} // Callers supply only maps of string literals.
	return string(data)
}

func olmProbeData(name, modelName string) utils.PodTemplateData {
	data := utils.DefaultPodData(name, webhookTestNamespace, modelName, "olm-upgrade")
	if !upgradeSettings.validateModels {
		// Generic catalog CI has no seeded fixtures. Test admission without
		// hostPath/SCC prerequisites, and do not call this validation success.
		data.Volumes = nil
		data.VolumeMounts = nil
	}
	return data
}

func expectModelVerified(podName string) {
	By("requiring successful cryptographic verification using the prepared signed PublicKey fixtures")
	Eventually(func(g Gomega) {
		var pod corev1.Pod
		g.Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &pod)).To(Succeed())
		var verified bool
		for _, status := range pod.Status.InitContainerStatuses {
			if status.Name == constants.ModelValidationInitContainerName && status.State.Terminated != nil {
				g.Expect(status.State.Terminated.ExitCode).To(BeZero(), "validation agent must verify the signed model")
				verified = true
			}
		}
		g.Expect(verified).To(BeTrue(), "validation agent must complete")
		g.Expect(isPodReady(pod)).To(BeTrue())
	}, 5*time.Minute, 5*time.Second).Should(Succeed())
	logs, err := utils.Run(exec.Command("kubectl", "logs", podName, "-n", webhookTestNamespace,
		"-c", constants.ModelValidationInitContainerName))
	Expect(err).NotTo(HaveOccurred())
	Expect(logs).To(ContainSubstring("Verification succeeded"))
}
