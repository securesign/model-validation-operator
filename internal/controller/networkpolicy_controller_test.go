package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	"github.com/sigstore/model-validation-operator/internal/testutil"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("NetworkPolicyReconciler", func() {
	var (
		ctx        context.Context
		reconciler *NetworkPolicyReconciler
		namespace  string
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = "test-operator-ns"
	})

	Context("when the NetworkPolicy does not exist", func() {
		It("should create the NetworkPolicy with the correct spec", func() {
			fakeClient := testutil.SetupFakeClientWithObjects()

			reconciler = &NetworkPolicyReconciler{
				Client:    fakeClient,
				Scheme:    runtime.NewScheme(),
				Namespace: namespace,
			}

			req := testutil.CreateReconcileRequest(namespace, networkPolicyName)
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			np := &networkingv1.NetworkPolicy{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: namespace}, np)
			Expect(err).NotTo(HaveOccurred())

			Expect(np.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "model-validation-operator"))
			Expect(np.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "model-validation-operator"))

			Expect(np.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue("control-plane", "controller-manager"))
			Expect(np.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue("app.kubernetes.io/name", "model-validation-operator"))

			Expect(np.Spec.PolicyTypes).To(ConsistOf(
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			))

			Expect(np.Spec.Ingress).To(HaveLen(1))
			expectPort(np.Spec.Ingress[0].Ports, 9443, corev1.ProtocolTCP)
			expectPort(np.Spec.Ingress[0].Ports, 8081, corev1.ProtocolTCP)

			Expect(np.Spec.Egress).To(HaveLen(2))
			expectPort(np.Spec.Egress[0].Ports, 53, corev1.ProtocolTCP)
			expectPort(np.Spec.Egress[0].Ports, 53, corev1.ProtocolUDP)
			expectPort(np.Spec.Egress[1].Ports, 443, corev1.ProtocolTCP)
			expectPort(np.Spec.Egress[1].Ports, 6443, corev1.ProtocolTCP)
		})
	})

	Context("when the NetworkPolicy already exists with wrong spec", func() {
		It("should update the NetworkPolicy to the desired spec", func() {
			existingNP := &networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      networkPolicyName,
					Namespace: namespace,
				},
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{},
					PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				},
			}

			fakeClient := testutil.SetupFakeClientWithObjects(existingNP)

			reconciler = &NetworkPolicyReconciler{
				Client:    fakeClient,
				Scheme:    runtime.NewScheme(),
				Namespace: namespace,
			}

			req := testutil.CreateReconcileRequest(namespace, networkPolicyName)
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			np := &networkingv1.NetworkPolicy{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: namespace}, np)
			Expect(err).NotTo(HaveOccurred())

			Expect(np.Spec.PolicyTypes).To(ConsistOf(
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			))
			Expect(np.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue("control-plane", "controller-manager"))
			Expect(np.Spec.Ingress).To(HaveLen(1))
			Expect(np.Spec.Egress).To(HaveLen(2))
		})
	})

	Context("when the NetworkPolicy already has the correct spec", func() {
		It("should be idempotent and not error", func() {
			fakeClient := testutil.SetupFakeClientWithObjects()

			reconciler = &NetworkPolicyReconciler{
				Client:    fakeClient,
				Scheme:    runtime.NewScheme(),
				Namespace: namespace,
			}

			req := testutil.CreateReconcileRequest(namespace, networkPolicyName)

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			result, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			np := &networkingv1.NetworkPolicy{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: namespace}, np)
			Expect(err).NotTo(HaveOccurred())
			Expect(np.Spec.Ingress).To(HaveLen(1))
			Expect(np.Spec.Egress).To(HaveLen(2))
		})
	})

	Context("when the NetworkPolicy is deleted externally", func() {
		It("should re-create the NetworkPolicy", func() {
			fakeClient := testutil.SetupFakeClientWithObjects()

			reconciler = &NetworkPolicyReconciler{
				Client:    fakeClient,
				Scheme:    runtime.NewScheme(),
				Namespace: namespace,
			}

			req := testutil.CreateReconcileRequest(namespace, networkPolicyName)

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			np := &networkingv1.NetworkPolicy{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: namespace}, np)
			Expect(err).NotTo(HaveOccurred())
			err = fakeClient.Delete(ctx, np)
			Expect(err).NotTo(HaveOccurred())

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			err = fakeClient.Get(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: namespace}, np)
			Expect(err).NotTo(HaveOccurred())
			Expect(np.Spec.PolicyTypes).To(ConsistOf(
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			))
		})
	})
})

func expectPort(ports []networkingv1.NetworkPolicyPort, portNum int32, protocol corev1.Protocol) {
	port := intstr.FromInt32(portNum)
	found := false
	for _, p := range ports {
		if p.Port != nil && *p.Port == port && p.Protocol != nil && *p.Protocol == protocol {
			found = true
			break
		}
	}
	ExpectWithOffset(1, found).To(BeTrue(), "expected port %d/%s in ports %v", portNum, protocol, ports)
}
