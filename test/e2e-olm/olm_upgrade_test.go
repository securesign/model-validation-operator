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
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/sigstore/model-validation-operator/internal/constants"
	utils "github.com/sigstore/model-validation-operator/test/utils"
)

func cleanupResource(resource, name, namespace string) error {
	if name == "" {
		return fmt.Errorf("refusing cleanup without an explicit %s name", resource)
	}
	args := []string{"delete", resource, name}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "--timeout=60s", "--request-timeout=30s", "--ignore-not-found=true")
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	_, err := utils.Run(exec.CommandContext(ctx, "kubectl", args...))
	return err
}

var _ = Describe("OLM Upgrade", Ordered, func() {
	var (
		baseCSV     string
		upgradeCSV  string
		preservedCR unstructured.Unstructured
		createdCR   bool
	)

	const (
		catalogName       = "model-validation-test-catalog"
		operatorGroupName = "model-validation-og"
		subscriptionName  = "model-validation-operator"
		testModelName     = "olm-upgrade-test-model"
	)

	BeforeAll(func() {
		_, _ = fmt.Fprintf(GinkgoWriter, "OLM Upgrade Test Configuration:\n")
		_, _ = fmt.Fprintf(GinkgoWriter, "  Base catalog: %s\n", upgradeSettings.baseCatalog)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Target catalog: %s\n", upgradeSettings.targetCatalog)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Channels: %s -> %s\n", upgradeSettings.baseChannel, upgradeSettings.targetChannel)
		_, _ = fmt.Fprintf(GinkgoWriter, "  Package: %s; GA certification: %t; real model verification: %t\n",
			upgradeSettings.packageName, upgradeSettings.gaCertification, upgradeSettings.validateModels)

		By("refusing to overwrite a standalone or existing OLM installation")
		Expect(ensureResourceAbsent("deployment", "model-validation-controller-manager", operatorNamespace)).To(Succeed())
		for _, resource := range []string{"subscriptions", "operatorgroups"} {
			var list unstructured.UnstructuredList
			Expect(utils.KubectlGetJSON(resource, "", operatorNamespace, &list)).To(Succeed())
			Expect(list.Items).To(BeEmpty(), "Use a clean operator namespace; refusing to replace existing %s", resource)
		}
		var csvs unstructured.UnstructuredList
		Expect(utils.KubectlGetJSON("clusterserviceversions", "", operatorNamespace, &csvs)).To(Succeed())
		Expect(ensureNoConflictingCSVs(csvs.Items, operatorNamespace, upgradeSettings.packageName)).To(Succeed())
		Expect(ensureResourceAbsent("catalogsource", catalogName, upgradeSettings.catalogNamespace)).To(Succeed())
		for _, name := range []string{"olm-test-pod-initial", "olm-test-pod-upgrade"} {
			Expect(ensureResourceAbsent("pod", name, webhookTestNamespace)).To(Succeed())
		}

		DeferCleanup(func() {
			By("cleaning up OLM test resources")
			var cleanupErrors []error
			// Delete only resources this suite owns. Capture a partially installed
			// CSV before deleting the Subscription, even if the install spec failed.
			csvNames := map[string]bool{baseCSV: true, upgradeCSV: true}
			output, err := getResourceIfPresent("subscription", subscriptionName, operatorNamespace)
			if err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("discovering owned CSVs: %w", err))
			} else if output != "" {
				names, err := subscriptionCSVNames(output)
				cleanupErrors = append(cleanupErrors, err)
				for _, name := range names {
					csvNames[name] = true
				}
			}
			var targets []cleanupTarget
			for _, name := range []string{"olm-test-pod-initial", "olm-test-pod-upgrade"} {
				targets = append(targets, cleanupTarget{"pod", name, webhookTestNamespace})
			}
			if createdCR {
				targets = append(targets, cleanupTarget{"modelvalidation", testModelName, webhookTestNamespace})
			}
			// Attempt all workload cleanup while the operator still exists. An
			// individual failure must not prevent attempts on remaining owned
			// resources, and is reported after all bounded deletes have run.
			targets = append(targets, cleanupTarget{"subscription", subscriptionName, operatorNamespace})
			delete(csvNames, "")
			var sortedCSVs []string
			for name := range csvNames {
				sortedCSVs = append(sortedCSVs, name)
			}
			sort.Strings(sortedCSVs)
			for _, name := range sortedCSVs {
				targets = append(targets, cleanupTarget{"csv", name, operatorNamespace})
			}
			targets = append(targets,
				cleanupTarget{"operatorgroup", operatorGroupName, operatorNamespace},
				cleanupTarget{"catalogsource", catalogName, upgradeSettings.catalogNamespace})
			cleanupErrors = append(cleanupErrors, cleanupAllResources(targets, cleanupResource))
			Expect(errors.Join(cleanupErrors...)).To(Succeed(), "All owned-resource cleanup attempts must succeed")
		})
	})

	It("should install the initial version via OLM", func() {
		By("creating a CatalogSource with the base catalog image")
		err := utils.KubectlApply(catalogSourceTemplate, utils.CatalogSourceTemplateData{
			Name:      catalogName,
			Namespace: upgradeSettings.catalogNamespace,
			Image:     upgradeSettings.baseCatalog,
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to create CatalogSource")

		By("waiting for CatalogSource to be ready")
		Eventually(func(g Gomega) {
			status, err := utils.KubectlGet("catalogsource", catalogName, upgradeSettings.catalogNamespace,
				"jsonpath={.status.connectionState.lastObservedState}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(status).To(Equal("READY"))
		}, 5*time.Minute, 5*time.Second).Should(Succeed(), "CatalogSource should become READY")

		By("creating an OperatorGroup for AllNamespaces mode")
		err = utils.KubectlApply(operatorGroupTemplate, utils.OperatorGroupTemplateData{
			Name:      operatorGroupName,
			Namespace: operatorNamespace,
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to create OperatorGroup")

		By("creating a Subscription to trigger installation")
		err = utils.KubectlApply(subscriptionTemplate, struct {
			utils.SubscriptionTemplateData
			StartingCSV string
		}{SubscriptionTemplateData: utils.SubscriptionTemplateData{
			Name:                   subscriptionName,
			Namespace:              operatorNamespace,
			Channel:                upgradeSettings.baseChannel,
			PackageName:            upgradeSettings.packageName,
			CatalogSourceName:      catalogName,
			CatalogSourceNamespace: upgradeSettings.catalogNamespace,
		}, StartingCSV: upgradeSettings.expectedBaseCSV})
		Expect(err).NotTo(HaveOccurred(), "Failed to create Subscription")

		By("waiting for subscription to report an installed CSV")
		Eventually(func(g Gomega) {
			csv, err := utils.KubectlGet("subscription", subscriptionName, operatorNamespace,
				"jsonpath={.status.installedCSV}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(csv).NotTo(BeEmpty())
			if upgradeSettings.expectedBaseCSV != "" {
				g.Expect(csv).To(Equal(upgradeSettings.expectedBaseCSV))
			}
			baseCSV = csv
		}, 5*time.Minute, 5*time.Second).Should(Succeed(),
			"Subscription should report an installed CSV")

		_, _ = fmt.Fprintf(GinkgoWriter, "  Installed base CSV: %s\n", baseCSV)

		By(fmt.Sprintf("waiting for base CSV %s to reach Succeeded phase", baseCSV))
		Eventually(func(g Gomega) {
			phase, err := utils.KubectlGet("csv", baseCSV, operatorNamespace,
				"jsonpath={.status.phase}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(phase).To(Equal("Succeeded"))
		}, 5*time.Minute, 5*time.Second).Should(Succeed(),
			"Base CSV should reach Succeeded phase")

		By("waiting for controller deployment to be available")
		Eventually(func() error {
			return controllerRolledOut(upgradeSettings.expectedBaseImage)
		}, 3*time.Minute, 10*time.Second).Should(Succeed(),
			"Controller deployment should be available")

		By("verifying controller pod is running")
		Eventually(func(g Gomega) {
			output, err := utils.KubectlGet("pods", "", operatorNamespace,
				"jsonpath={.items[?(@.metadata.labels.control-plane==\"controller-manager\")].status.phase}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(ContainSubstring("Running"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed(),
			"Controller pod should be running")
	})

	It("should have a functioning webhook after OLM installation", func() {
		By("deploying a ModelValidation CR")
		Expect(ensureResourceAbsent("modelvalidation", testModelName, webhookTestNamespace)).To(Succeed())
		createdCR = true
		err := utils.KubectlApply(modelValidationTemplate, utils.CRTemplateData{
			ModelName: testModelName,
			Namespace: webhookTestNamespace,
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelValidation CR")
		Expect(utils.KubectlGetJSON("modelvalidation", testModelName, webhookTestNamespace, &preservedCR)).To(Succeed())
		Expect(preservedCR.GetUID()).NotTo(BeEmpty())

		By("deploying a test pod with model validation label")
		err = utils.KubectlApply(podTemplate, olmProbeData("olm-test-pod-initial", testModelName))
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy test pod")

		By("verifying the init container is injected by the webhook")
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			err := utils.KubectlGetJSON("pod", "olm-test-pod-initial", webhookTestNamespace, &pod)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(pod.Spec.InitContainers).ToNot(BeEmpty(),
				"Pod should have init containers after webhook injection")
			g.Expect(pod.Spec.InitContainers[0].Name).To(
				Equal(constants.ModelValidationInitContainerName))
		}, 1*time.Minute, 5*time.Second).Should(Succeed(),
			"Init container should be injected")
		if upgradeSettings.validateModels {
			expectModelVerified("olm-test-pod-initial")
		}
	})

	It("should upgrade when the catalog is updated", func() {
		By("capturing the pre-upgrade controller image")
		var preUpgradeImage string
		Eventually(func(g Gomega) {
			img, err := utils.KubectlGet("deployment", "model-validation-controller-manager",
				operatorNamespace,
				"jsonpath={.spec.template.spec.containers[?(@.name==\"manager\")].image}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(img).NotTo(BeEmpty())
			preUpgradeImage = img
		}, 1*time.Minute, 5*time.Second).Should(Succeed())
		_, _ = fmt.Fprintf(GinkgoWriter, "  Pre-upgrade image: %s\n", preUpgradeImage)

		By(fmt.Sprintf("patching CatalogSource to use target catalog: %s", upgradeSettings.targetCatalog))
		err := utils.KubectlPatch("catalogsource", catalogName, upgradeSettings.catalogNamespace, "merge",
			jsonMergePatch(map[string]any{"spec": map[string]string{"image": upgradeSettings.targetCatalog}}))
		Expect(err).NotTo(HaveOccurred(), "Failed to patch CatalogSource")
		if upgradeSettings.targetChannel != upgradeSettings.baseChannel {
			Expect(utils.KubectlPatch("subscription", subscriptionName, operatorNamespace, "merge",
				jsonMergePatch(map[string]any{"spec": map[string]string{"channel": upgradeSettings.targetChannel}}))).To(Succeed())
		}

		By("waiting for CatalogSource to reconnect with updated catalog")
		Eventually(func(g Gomega) {
			status, err := utils.KubectlGet("catalogsource", catalogName, upgradeSettings.catalogNamespace,
				"jsonpath={.status.connectionState.lastObservedState}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(status).To(Equal("READY"))
		}, 3*time.Minute, 5*time.Second).Should(Succeed(),
			"CatalogSource should reconnect after update")

		By("waiting for subscription to report a new installed CSV")
		Eventually(func(g Gomega) {
			csv, err := utils.KubectlGet("subscription", subscriptionName, operatorNamespace,
				"jsonpath={.status.installedCSV}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(csv).NotTo(BeEmpty())
			g.Expect(csv).NotTo(Equal(baseCSV), "installed CSV should change after upgrade")
			if upgradeSettings.expectedTargetCSV != "" {
				g.Expect(csv).To(Equal(upgradeSettings.expectedTargetCSV))
			}
			upgradeCSV = csv
		}, 5*time.Minute, 10*time.Second).Should(Succeed(),
			"Subscription should report a new installed CSV after upgrade")

		_, _ = fmt.Fprintf(GinkgoWriter, "  Upgraded CSV: %s (was: %s)\n", upgradeCSV, baseCSV)

		By(fmt.Sprintf("waiting for upgrade CSV %s to reach Succeeded phase", upgradeCSV))
		Eventually(func(g Gomega) {
			phase, err := utils.KubectlGet("csv", upgradeCSV, operatorNamespace,
				"jsonpath={.status.phase}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(phase).To(Equal("Succeeded"))
		}, 5*time.Minute, 10*time.Second).Should(Succeed(),
			"Upgrade CSV should reach Succeeded phase")

		By("waiting for controller deployment to be available after upgrade")
		Eventually(func() error {
			return controllerRolledOut(upgradeSettings.expectedTargetImage)
		}, 3*time.Minute, 10*time.Second).Should(Succeed(),
			"Controller deployment should be available after upgrade")

		By("verifying the controller image changed after upgrade")
		Eventually(func(g Gomega) {
			img, err := utils.KubectlGet("deployment", "model-validation-controller-manager",
				operatorNamespace,
				"jsonpath={.spec.template.spec.containers[?(@.name==\"manager\")].image}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(img).NotTo(BeEmpty())
			g.Expect(img).NotTo(Equal(preUpgradeImage),
				"Controller image should change after upgrade")
		}, 2*time.Minute, 5*time.Second).Should(Succeed(),
			"Controller deployment image should be updated")
	})

	It("should maintain functionality after upgrade", func() {
		By("verifying existing ModelValidation CR survived the upgrade")
		var upgradedCR unstructured.Unstructured
		Expect(utils.KubectlGetJSON("modelvalidation", testModelName, webhookTestNamespace, &upgradedCR)).To(Succeed())
		Expect(upgradedCR.GetUID()).To(Equal(preservedCR.GetUID()), "CR must not be deleted and recreated")
		Expect(upgradedCR.Object["spec"]).To(Equal(preservedCR.Object["spec"]), "CR spec must be preserved")

		By("verifying controller pod is running after upgrade")
		Eventually(func(g Gomega) {
			output, err := utils.KubectlGet("pods", "", operatorNamespace,
				"jsonpath={.items[?(@.metadata.labels.control-plane==\"controller-manager\")].status.phase}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(ContainSubstring("Running"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed(),
			"Controller pod should be running after upgrade")

		By("deploying a new test pod to verify webhook still works after upgrade")
		Eventually(func() error {
			return utils.KubectlApply(podTemplate, olmProbeData("olm-test-pod-upgrade", testModelName))
		}, 2*time.Minute, 5*time.Second).Should(Succeed(),
			"Post-upgrade test pod should be accepted by webhook")

		By("verifying the init container is injected on the new pod")
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			err := utils.KubectlGetJSON("pod", "olm-test-pod-upgrade", webhookTestNamespace, &pod)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(pod.Spec.InitContainers).ToNot(BeEmpty(),
				"Post-upgrade pod should have init containers")
			g.Expect(pod.Spec.InitContainers[0].Name).To(
				Equal(constants.ModelValidationInitContainerName))
		}, 1*time.Minute, 5*time.Second).Should(Succeed(),
			"Init container should be injected after upgrade")
		if upgradeSettings.validateModels {
			expectModelVerified("olm-test-pod-upgrade")
		}
	})

	It("should allow CR deletion and subsequent Pod cleanup after upgrade", func() {
		By("deleting the ModelValidation CR")
		Expect(cleanupResource("modelvalidation", testModelName, webhookTestNamespace)).To(Succeed())

		By("verifying the ModelValidation CR is deleted")
		Eventually(func(g Gomega) {
			output, err := getResourceIfPresent("modelvalidation", testModelName, webhookTestNamespace)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(BeEmpty())
		}, 1*time.Minute, 5*time.Second).Should(Succeed(),
			"ModelValidation CR should be deleted after upgrade")

		By("deleting both old and new Pods after their ModelValidation CR is gone")
		podTargets := []cleanupTarget{
			{"pod", "olm-test-pod-initial", webhookTestNamespace},
			{"pod", "olm-test-pod-upgrade", webhookTestNamespace},
		}
		Expect(cleanupAllResources(podTargets, cleanupResource)).To(Succeed(),
			"Controller finalizer cleanup must work after the referenced CR has been deleted")
		Eventually(func(g Gomega) {
			for _, target := range podTargets {
				output, err := getResourceIfPresent(target.resource, target.name, target.namespace)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(BeEmpty(), "Pod %s must not remain stuck terminating", target.name)
			}
		}, time.Minute, 5*time.Second).Should(Succeed())
	})

})
