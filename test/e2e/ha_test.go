//go:build ha

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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/sigstore/model-validation-operator/api/v1alpha1"
	"github.com/sigstore/model-validation-operator/internal/constants"
	utils "github.com/sigstore/model-validation-operator/test/utils"
)

const (
	haDeploymentName = "model-validation-controller-manager"
	haModelName      = "ha-validation-model"
	haPDBName        = "controller-manager"
)

func haManagerPods() ([]corev1.Pod, error) {
	var deployment appsv1.Deployment
	if err := utils.KubectlGetJSON("deployment", haDeploymentName, operatorNamespace, &deployment); err != nil {
		return nil, err
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	output, err := utils.Run(exec.Command("kubectl", "get", "pods", "-n", operatorNamespace,
		"-l", selector.String(), "--request-timeout=15s", "-o", "json"))
	if err != nil {
		return nil, err
	}
	var pods corev1.PodList
	err = json.Unmarshal([]byte(output), &pods)
	return pods.Items, err
}

func haPodReady(pod corev1.Pod) bool {
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

// A completed Deployment rollout can leave the previous Pod's lease identity
// behind until its lease expires. Require a live, Ready manager and re-read the
// lease after looking it up before selecting a failover victim.
func haCurrentReadyLeader(leaseName string) (string, corev1.Pod, error) {
	var lease coordinationv1.Lease
	if err := utils.KubectlGetJSON("lease", leaseName, operatorNamespace, &lease); err != nil {
		return "", corev1.Pod{}, err
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return "", corev1.Pod{}, fmt.Errorf("leader lease has no holder")
	}
	holder := *lease.Spec.HolderIdentity
	pods, err := haManagerPods()
	if err != nil {
		return "", corev1.Pod{}, err
	}
	for _, pod := range pods {
		if pod.Name != strings.SplitN(holder, "_", 2)[0] || !haPodReady(pod) {
			continue
		}
		var latest coordinationv1.Lease
		if err := utils.KubectlGetJSON("lease", leaseName, operatorNamespace, &latest); err != nil {
			return "", corev1.Pod{}, err
		}
		if latest.UID != lease.UID || latest.Spec.HolderIdentity == nil || *latest.Spec.HolderIdentity != holder {
			return "", corev1.Pod{}, fmt.Errorf("leader changed while selecting the failover victim")
		}
		return holder, pod, nil
	}
	return "", corev1.Pod{}, fmt.Errorf("lease holder %s is not a live Ready manager", holder)
}

// Include observedGeneration, exact replica counts and Pod UIDs: old status or
// terminating Pods must not make a rolling restart appear complete.
func haRolloutComplete(generation int64, oldUIDs map[types.UID]bool) error {
	var deployment appsv1.Deployment
	if err := utils.KubectlGetJSON("deployment", haDeploymentName, operatorNamespace, &deployment); err != nil {
		return err
	}
	want := *deployment.Spec.Replicas
	status := deployment.Status
	if deployment.Generation < generation || status.ObservedGeneration < deployment.Generation ||
		status.Replicas != want || status.UpdatedReplicas != want || status.ReadyReplicas != want ||
		status.AvailableReplicas != want || status.UnavailableReplicas != 0 {
		return fmt.Errorf("deployment generation %d observed %d is not fully rolled out: %+v",
			deployment.Generation, status.ObservedGeneration, status)
	}
	pods, err := haManagerPods()
	if err != nil {
		return err
	}
	if len(pods) != int(want) {
		return fmt.Errorf("expected %d manager Pods including terminating Pods, got %d", want, len(pods))
	}
	for _, pod := range pods {
		if oldUIDs[pod.UID] || !haPodReady(pod) {
			return fmt.Errorf("manager Pod %s is old or not ready", pod.Name)
		}
	}
	return nil
}

func waitForHAReplicas() {
	Eventually(func() error { return haRolloutComplete(0, nil) }, 3*time.Minute, 2*time.Second).Should(Succeed())
}

func expectHAManagerSpread(pods []corev1.Pod) {
	Expect(pods).To(HaveLen(2))
	Expect(pods[0].Spec.NodeName).NotTo(BeEmpty())
	Expect(pods[1].Spec.NodeName).NotTo(BeEmpty())
	Expect(pods[0].Spec.NodeName).NotTo(Equal(pods[1].Spec.NodeName),
		"healthy manager replicas must occupy different nodes after a completed rollout")
}

// Server-side dry run invokes the real admission webhook without creating
// unlimited workloads during a slow drain/rollout.
func haAdmissionProbe() error {
	pod := corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "ha-admission-probe-", Namespace: webhookTestNamespace,
			Labels: map[string]string{constants.ModelValidationLabel: haModelName},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "workload", Image: "busybox:latest"}}},
	}
	body, err := json.Marshal(pod)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "create", "--dry-run=server", "--request-timeout=15s",
		"-f", "-", "-o", "json")
	cmd.Stdin = bytes.NewReader(body)
	// API admission warnings go to stderr and are not part of the Pod JSON.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("admission probe failed: %w: %s", err, stderr.String())
	}
	if err := json.Unmarshal(output, &pod); err != nil {
		return err
	}
	if !utils.HasValidationContainer(&pod) {
		return fmt.Errorf("admission succeeded without injecting the validation container")
	}
	return nil
}

