#!/usr/bin/env bash
# Runs the full unit test suite across every module in the workspace.
# Usage: ./scripts/test-all.sh
#
# Exit code 0 = all pass, non-zero = at least one failure.

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

RESULTS=()
OVERALL=0

run_tests() {
  local dir="$1"
  local label="$2"
  echo ""
  echo "━━━ $label ━━━"
  if (cd "$dir" && go test -race -count=1 ./... 2>&1); then
    RESULTS+=("PASS  $label")
  else
    RESULTS+=("FAIL  $label")
    OVERALL=1
  fi
}

run_tests "$ROOT/internal"                   "internal (shared libs)"
run_tests "$ROOT/services/ledger-svc"        "ledger-svc"
run_tests "$ROOT/services/account-svc"       "account-svc"
run_tests "$ROOT/services/fx-svc"            "fx-svc"
run_tests "$ROOT/services/transaction-svc"   "transaction-svc"
run_tests "$ROOT/services/payout-svc"        "payout-svc"
run_tests "$ROOT/services/recon-worker"      "recon-worker"
run_tests "$ROOT/services/gateway"           "gateway"
run_tests "$ROOT/services/consumers"         "consumers"
run_tests "$ROOT/services/ui-bff"            "ui-bff"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
for r in "${RESULTS[@]}"; do echo "  $r"; done
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
[ "$OVERALL" -eq 0 ] && echo "  ALL TESTS PASSED" || echo "  SOME TESTS FAILED"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

exit "$OVERALL"
