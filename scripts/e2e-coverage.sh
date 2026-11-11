#!/usr/bin/env bash
# Collect Coverport snapshots without losing counters when a manager restarts.
# Run init once, snapshot before a disruption, and collect after the suite.
set -euo pipefail

project_root=$(cd "$(dirname "$0")/.." && pwd)
data_dir=${E2E_COVERAGE_DIR:-"$project_root/e2e-coverdata"}
profile=${E2E_COVERAGE_PROFILE:-"$project_root/e2e-cover.out"}
namespace=${E2E_OPERATOR_NAMESPACE:-model-validation-operator-system}
kubectl=${KUBECTL:-kubectl}
go_tool=${GO:-go}
attempts=${E2E_COVERAGE_ATTEMPTS:-30}
retry_delay=${E2E_COVERAGE_RETRY_DELAY:-1}
forward_pid=

fail() { echo "E2E coverage: $*" >&2; exit 1; }
stop_forward() {
    if [[ -n "$forward_pid" ]]; then
        kill "$forward_pid" 2>/dev/null || true
        wait "$forward_pid" 2>/dev/null || true
        forward_pid=
    fi
}
trap stop_forward EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

[[ "$data_dir" != *,* ]] || fail "the data directory must not contain commas (covdata input separator)"
[[ "$attempts" =~ ^[1-9][0-9]*$ ]] || fail "E2E_COVERAGE_ATTEMPTS must be positive"
[[ "$retry_delay" =~ ^[0-9]+$ ]] || fail "E2E_COVERAGE_RETRY_DELAY must be a nonnegative integer"

session_dir() {
    [[ -f "$data_dir/.active" ]] || fail "initialize a session first: make init-e2e-coverage"
    local active
    active=$(<"$data_dir/.active")
    [[ "$active" =~ ^run\.[a-zA-Z0-9]+$ ]] || fail "invalid coverage session marker"
    [[ -d "$data_dir/$active" ]] || fail "coverage session directory is missing"
    printf '%s\n' "$data_dir/$active"
}

init_session() {
    mkdir -p "$data_dir"
    local session marker
    session=$(mktemp -d "$data_dir/run.XXXXXXXX")
    if [[ -f "$profile" ]]; then
        mv "$profile" "$session/previous-profile.out"
    fi
    marker=$(mktemp "$data_dir/.active.XXXXXXXX")
    basename "$session" > "$marker"
    mv "$marker" "$data_dir/.active"
    echo "Initialized coverage session $session (previous sessions retained)"
}

validate_profile() {
    # covdata may succeed for an empty directory; require actual statement records.
    awk 'NR == 1 { if ($0 !~ /^mode: (set|count|atomic)$/) exit 1; next }
        NF != 3 || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/ { exit 1 }
        { statements += $2 }
        END { if (NR < 2 || statements < 1) exit 1 }' "$1" || fail "empty or malformed coverage profile: $1"
}

