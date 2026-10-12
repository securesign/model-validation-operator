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
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"text/template"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	utils "github.com/sigstore/model-validation-operator/test/utils"
)

func TestCleanupAllResources(t *testing.T) {
	firstFailure := errors.New("Pod finalizer did not finish")
	secondFailure := errors.New("CSV API rejected deletion")
	targets := []cleanupTarget{
		{"pod", "initial", webhookTestNamespace},
		{"pod", "upgraded", webhookTestNamespace},
		{"modelvalidation", "model", webhookTestNamespace},
		{"subscription", "subscription", operatorNamespace},
		{"csv", "candidate", operatorNamespace},
		{"operatorgroup", "group", operatorNamespace},
		{"catalogsource", "catalog", operatorNamespace},
	}
	var attempted []cleanupTarget
	err := cleanupAllResources(targets, func(resource, name, namespace string) error {
		attempted = append(attempted, cleanupTarget{resource, name, namespace})
		switch name {
		case "initial":
			return firstFailure
		case "candidate":
			return secondFailure
		default:
			return nil
		}
	})
	if !slices.Equal(attempted, targets) {
		t.Fatalf("cleanup did not attempt all owned resources in order: %v", attempted)
	}
	if !errors.Is(err, firstFailure) || !errors.Is(err, secondFailure) {
		t.Fatalf("cleanup did not preserve both failures: %v", err)
	}
	if !strings.Contains(err.Error(), "initial") || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("aggregate error omitted resource identities: %v", err)
	}
	if err := cleanupAllResources(targets, func(string, string, string) error { return nil }); err != nil {
		t.Fatalf("successful cleanup failed: %v", err)
	}
}

func TestCleanupRejectsUnnamedTargetsWithoutSkippingLaterTargets(t *testing.T) {
	var attempted []string
	err := cleanupAllResources([]cleanupTarget{{"csv", "", operatorNamespace}, {"pod", "owned", webhookTestNamespace}},
		func(_, name, _ string) error { attempted = append(attempted, name); return nil })
	if err == nil || !slices.Equal(attempted, []string{"owned"}) {
		t.Fatalf("unnamed target must fail without broad deletion or blocking later cleanup: %v, %v", attempted, err)
	}
}

func TestSubscriptionCSVNames(t *testing.T) {
	const prefix = `{"apiVersion":"operators.coreos.com/v1alpha1","kind":"Subscription","status":`
	names, err := subscriptionCSVNames(prefix + `{"installedCSV":"base","currentCSV":"candidate"}}`)
	if err != nil || !slices.Equal(names, []string{"base", "candidate"}) {
		t.Fatalf("failed to discover only Subscription-referenced CSVs: %v, %v", names, err)
	}
	names, err = subscriptionCSVNames(prefix + `{"installedCSV":"base","currentCSV":true}}`)
	if err == nil || !slices.Equal(names, []string{"base"}) {
		t.Fatalf("must preserve known identity and report malformed remaining identity: %v, %v", names, err)
	}
	if _, err := subscriptionCSVNames(`invalid JSON`); err == nil {
		t.Fatal("malformed Subscription JSON must not be silently ignored")
	}
	names, err = subscriptionCSVNames(prefix + `{}}`)
	if err != nil || len(names) != 0 {
		t.Fatalf("missing CSV references should return no names: %v, %v", names, err)
	}
}