func probeDuringHAChange(change func(), complete func() error) {
	Expect(haAdmissionProbe()).To(Succeed(), "pre-disruption admission probe")
	type probeResult struct {
		count    int
		failures []string
	}
	stop := make(chan struct{})
	done := make(chan probeResult, 1)
	go func() {
		result := probeResult{}
		for {
			select {
			case <-stop:
				done <- result
				return
			default:
			}
			result.count++
			if err := haAdmissionProbe(); err != nil {
				result.failures = append(result.failures, fmt.Sprintf("%s: %v", time.Now().UTC().Format(time.RFC3339Nano), err))
			}
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-stop:
				timer.Stop()
				done <- result
				return
			case <-timer.C:
			}
		}
	}()
	// Also stop traffic if an assertion in change/completion fails.
	defer func() {
		close(stop)
		result := <-done
		AddReportEntry("admission probes during disruption", result.count)
		Expect(result.count).To(BeNumerically(">", 0))
		Expect(result.failures).To(BeEmpty(), "admission failures must not be retried away")
	}()
	change()
	Eventually(complete, 4*time.Minute, 2*time.Second).Should(Succeed())
	Expect(haAdmissionProbe()).To(Succeed(), "post-recovery admission probe")
}

func haEvict(pod corev1.Pod) error {
	eviction := policyv1.Eviction{
		TypeMeta:   metav1.TypeMeta{APIVersion: "policy/v1", Kind: "Eviction"},
		ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: operatorNamespace},
		DeleteOptions: &metav1.DeleteOptions{
			DryRun: []string{metav1.DryRunAll}, Preconditions: &metav1.Preconditions{UID: &pod.UID},
		},
	}
	body, err := json.Marshal(eviction)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/eviction", operatorNamespace, pod.Name)
	cmd := exec.Command("kubectl", "create", "--raw", path, "--request-timeout=15s", "-f", "-")
	cmd.Stdin = bytes.NewReader(body)
	_, err = utils.Run(cmd)
	return err
}

func expectHABudget(replicas int32, allowed int32) {
	Eventually(func(g Gomega) {
		var pdb policyv1.PodDisruptionBudget
		g.Expect(utils.KubectlGetJSON("poddisruptionbudget", haPDBName, operatorNamespace, &pdb)).To(Succeed())
		g.Expect(pdb.Spec.MinAvailable).NotTo(BeNil())
		g.Expect(pdb.Spec.MinAvailable.IntValue()).To(Equal(1))
		g.Expect(pdb.Status.ObservedGeneration).To(BeNumerically(">=", pdb.Generation))
		g.Expect(pdb.Status.ExpectedPods).To(Equal(replicas))
		g.Expect(pdb.Status.CurrentHealthy).To(Equal(replicas))
		g.Expect(pdb.Status.DesiredHealthy).To(Equal(int32(1)))
		g.Expect(pdb.Status.DisruptionsAllowed).To(Equal(allowed))
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		g.Expect(err).NotTo(HaveOccurred())
		pods, err := haManagerPods()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(pods).To(HaveLen(int(replicas)))
		for _, pod := range pods {
			g.Expect(selector.Matches(labels.Set(pod.Labels))).To(BeTrue(), "PDB does not select %s", pod.Name)
		}
	}, time.Minute, 2*time.Second).Should(Succeed())
}

func scaleHAManagers(replicas int32) {
	_, err := utils.Run(exec.Command("kubectl", "scale", "deployment", haDeploymentName,
		"-n", operatorNamespace, fmt.Sprintf("--replicas=%d", replicas), "--request-timeout=15s"))
	Expect(err).NotTo(HaveOccurred())
	waitForHAReplicas()
}

