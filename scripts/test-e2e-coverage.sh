#!/usr/bin/env bash
# No Kubernetes access: use mocked clients and real Go coverage data.
set -euo pipefail
project_root=$(cd "$(dirname "$0")/.." && pwd)
fixtures="$project_root/scripts/testdata/e2e-coverage"
collector="$project_root/scripts/e2e-coverage.sh"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/mvo-coverage-test.XXXXXXXX")
trap 'rm -rf "$test_dir"' EXIT
export E2E_COVERAGE_DIR="$test_dir/collected"
export E2E_COVERAGE_PROFILE="$test_dir/coverage.out"
export E2E_COVERAGE_ATTEMPTS=4 E2E_COVERAGE_RETRY_DELAY=1
export MOCK_STATE="$test_dir" MOCK_RESPONSE="$test_dir/before.json"
export KUBECTL="$fixtures/kubectl" PATH="$fixtures:$PATH"

assert_no_forwards() {
    [[ -f "$test_dir/forward-pids" ]] || return 0
    while read -r pid; do
        if kill -0 "$pid" 2>/dev/null; then
            echo "FAIL: port-forward $pid leaked" >&2
            exit 1
        fi
    done < "$test_dir/forward-pids"
}

expect_failure() {
    local mode=$1
    if MOCK_MODE="$mode" bash "$collector" snapshot > "$test_dir/$mode.log" 2>&1; then
        echo "FAIL: accepted $mode" >&2
        exit 1
    fi
    assert_no_forwards
    echo "PASS: rejects $mode and cleans port-forwards"
}

mkdir "$test_dir/before" "$test_dir/after"
go build -cover -covermode=atomic -o "$test_dir/fixture" "$fixtures/main.go"
GOCOVERDIR="$test_dir/before" "$test_dir/fixture" before >/dev/null
GOCOVERDIR="$test_dir/after" "$test_dir/fixture" after >/dev/null
for phase in before after; do
    meta=("$test_dir/$phase"/covmeta.*)
    counters=("$test_dir/$phase"/covcounters.*)
    jq -n --arg meta "$(basename "${meta[0]}")" --arg counter "$(basename "${counters[0]}")" \
        --arg meta_data "$(base64 < "${meta[0]}" | tr -d '\n')" \
        --arg counter_data "$(base64 < "${counters[0]}" | tr -d '\n')" \
        '{meta_filename:$meta,counters_filename:$counter,meta_data:$meta_data,counters_data:$counter_data}' \
        > "$test_dir/$phase.json"
done

if bash "$collector" snapshot > "$test_dir/no-session.log" 2>&1; then
    echo "FAIL: snapshot should require explicit initialization" >&2
    exit 1
fi
bash "$collector" init
if bash "$collector" report > "$test_dir/empty-report.log" 2>&1; then
    echo "FAIL: empty report should not succeed" >&2
    exit 1
fi
echo "PASS: requires initialization and nonempty report"
for mode in api-error zero-pods invalid-list port-forward-error http-error malformed missing-counters path-traversal bad-base64 bad-binary; do
    expect_failure "$mode"
done

bash "$collector" snapshot
bash "$collector" report
go tool cover -func="$E2E_COVERAGE_PROFILE" > "$test_dir/before-functions"
awk '$2 == "beforeRestart" && $3 == "100.0%" { found = 1 } END { exit !found }' "$test_dir/before-functions"
awk '$2 == "afterRestart" && $3 == "0.0%" { found = 1 } END { exit !found }' "$test_dir/before-functions"
cp "$E2E_COVERAGE_PROFILE" "$test_dir/first.out"
# Re-reading cumulative counters must replace the process snapshot, not add hits.
bash "$collector" collect
cmp "$test_dir/first.out" "$E2E_COVERAGE_PROFILE"
echo "PASS: multi-replica collection is repeatable without double-counting"

# Every process is replaced, just as in the scale-to-zero test. Only the second
# response knows afterRestart; beforeRestart must survive through stored data.
MOCK_GENERATION=2 MOCK_RESPONSE="$test_dir/after.json" bash "$collector" collect
go tool cover -func="$E2E_COVERAGE_PROFILE" > "$test_dir/merged-functions"
awk '$2 == "beforeRestart" && $3 == "100.0%" { found = 1 } END { exit !found }' "$test_dir/merged-functions"
awk '$2 == "afterRestart" && $3 == "100.0%" { found = 1 } END { exit !found }' "$test_dir/merged-functions"
assert_no_forwards
echo "PASS: uniquely covered paths survive replacement of every replica"

bash "$collector" init
[[ ! -e "$E2E_COVERAGE_PROFILE" ]] || { echo "FAIL: stale profile survives initialization" >&2; exit 1; }
if bash "$collector" report > "$test_dir/new-session.log" 2>&1; then
    echo "FAIL: a new session reused old snapshots" >&2
    exit 1
fi
echo "PASS: explicit initialization isolates successive suite runs"