func TestControllerRollout(t *testing.T) {
	const expectedImage = "example.com/mvo:release"
	tests := []struct {
		name   string
		change func(*appsv1.Deployment)
	}{
		{name: "controller has not observed new generation", change: func(d *appsv1.Deployment) {
			d.Status.ObservedGeneration = d.Generation - 1
		}},
		{name: "old replica still present", change: func(d *appsv1.Deployment) { d.Status.Replicas++ }},
		{name: "new replica missing", change: func(d *appsv1.Deployment) { d.Status.UpdatedReplicas-- }},
		{name: "replica not ready", change: func(d *appsv1.Deployment) { d.Status.ReadyReplicas-- }},
		{name: "replica not available", change: func(d *appsv1.Deployment) { d.Status.AvailableReplicas-- }},
		{name: "unavailable replicas", change: func(d *appsv1.Deployment) { d.Status.UnavailableReplicas++ }},
		{name: "wrong release image", change: func(d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].Image = "example.com/mvo:other"
		}},
		{name: "manager missing", change: func(d *appsv1.Deployment) { d.Spec.Template.Spec.Containers = nil }},
		{name: "replicas unspecified", change: func(d *appsv1.Deployment) { d.Spec.Replicas = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			replicas := int32(2)
			deployment := appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec: appsv1.DeploymentSpec{Replicas: &replicas,
					Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "manager", Image: expectedImage}},
					}},
				},
				Status: appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2,
					UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2},
			}
			if err := validateControllerRollout(deployment, expectedImage); err != nil {
				t.Fatalf("healthy rollout rejected: %v", err)
			}
			tc.change(&deployment)
			if err := validateControllerRollout(deployment, expectedImage); err == nil {
				t.Fatal("incomplete or incorrect rollout accepted")
			}
		})
	}
}

func TestCatalogTemplate(t *testing.T) {
	tmpl, err := template.New("catalogsource").Parse(string(catalogSourceTemplate))
	if err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, utils.CatalogSourceTemplateData{
		Name: "test-catalog", Namespace: operatorNamespace, Image: "example.com/catalog:v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]any
	if err := yaml.Unmarshal(rendered.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	mode, _, err := unstructured.NestedString(catalog, "spec", "grpcPodConfig", "securityContextConfig")
	if err != nil || mode != "restricted" {
		t.Fatalf("catalog security mode = %q, error = %v", mode, err)
	}
}

func TestExistingCSVs(t *testing.T) {
	const packageName = "model-validation-operator"
	const copiedFromLabel = "olm.copiedFrom"
	tests := []struct {
		name      string
		change    func(*unstructured.Unstructured)
		wantError bool
	}{
		{name: "confirmed unrelated copy is allowed"},
		{name: "missing copy label", change: func(csv *unstructured.Unstructured) {
			csv.SetLabels(nil)
		}, wantError: true},
		{name: "missing copy reason", change: func(csv *unstructured.Unstructured) {
			delete(csv.Object, "status")
		}, wantError: true},
		{name: "local installation", change: func(csv *unstructured.Unstructured) {
			csv.SetLabels(nil)
			csv.Object["status"] = map[string]any{"reason": "InstallSucceeded"}
		}, wantError: true},
		{name: "self copy label", change: func(csv *unstructured.Unstructured) {
			csv.SetLabels(map[string]string{copiedFromLabel: operatorNamespace})
		}, wantError: true},
		{name: "wrong destination namespace", change: func(csv *unstructured.Unstructured) {
			csv.SetNamespace("other")
		}, wantError: true},
		{name: "copied MVO by package name", change: func(csv *unstructured.Unstructured) {
			csv.SetName(packageName + ".v0.1.2")
		}, wantError: true},
		{name: "copied different package owning MVO API", change: func(csv *unstructured.Unstructured) {
			csv.Object["spec"] = map[string]any{"customresourcedefinitions": map[string]any{
				"owned": []any{map[string]any{"name": "modelvalidations.ml.sigstore.dev"}},
			}}
		}, wantError: true},
		{name: "malformed reason", change: func(csv *unstructured.Unstructured) {
			csv.Object["status"] = map[string]any{"reason": true}
		}, wantError: true},
	}
	if err := ensureNoConflictingCSVs(nil, operatorNamespace, packageName); err != nil {
		t.Fatalf("empty namespace rejected: %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			csv := unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{"reason": "Copied"},
				"spec": map[string]any{"customresourcedefinitions": map[string]any{
					"owned": []any{map[string]any{"name": "certificates.cert-manager.io"}},
				}},
			}}
			csv.SetName("cert-manager.v1.19.0")
			csv.SetNamespace(operatorNamespace)
			csv.SetLabels(map[string]string{copiedFromLabel: "cert-manager-operator"})
			if tc.change != nil {
				tc.change(&csv)
			}
			err := ensureNoConflictingCSVs([]unstructured.Unstructured{csv}, operatorNamespace, packageName)
			if (err != nil) != tc.wantError {
				t.Fatalf("wanted error=%t, got %v", tc.wantError, err)
			}
		})
	}
}
