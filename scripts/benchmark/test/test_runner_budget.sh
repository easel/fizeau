#!/usr/bin/env bash
# test_runner_budget.sh — acceptance tests for budget enforcement (A3c)
#
# Tests:
#  1. test_max_cost_usd_produces_budget_halted: --max-cost-usd flag produces
#     budget_halted placeholders when cap is reached.
#  2. test_budget_json_accumulates: budget.json by_cell sums to total_usd;
#     field types are numeric.
#  3. test_no_cap_no_halt: without --max-cost-usd, no cells are budget_halted
#     regardless of cost.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BENCHMARK_BIN="${SCRIPT_DIR}/benchmark"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

TESTS_PASSED=0
TESTS_FAILED=0

fail() {
  echo -e "${RED}FAIL${NC}: $*" >&2
  TESTS_FAILED=$((TESTS_FAILED + 1))
}

pass() {
  echo -e "${GREEN}PASS${NC}: $*"
  TESTS_PASSED=$((TESTS_PASSED + 1))
}

require() { command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"; }

# test_max_cost_usd_produces_budget_halted: AC1
# Verify --max-cost-usd flag produces budget_halted placeholders.
test_max_cost_usd_produces_budget_halted() {
  local test_name="test_max_cost_usd_produces_budget_halted"
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" RETURN

  local out_dir stub_dir
  out_dir="${tmpdir}/out"
  stub_dir="${tmpdir}/stubs"
  mkdir -p "${out_dir}" "${stub_dir}"

  # Stub task-executor that reports 0.05 USD per cell
  cat >"${stub_dir}/costly" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"$spec")"
task_id="$(jq -r '.task_id' <<<"$spec")"
mkdir -p "$cell_dir"
jq -n --arg t "$task_id" \
  '{task_id:$t, final_status:"completed", status:"completed",
    input_tokens:1000, output_tokens:1000, cached_input_tokens:0,
    cost_usd:0.05}' \
  >"$cell_dir/result.json"
EOF
  chmod +x "${stub_dir}/costly"

  # Run with --max-cost-usd 0.01 (first cell costs 0.05, should trigger halt)
  set +e
  env BENCH_TASK_EXECUTOR_OVERRIDE="${stub_dir}/costly" \
      BENCH_HARBOR_DIGEST_OVERRIDE="sha256:test-digest-budget" \
      "${BENCHMARK_BIN}" \
        --profile sindri-lucebox \
        --bench-set tb-2-1-canary \
        --reps 1 \
        --max-cost-usd 0.01 \
        --out "${out_dir}" \
      >/dev/null 2>&1
  set -e

  # Check for at least one budget_halted cell
  local halted_count
  halted_count=$(grep -rl '"final_status": "budget_halted"' "${out_dir}/cells" 2>/dev/null | wc -l | tr -d ' ')
  if [[ ${halted_count} -lt 1 ]]; then
    fail "${test_name}: expected >=1 budget_halted cell; found ${halted_count}"
    return 1
  fi

  # Verify each halted cell has correct fields
  local halted_reports
  mapfile -t halted_reports < <(grep -rl '"final_status": "budget_halted"' \
    "${out_dir}/cells" 2>/dev/null || true)
  for report in "${halted_reports[@]}"; do
    local po
    po=$(jq -r '.process_outcome // ""' "${report}" 2>/dev/null || true)
    if [[ "${po}" != "setup_failed" ]]; then
      fail "${test_name}: halted cell has process_outcome=${po}, expected setup_failed"
      return 1
    fi
    local note
    note=$(jq -r '.note // ""' "${report}" 2>/dev/null || true)
    if [[ -z "${note}" ]]; then
      fail "${test_name}: halted cell has empty note"
      return 1
    fi
  done

  pass "${test_name}"
}

# test_budget_json_accumulates: AC2
# Verify budget.json sums correctly and has numeric types.
test_budget_json_accumulates() {
  local test_name="test_budget_json_accumulates"
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" RETURN

  local out_dir stub_dir
  out_dir="${tmpdir}/out"
  stub_dir="${tmpdir}/stubs"
  mkdir -p "${out_dir}" "${stub_dir}"

  # Stub task-executor that reports 0.02 USD per cell
  cat >"${stub_dir}/metered" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"$spec")"
task_id="$(jq -r '.task_id' <<<"$spec")"
mkdir -p "$cell_dir"
jq -n --arg t "$task_id" \
  '{task_id:$t, final_status:"completed", status:"completed",
    input_tokens:500, output_tokens:500, cached_input_tokens:0,
    cost_usd:0.02}' \
  >"$cell_dir/result.json"