snapshot() {
    local session inventory candidates pod uid restarts snapshot_dir port response meta counter marker
    for dependency in "$kubectl" "$go_tool" curl jq base64; do
        command -v "$dependency" >/dev/null || fail "required command not found: $dependency"
    done
    session=$(session_dir)
    inventory=$(mktemp "$session/pods.XXXXXXXX")
    # Check API failures outside command substitution in a for loop.
    "$kubectl" --request-timeout=20s get pods -n "$namespace" \
        -l control-plane=controller-manager -o json > "$inventory"
    jq -e '.items | type == "array"' "$inventory" >/dev/null || fail "invalid PodList response"
    candidates=$(jq -r '.items[] | select(.metadata.deletionTimestamp == null and .status.phase == "Running") |
        . as $pod | .status.containerStatuses[]? |
        select(.name == "manager" and .state.running != null) |
        [$pod.metadata.name, $pod.metadata.uid, (.restartCount | tostring)] | @tsv' "$inventory")
    [[ -n "$candidates" ]] || fail "no running manager processes found in $namespace"

    while IFS=$'\t' read -r pod uid restarts; do
        [[ "$pod" =~ ^[a-z0-9.-]+$ && "$uid" =~ ^[a-zA-Z0-9-]+$ && "$restarts" =~ ^[0-9]+$ ]] ||
            fail "invalid manager process identity"
        snapshot_dir=$(mktemp -d "$session/snapshot.XXXXXXXX")
        response="$snapshot_dir/response.json"
        # Let the OS choose a free local port; never expose Coverport publicly.
        "$kubectl" --request-timeout=20s port-forward -n "$namespace" --address=127.0.0.1 \
            --pod-running-timeout=20s "pod/$pod" :53700 > "$snapshot_dir/port-forward.log" 2>&1 &
        forward_pid=$!
        local collected=false
        for ((attempt = 1; attempt <= attempts; attempt++)); do
            kill -0 "$forward_pid" 2>/dev/null || break
            port=$(sed -nE 's/^Forwarding from 127\.0\.0\.1:([0-9]+) -> .*/\1/p' "$snapshot_dir/port-forward.log" | head -1)
            if [[ -n "$port" ]] && curl --silent --show-error --fail --connect-timeout 2 --max-time 5 \
                "http://127.0.0.1:$port/coverage" > "$response" 2> "$snapshot_dir/curl.log"; then
                collected=true
                break
            fi
            sleep "$retry_delay"
        done
        stop_forward
        if [[ "$collected" != true ]]; then
            cat "$snapshot_dir/port-forward.log" >&2
            fail "could not collect counters from $pod; diagnostics: $snapshot_dir"
        fi
        jq -e '
            (.meta_filename | type == "string" and test("^covmeta\\.[a-f0-9]{32}$")) and
            (.counters_filename | type == "string" and test("^covcounters\\.[a-f0-9]{32}\\.[0-9]+\\.[0-9]+$")) and
            (.meta_data | type == "string" and length > 0 and test("^[A-Za-z0-9+/]*={0,2}$")) and
            (.counters_data | type == "string" and length > 0 and test("^[A-Za-z0-9+/]*={0,2}$")) and
            ((.meta_filename | split(".")[1]) == (.counters_filename | split(".")[1]))
        ' "$response" >/dev/null || fail "malformed Coverport response from $pod"
        meta=$(jq -r .meta_filename "$response")
        counter=$(jq -r .counters_filename "$response")
        local decode=--decode
        base64 --decode </dev/null >/dev/null 2>&1 || decode=-D
        jq -r .meta_data "$response" | base64 "$decode" > "$snapshot_dir/$meta"
        jq -r .counters_data "$response" | base64 "$decode" > "$snapshot_dir/$counter"
        [[ -s "$snapshot_dir/$meta" && -s "$snapshot_dir/$counter" ]] || fail "empty binary coverage data from $pod"
        "$go_tool" tool covdata textfmt -i="$snapshot_dir" -o="$snapshot_dir/profile.out"
        validate_profile "$snapshot_dir/profile.out"
        # Publish only a completely validated snapshot. The latest snapshot for a
        # process replaces its pointer, avoiding double-counting cumulative hits.
        # Restarted containers and replacement Pods get separate process pointers.
        marker=$(mktemp "$session/.latest.XXXXXXXX")
        basename "$snapshot_dir" > "$marker"
        mv "$marker" "$session/latest.$uid.$restarts"
        echo "Saved coverage for $pod (UID $uid, restart $restarts)"
    done <<< "$candidates"
}

report() {
    local session pointer name inputs= output
    session=$(session_dir)
    shopt -s nullglob
    local pointers=("$session"/latest.*)
    ((${#pointers[@]} > 0)) || fail "no validated snapshots in the active session"
    for pointer in "${pointers[@]}"; do
        name=$(<"$pointer")
        [[ "$name" =~ ^snapshot\.[a-zA-Z0-9]+$ && -d "$session/$name" ]] || fail "invalid snapshot pointer"
        inputs="${inputs:+$inputs,}$session/$name"
    done
    output=$(mktemp "$session/merged.XXXXXXXX")
    "$go_tool" tool covdata textfmt -i="$inputs" -o="$output"
    validate_profile "$output"
    mv "$output" "$profile"
    echo "Merged ${#pointers[@]} manager processes into $profile"
}

case "${1:-}" in
    init) init_session ;;
    snapshot) snapshot ;;
    report) report ;;
    collect) snapshot; report ;;
    *) fail "usage: $0 {init|snapshot|report|collect}" ;;
esac
