#!/usr/bin/env bash
set -euo pipefail

RESULTS_FILE="bench-results.log"
BENCH_CONFIG="otelcol-bench.yaml"
COLLECTOR="./otelcol-dev/otelcol"
STEP_THRESHOLD=5
BASELINE_THRESHOLD=8

usage() {
    echo "Usage: $0 --label <label> [--compare] [--compare-baseline] [--skip-build]"
    echo ""
    echo "  --label <label>       Label for this benchmark run (required)"
    echo "  --compare             Compare against the previous entry; fail if >$STEP_THRESHOLD% regression"
    echo "  --compare-baseline    Compare against the first entry (step 0); fail if >$BASELINE_THRESHOLD% regression"
    echo "  --skip-build          Skip the collector build step"
    exit 1
}

LABEL=""
COMPARE=false
COMPARE_BASELINE=false
SKIP_BUILD=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --label) LABEL="$2"; shift 2 ;;
        --compare) COMPARE=true; shift ;;
        --compare-baseline) COMPARE_BASELINE=true; shift ;;
        --skip-build) SKIP_BUILD=true; shift ;;
        *) usage ;;
    esac
done

if [[ -z "$LABEL" ]]; then
    usage
fi

cd "$(dirname "$0")"

if [[ "$SKIP_BUILD" == false ]]; then
    echo "==> Building collector..."
    ./ocb --config builder-config.yaml 2>&1 | tail -1
fi

if [[ ! -x "$COLLECTOR" ]]; then
    echo "ERROR: Collector binary not found at $COLLECTOR"
    exit 1
fi

echo "==> Running benchmark with label: $LABEL"
TMPLOG=$(mktemp)
trap "rm -f $TMPLOG" EXIT

"$COLLECTOR" --config "$BENCH_CONFIG" 2>&1 | tee "$TMPLOG"

LOGS_PER_SEC=$(grep '"logs_per_second"' "$TMPLOG" | grep 'finished generating logs' | tail -1 | sed -E 's/.*"logs_per_second": ?([0-9]+\.?[0-9]*).*/\1/')

if [[ -z "$LOGS_PER_SEC" ]]; then
    echo "ERROR: Could not parse logs_per_second from collector output"
    cat "$TMPLOG"
    exit 1
fi

TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "$TIMESTAMP $LABEL $LOGS_PER_SEC" >> "$RESULTS_FILE"
echo "==> Result: $LABEL = $LOGS_PER_SEC logs/sec"

check_regression() {
    local ref_lps="$1"
    local cur_lps="$2"
    local threshold="$3"
    local ref_label="$4"

    local pct
    pct=$(awk "BEGIN { printf \"%.2f\", (($ref_lps - $cur_lps) / $ref_lps) * 100 }")
    local is_regression
    is_regression=$(awk "BEGIN { print ($pct > $threshold) ? 1 : 0 }")

    echo "    Reference ($ref_label): $ref_lps logs/sec"
    echo "    Current:                $cur_lps logs/sec"
    echo "    Delta:                  ${pct}%"

    if [[ "$is_regression" == "1" ]]; then
        echo "FAIL: Regression of ${pct}% exceeds ${threshold}% threshold"
        return 1
    else
        echo "    PASS (within ${threshold}% threshold)"
        return 0
    fi
}

EXIT_CODE=0

if [[ "$COMPARE" == true ]]; then
    echo "==> Comparing against previous entry (${STEP_THRESHOLD}% threshold)..."
    PREV_LINE=$(tail -2 "$RESULTS_FILE" | head -1)
    if [[ -z "$PREV_LINE" ]]; then
        echo "WARNING: No previous entry found, skipping comparison"
    else
        PREV_LPS=$(echo "$PREV_LINE" | awk '{print $NF}')
        PREV_LABEL=$(echo "$PREV_LINE" | awk '{$1=""; $NF=""; print}' | xargs)
        if ! check_regression "$PREV_LPS" "$LOGS_PER_SEC" "$STEP_THRESHOLD" "$PREV_LABEL"; then
            EXIT_CODE=1
        fi
    fi
fi

if [[ "$COMPARE_BASELINE" == true ]]; then
    echo "==> Comparing against baseline (${BASELINE_THRESHOLD}% threshold)..."
    BASELINE_LINE=$(head -1 "$RESULTS_FILE")
    BASELINE_LPS=$(echo "$BASELINE_LINE" | awk '{print $NF}')
    BASELINE_LABEL=$(echo "$BASELINE_LINE" | awk '{$1=""; $NF=""; print}' | xargs)
    if ! check_regression "$BASELINE_LPS" "$LOGS_PER_SEC" "$BASELINE_THRESHOLD" "$BASELINE_LABEL"; then
        EXIT_CODE=1
    fi
fi

exit $EXIT_CODE