EOF
  chmod +x "${stub_dir}/metered"

  # Run with --max-cost-usd 0.10 (should allow ~5 cells at 0.02 each)
  set +e
  env BENCH_TASK_EXECUTOR_OVERRIDE="${stub_dir}/metered" \
      BENCH_HARBOR_DIGEST_OVERRIDE="sha256:test-digest-budget2" \
      "${BENCHMARK_BIN}" \
        --profile sindri-lucebox \
        --bench-set tb-2-1-canary \
        --reps 1 \
        --max-cost-usd 0.10 \
        --out "${out_dir}" \
      >/dev/null 2>&1
  set -e

  # Verify budget.json exists and has proper structure
  local budget_json="${out_dir}/budget.json"
  if [[ ! -f "${budget_json}" ]]; then
    fail "${test_name}: budget.json missing"
    return 1
  fi

  # Check that total_cost_usd equals sum of cells[].cost_usd
  local total
  total=$(jq -r '.total_cost_usd' "${budget_json}" 2>/dev/null || true)
  if [[ -z "${total}" ]]; then
    fail "${test_name}: total_cost_usd field missing or unparseable"
    return 1
  fi

  # Verify total is numeric and > 0
  if ! [[ "${total}" =~ ^[0-9.]+$ ]]; then
    fail "${test_name}: total_cost_usd=${total} is not numeric"
    return 1
  fi

  # Recompute from cells array
  local recomputed
  recomputed=$(jq -r '[.cells[].cost_usd] | add' "${budget_json}" 2>/dev/null || true)
  if [[ -z "${recomputed}" ]]; then
    fail "${test_name}: cannot recompute sum from cells"
    return 1
  fi

  # Compare totals (allow small floating-point error)
  local diff
  diff=$(awk -v a="${total}" -v b="${recomputed}" 'BEGIN{print(a - b >= -0.001 && a - b <= 0.001)}'  || true)
  if [[ "${diff}" != "1" ]]; then
    fail "${test_name}: total=${total} != sum(cells)=${recomputed}"
    return 1
  fi

  # Verify all cells have numeric cost_usd
  local cell_count
  cell_count=$(jq '.cells | length' "${budget_json}" 2>/dev/null || true)
  if [[ "${cell_count}" -lt 1 ]]; then
    fail "${test_name}: budget.json has no cells recorded"
    return 1
  fi

  local i
  for ((i = 0; i < cell_count; i++)); do
    local cost
    cost=$(jq -r ".cells[${i}].cost_usd" "${budget_json}" 2>/dev/null || true)
    if [[ -z "${cost}" ]] || ! [[ "${cost}" =~ ^[0-9.]+$ ]]; then
      fail "${test_name}: cells[${i}].cost_usd=${cost} is not numeric"
      return 1
    fi
  done

  pass "${test_name}"
}

# test_no_cap_no_halt: AC3
# Verify that without --max-cost-usd, no cells are budget_halted.
test_no_cap_no_halt() {
  local test_name="test_no_cap_no_halt"
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" RETURN

  local out_dir stub_dir
  out_dir="${tmpdir}/out"
  stub_dir="${tmpdir}/stubs"
  mkdir -p "${out_dir}" "${stub_dir}"

  # Stub task-executor that reports high cost
  cat >"${stub_dir}/expensive" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"$spec")"
task_id="$(jq -r '.task_id' <<<"$spec")"
mkdir -p "$cell_dir"
jq -n --arg t "$task_id" \
  '{task_id:$t, final_status:"completed", status:"completed",
    input_tokens:10000, output_tokens:10000, cached_input_tokens:0,
    cost_usd:1.00}' \
  >"$cell_dir/result.json"
EOF
  chmod +x "${stub_dir}/expensive"

  # Run WITHOUT --max-cost-usd (no budget enforcement)
  set +e
  env BENCH_TASK_EXECUTOR_OVERRIDE="${stub_dir}/expensive" \
      BENCH_HARBOR_DIGEST_OVERRIDE="sha256:test-digest-no-cap" \
      "${BENCHMARK_BIN}" \
        --profile sindri-lucebox \
        --bench-set tb-2-1-canary \
        --reps 1 \
        --out "${out_dir}" \
      >/dev/null 2>&1
  set -e

  # Verify no budget_halted cells exist
  local halted_count
  if [[ -d "${out_dir}/cells" ]]; then
    halted_count=$(find "${out_dir}/cells" -name "report.json" -exec grep -l '"final_status": "budget_halted"' {} \; 2>/dev/null | wc -l)
  else
    halted_count=0
  fi
  if [[ ${halted_count} -gt 0 ]]; then
    fail "${test_name}: expected 0 budget_halted cells; found ${halted_count}"
    return 1
  fi

  # Verify budget.json was not created (since no cap was set)
  local budget_json="${out_dir}/budget.json"
  if [[ -f "${budget_json}" ]]; then
    fail "${test_name}: budget.json should not exist when --max-cost-usd is not set"
    return 1
  fi

  pass "${test_name}"
}

# Main test runner
main() {
  echo "Running budget enforcement tests..."
  echo ""

  require jq
  require yq

  test_max_cost_usd_produces_budget_halted
  test_budget_json_accumulates
  test_no_cap_no_halt

  echo ""
  echo "========================================"
  echo "Test Summary:"
  echo "  Tests passed: $TESTS_PASSED"
  echo "  Tests failed: $TESTS_FAILED"
  echo "========================================"

  if [[ $TESTS_FAILED -gt 0 ]]; then
    exit 1
  fi
  exit 0
}

main "$@"