var _ = Describe("High availability", Ordered, Serial, func() {
	BeforeAll(func() {
		Expect(utils.KubectlApply(modelValidationTemplate, utils.CRTemplateData{
			ModelName: haModelName, Namespace: webhookTestNamespace,
		})).To(Succeed())
		waitForHAReplicas()
		pods, err := haManagerPods()
		Expect(err).NotTo(HaveOccurred())
		Expect(pods).To(HaveLen(2), "HA tests require exactly two manager replicas")
	})

	AfterAll(func() {
		_, err := utils.Run(exec.Command("kubectl", "delete", "pods", "-n", webhookTestNamespace,
			"-l", "test-batch=ha", "--ignore-not-found=true", "--timeout=45s"))
		Expect(err).NotTo(HaveOccurred())
		_, err = utils.Run(exec.Command("kubectl", "delete", "modelvalidation", haModelName,
			"-n", webhookTestNamespace, "--ignore-not-found=true", "--timeout=30s"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("enforces a disruption budget and spreads replicas across nodes", func() {
		expectHABudget(2, 1)
		var deployment appsv1.Deployment
		Expect(utils.KubectlGetJSON("deployment", haDeploymentName, operatorNamespace, &deployment)).To(Succeed())
		Expect(deployment.Spec.Template.Spec.Affinity).NotTo(BeNil())
		Expect(deployment.Spec.Template.Spec.Affinity.PodAntiAffinity).NotTo(BeNil())
		Expect(deployment.Spec.Template.Spec.Affinity.PodAntiAffinity.
			PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElement(
			HaveField("PodAffinityTerm", And(
				HaveField("TopologyKey", "kubernetes.io/hostname"),
				HaveField("MatchLabelKeys", ContainElement("pod-template-hash")),
			)),
		), "anti-affinity must spread each revision independently during rolling updates")
		pods, err := haManagerPods()
		Expect(err).NotTo(HaveOccurred())
		expectHAManagerSpread(pods)
		Expect(haEvict(pods[0])).To(Succeed(), "a healthy two-replica budget should allow an eviction")

		By("exhausting the budget and proving the Eviction API rejects disruption of the last healthy replica")
		DeferCleanup(scaleHAManagers, int32(2))
		scaleHAManagers(1)
		expectHABudget(1, 0)
		pods, err = haManagerPods()
		Expect(err).NotTo(HaveOccurred())
		err = haEvict(pods[0])
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("disruption budget"))
		scaleHAManagers(2)
		expectHABudget(2, 1)
	})

	It("keeps the webhook available when one replica is killed", func() {
		pods, err := haManagerPods()
		Expect(err).NotTo(HaveOccurred())
		victim := pods[0]
		probeDuringHAChange(func() {
			_, deleteErr := utils.Run(exec.Command("kubectl", "delete", "pod", victim.Name,
				"-n", operatorNamespace, "--wait=false", "--request-timeout=15s"))
			Expect(deleteErr).NotTo(HaveOccurred())
		}, func() error { return haRolloutComplete(0, map[types.UID]bool{victim.UID: true}) })
	})

	It("allows a voluntary node drain while honoring the PDB", func() {
		Expect(os.Getenv("E2E_ALLOW_NODE_DRAIN")).To(Equal("true"),
			"node drain is disruptive: explicitly set E2E_ALLOW_NODE_DRAIN=true on an isolated test cluster")
		selectorText := os.Getenv("E2E_DRAIN_NODE_SELECTOR")
		Expect(selectorText).NotTo(BeEmpty(), "set E2E_DRAIN_NODE_SELECTOR to explicitly approved test workers")
		selector, err := labels.Parse(selectorText)
		Expect(err).NotTo(HaveOccurred())
		Expect(selector.Empty()).To(BeFalse(), "an empty node selector would authorize all nodes")
		pods, err := haManagerPods()
		Expect(err).NotTo(HaveOccurred())
		var target *corev1.Node
		for _, pod := range pods {
			var node corev1.Node
			Expect(utils.KubectlGetJSON("node", pod.Spec.NodeName, "", &node)).To(Succeed())
			_, controlPlane := node.Labels["node-role.kubernetes.io/control-plane"]
			_, master := node.Labels["node-role.kubernetes.io/master"]
			if selector.Matches(labels.Set(node.Labels)) && !controlPlane && !master {
				target = &node
				break
			}
		}
		Expect(target).NotTo(BeNil(), "no manager is on an explicitly approved non-control-plane node")
		DeferCleanup(func() {
			action := "uncordon"
			if target.Spec.Unschedulable {
				action = "cordon"
			}
			_, restoreErr := utils.Run(exec.Command("kubectl", action, target.Name, "--request-timeout=15s"))
			Expect(restoreErr).NotTo(HaveOccurred(), "restore original scheduling state of %s", target.Name)
			waitForHAReplicas()
		})
		expectHABudget(2, 1)
		probeDuringHAChange(func() {
			_, drainErr := utils.Run(exec.Command("kubectl", "drain", target.Name, "--ignore-daemonsets",
				"--pod-selector=app.kubernetes.io/name=model-validation-operator,control-plane=controller-manager",
				"--delete-emptydir-data", "--timeout=180s"))
			Expect(drainErr).NotTo(HaveOccurred())
		}, func() error { return haRolloutComplete(0, nil) })
	})

	It("retains webhook availability throughout a rolling update", func() {
		pods, err := haManagerPods()
		Expect(err).NotTo(HaveOccurred())
		oldUIDs := make(map[types.UID]bool, len(pods))
		for _, pod := range pods {
			oldUIDs[pod.UID] = true
		}
		var before appsv1.Deployment
		Expect(utils.KubectlGetJSON("deployment", haDeploymentName, operatorNamespace, &before)).To(Succeed())
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		probeDuringHAChange(func() {
			Expect(utils.KubectlPatch("deployment", haDeploymentName, operatorNamespace, "merge",
				fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"ha-test/restartedAt":%q}}}}}`, stamp))).To(Succeed())
		}, func() error { return haRolloutComplete(before.Generation+1, oldUIDs) })
		var after appsv1.Deployment
		Expect(utils.KubectlGetJSON("deployment", haDeploymentName, operatorNamespace, &after)).To(Succeed())
		const revision = "deployment.kubernetes.io/revision"
		Expect(after.Annotations[revision]).NotTo(BeEmpty())
		Expect(after.Annotations[revision]).NotTo(Equal(before.Annotations[revision]))
		pods, err = haManagerPods()
		Expect(err).NotTo(HaveOccurred())
		expectHAManagerSpread(pods)
	})

	It("transfers the leader lease and resumes reconciliation and finalizer cleanup", func() {
		const leaseName = "2b2639c9.sigstore.dev"
		var oldHolder string
		var victim corev1.Pod
		Eventually(func() error {
			var getErr error
			oldHolder, victim, getErr = haCurrentReadyLeader(leaseName)
			return getErr
		}, 90*time.Second, 2*time.Second).Should(Succeed(), "wait for leadership to settle after prior disruptions")

		// Raw DELETE accepts Kubernetes DeleteOptions, including a UID precondition.
		// Never mistake a stale name / NotFound for successfully killing a leader.
		deleteOptions := metav1.DeleteOptions{
			TypeMeta:      metav1.TypeMeta{APIVersion: "v1", Kind: "DeleteOptions"},
			Preconditions: &metav1.Preconditions{UID: &victim.UID},
		}
		body, err := json.Marshal(deleteOptions)
		Expect(err).NotTo(HaveOccurred())
		path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", operatorNamespace, victim.Name)
		deleteCmd := exec.Command("kubectl", "delete", "--raw", path, "--request-timeout=15s", "-f", "-")
		deleteCmd.Stdin = bytes.NewReader(body)
		_, err = utils.Run(deleteCmd)
		Expect(err).NotTo(HaveOccurred(), "delete the selected live leader, not a stale lease holder")
		Eventually(func(g Gomega) {
			newHolder, leader, getErr := haCurrentReadyLeader(leaseName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(newHolder).NotTo(Equal(oldHolder))
			g.Expect(leader.UID).NotTo(Equal(victim.UID))
		}, 90*time.Second, 2*time.Second).Should(Succeed())

		const podName = "ha-leader-failover"
		Expect(utils.KubectlApply(podTemplate,
			utils.DefaultPodData(podName, webhookTestNamespace, haModelName, "ha"))).To(Succeed())
		var pod corev1.Pod
		Expect(utils.KubectlGetJSON("pod", podName, webhookTestNamespace, &pod)).To(Succeed())
		Expect(pod.Finalizers).To(ContainElement(constants.ModelValidationFinalizer))
		Eventually(func(g Gomega) {
			var mv v1alpha1.ModelValidation
			g.Expect(utils.KubectlGetJSON("modelvalidation", haModelName, webhookTestNamespace, &mv)).To(Succeed())
			g.Expect(mv.Status.InjectedPodCount).To(Equal(int32(1)))
			g.Expect(mv.Status.InjectedPods).To(ContainElement(And(
				HaveField("Name", podName), HaveField("UID", string(pod.UID)))))
		}, time.Minute, 2*time.Second).Should(Succeed())
		_, err = utils.Run(exec.Command("kubectl", "delete", "pod", podName, "-n", webhookTestNamespace,
			"--wait=false", "--request-timeout=15s"))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			absent, getErr := utils.KubectlResourceAbsent("pod", podName, webhookTestNamespace)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(absent).To(BeTrue(), "the new leader must remove the tracking finalizer")
			var mv v1alpha1.ModelValidation
			g.Expect(utils.KubectlGetJSON("modelvalidation", haModelName, webhookTestNamespace, &mv)).To(Succeed())
			g.Expect(mv.Status.InjectedPodCount).To(BeZero())
			g.Expect(mv.Status.InjectedPods).To(BeEmpty())
		}, time.Minute, 2*time.Second).Should(Succeed())
		waitForHAReplicas()
	})
})
