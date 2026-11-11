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

package utils_test //nolint:revive

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestRedactCommand(t *testing.T) {
	for _, command := range []string{
		`curl -H 'Authorization: Bearer sensitive-token' https://example.invalid`,
		`kubectl --token=sensitive-token get pods`,
		`oc login --password sensitive-token`,
	} {
		result := redactCommand(command)
		if strings.Contains(result, "sensitive-token") || !strings.Contains(result, "[REDACTED]") {
			t.Errorf("secret was not redacted: %s", result)
		}
	}
}

func TestRunPreservesProcessDirectoryAndCommandEnvironment(t *testing.T) {
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `printf '%s\n%s' "$PWD" "$MVO_TEST_VALUE"`)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "MVO_TEST_VALUE=preserved")
	output, err := Run(cmd)
	if err != nil {
		t.Fatal(err)
	}
	expectedDir, err := filepath.EvalSymlinks(cmd.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if output != expectedDir+"\npreserved" {
		t.Fatalf("command directory or environment lost: %q", output)
	}
	after, err := os.Getwd()
	if err != nil || before != after {
		t.Fatalf("Run changed the process directory: %s -> %s (%v)", before, after, err)
	}
}

func TestRunRedactsSecretInWrappedErrors(t *testing.T) {
	_, err := Run(exec.Command("sh", "-c", "echo 'Authorization: Bearer sensitive-token' >&2; exit 1"))
	if err == nil || strings.Contains(err.Error(), "sensitive-token") {
		t.Fatalf("expected a redacted error, got %v", err)
	}
}

func TestKubectlResourceAbsentDoesNotHideAPIErrors(t *testing.T) {
	stub := `#!/bin/sh
case $MVO_FAKE_GET in
missing) exit 0;;
found) echo pod/test;;
*) echo Forbidden >&2; exit 1;;
esac
`
	fakeKubectl(t, stub)
	for _, test := range []struct {
		name   string
		absent bool
		err    bool
	}{
		{name: "missing", absent: true},
		{name: "found"},
		{name: "forbidden", err: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MVO_FAKE_GET", test.name)
			absent, err := KubectlResourceAbsent("pod", "test", "test-namespace")
			if absent != test.absent || (err != nil) != test.err {
				t.Fatalf("absent = %t, error = %v", absent, err)
			}
		})
	}
}

func fakeKubectl(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestKubectlGetBoundsRequestsAndPreservesResourceArguments(t *testing.T) {
	fakeKubectl(t, "#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
	output, err := KubectlGet("pods", "test-pod", "test-namespace", "json")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"get", "pods", "--request-timeout=15s", "test-pod", "-n", "test-namespace", "-o", "json"}
	if !reflect.DeepEqual(GetNonEmptyLines(output), want) {
		t.Errorf("kubectl arguments: %q, want %q", GetNonEmptyLines(output), want)
	}
	output, err = KubectlGet("nodes", "", "", "")
	if err != nil || !reflect.DeepEqual(GetNonEmptyLines(output), []string{"get", "nodes", "--request-timeout=15s"}) {
		t.Errorf("cluster-wide list arguments: %q (%v)", output, err)
	}
}

func TestDefaultPodDataMountsAllVerificationFixturesReadOnly(t *testing.T) {
	data := DefaultPodData("test-pod", "test-namespace", "test-model", "test-batch")
	if data.PodName != "test-pod" || data.Namespace != "test-namespace" ||
		data.ModelName != "test-model" || data.TestBatch != "test-batch" {
		t.Fatalf("pod identity fields were not preserved: %+v", data)
	}
	wantMounts := []VolumeMount{
		{Name: "model-data", MountPath: "/data", ReadOnly: true},
		{Name: "keys-data", MountPath: "/keys", ReadOnly: true},
		{Name: "fixture-data", MountPath: "/fixtures", ReadOnly: true},
	}
	if !reflect.DeepEqual(data.VolumeMounts, wantMounts) {
		t.Errorf("unexpected mounts: %+v", data.VolumeMounts)
	}
	wantVolumes := []Volume{
		{Name: "model-data", HostPath: &HostPathVolume{
			Path: "/tmp/e2e-model-data", Type: string(corev1.HostPathDirectory),
		}},
		{Name: "keys-data", HostPath: &HostPathVolume{
			Path: "/tmp/e2e-keys-data", Type: string(corev1.HostPathDirectory),
		}},
		{Name: "fixture-data", HostPath: &HostPathVolume{
			Path: "/tmp/e2e-fixture-data", Type: string(corev1.HostPathDirectory),
		}},
	}
	if !reflect.DeepEqual(data.Volumes, wantVolumes) {
		t.Errorf("unexpected hostPath sources: %+v", data.Volumes)
	}
}
