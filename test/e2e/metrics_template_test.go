//go:build integration || ha

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
	"errors"
	"io"
	"slices"
	"testing"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	utils "github.com/sigstore/model-validation-operator/test/utils"
)

// Keep the metrics helper compatible with restricted PSA and OpenShift's
// namespace-assigned UID range, even when the caller can use an elevated SCC.
func TestMetricsPodSecurityTemplate(t *testing.T) {
	tmpl, err := template.New("metrics").Option("missingkey=error").Parse(string(curlPodTemplate))
	if err != nil {
		t.Fatal(err)
	}
	data := utils.CurlPodTemplateData{
		PodName: "metrics-security-test", Namespace: "metrics-test", ServiceAccount: "metrics-reader",
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewYAMLOrJSONDecoder(&rendered, 4096)
	podCount := 0
	for {
		var pod corev1.Pod
		if err := decoder.Decode(&pod); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if pod.Kind != "Pod" {
			continue
		}
		podCount++
		if pod.Name != data.PodName || pod.Namespace != data.Namespace ||
			(pod.Spec.ServiceAccountName != data.ServiceAccount && pod.Spec.DeprecatedServiceAccount != data.ServiceAccount) {
			t.Fatalf("metrics Pod identity or service account was not rendered correctly: %+v", pod)
		}
		assertMetricsPodSecurity(t, pod)
	}
	if podCount != 1 {
		t.Fatalf("expected exactly one rendered metrics Pod, got %d", podCount)
	}
}

func assertMetricsPodSecurity(t *testing.T, pod corev1.Pod) {
	t.Helper()
	if pod.Annotations["openshift.io/required-scc"] != "restricted-v2" {
		t.Fatal("metrics Pod must explicitly require restricted-v2 rather than inherit the fixture SCC")
	}
	if pod.Spec.SecurityContext != nil && (pod.Spec.SecurityContext.RunAsUser != nil ||
		pod.Spec.SecurityContext.SELinuxOptions != nil) {
		t.Fatal("Pod-level security must not override OpenShift's assigned UID or SELinux context")
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected one metrics helper container, got %d", len(pod.Spec.Containers))
	}
	security := pod.Spec.Containers[0].SecurityContext
	if security == nil || security.RunAsNonRoot == nil || !*security.RunAsNonRoot ||
		security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
		t.Fatal("metrics container must require non-root execution and disable privilege escalation")
	}
	if security.RunAsUser != nil || security.SELinuxOptions != nil ||
		(security.Privileged != nil && *security.Privileged) {
		t.Fatal("metrics container must not force a UID, SELinux context, or privileged execution")
	}
	if security.Capabilities == nil || len(security.Capabilities.Add) != 0 ||
		!slices.Contains(security.Capabilities.Drop, corev1.Capability("ALL")) {
		t.Fatal("metrics container must drop all capabilities without adding any")
	}
	if security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("metrics container must use RuntimeDefault seccomp")
	}
}
