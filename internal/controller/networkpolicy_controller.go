package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const networkPolicyName = "controller-manager"

// NetworkPolicyReconciler ensures a static NetworkPolicy exists in the operator namespace,
// restricting traffic to only the required endpoints.
type NetworkPolicyReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch

// Reconcile ensures the operator's NetworkPolicy exists with the desired spec
func (r *NetworkPolicyReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkPolicyName,
			Namespace: r.Namespace,
		},
	}

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		np.Labels = map[string]string{
			"app.kubernetes.io/name":       "model-validation-operator",
			"app.kubernetes.io/managed-by": "model-validation-operator",
		}
		np.Spec = r.desiredSpec()
		return nil
	})
	if err != nil {
		logger.Error(err, "Failed to reconcile NetworkPolicy")
		return ctrl.Result{}, err
	}

	if result != controllerutil.OperationResultNone {
		logger.Info("NetworkPolicy reconciled", "operation", result)
	}

	return ctrl.Result{}, nil
}

func (r *NetworkPolicyReconciler) desiredSpec() networkingv1.NetworkPolicySpec {
	port53 := intstr.FromInt32(53)
	port443 := intstr.FromInt32(443)
	port6443 := intstr.FromInt32(6443)
	port8081 := intstr.FromInt32(8081)
	port8443 := intstr.FromInt32(8443)
	port9443 := intstr.FromInt32(9443)
	protocolTCP := corev1.ProtocolTCP
	protocolUDP := corev1.ProtocolUDP

	return networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{
			MatchLabels: map[string]string{
				"control-plane":          "controller-manager",
				"app.kubernetes.io/name": "model-validation-operator",
			},
		},
		PolicyTypes: []networkingv1.PolicyType{
			networkingv1.PolicyTypeIngress,
			networkingv1.PolicyTypeEgress,
		},
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			// Webhook: only the API server (represented via the default namespace) calls this.
			{
				From: []networkingv1.NetworkPolicyPeer{
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "default",
							},
						},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{Port: &port9443, Protocol: &protocolTCP},
				},
			},
			// Health probes: kubelet traffic is not subject to NetworkPolicy, but
			// in-cluster liveness checks (e.g. from the same namespace) need access.
			{
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{Port: &port8081, Protocol: &protocolTCP},
				},
			},
			// Metrics: scoped to namespaces labeled metrics=enabled.
			{
				From: []networkingv1.NetworkPolicyPeer{
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"metrics": "enabled",
							},
						},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{Port: &port8443, Protocol: &protocolTCP},
				},
			},
		},
		Egress: []networkingv1.NetworkPolicyEgressRule{
			// DNS: scoped to kube-system (vanilla K8s) and openshift-dns (OpenShift).
			{
				To: []networkingv1.NetworkPolicyPeer{
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "kube-system",
							},
						},
					},
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "openshift-dns",
							},
						},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{Port: &port53, Protocol: &protocolTCP},
					{Port: &port53, Protocol: &protocolUDP},
				},
			},
			// Kubernetes API server.
			{
				To: []networkingv1.NetworkPolicyPeer{
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "default",
							},
						},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{Port: &port443, Protocol: &protocolTCP},
					{Port: &port6443, Protocol: &protocolTCP},
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *NetworkPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	isOurNetworkPolicy := func(_ context.Context, obj client.Object) []reconcile.Request {
		if obj.GetName() == networkPolicyName && obj.GetNamespace() == r.Namespace {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{
				Name:      networkPolicyName,
				Namespace: r.Namespace,
			}}}
		}
		return nil
	}

	isOurNamespace := func(_ context.Context, obj client.Object) []reconcile.Request {
		if obj.GetName() == r.Namespace {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{
				Name:      networkPolicyName,
				Namespace: r.Namespace,
			}}}
		}
		return nil
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("networkpolicy").
		Watches(&networkingv1.NetworkPolicy{}, handler.EnqueueRequestsFromMapFunc(isOurNetworkPolicy)).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(isOurNamespace)).
		Complete(r)
}
