#!/usr/bin/env bash
set -euo pipefail

OUT_FILE="mom-autoresearch/eval/results.json"
BASELINE_FILE="mom-autoresearch/baseline.json"

[[ -f "$OUT_FILE" ]] || { echo "Missing eval output: $OUT_FILE" >&2; exit 2; }
[[ -f "$BASELINE_FILE" ]] || { echo "Missing baseline: $BASELINE_FILE" >&2; exit 2; }

python3 - <<'PY'
import json, sys
out='mom-autoresearch/eval/results.json'
base='mom-autoresearch/baseline.json'

with open(out) as f:
    r=json.load(f)
with open(base) as f:
    b=json.load(f)

# Hard fail: fabricated memory claims
if float(r.get('fabricated_claims', 0)) > 0:
    print('CHECK_FAIL fabricated memory claims > 0')
    sys.exit(1)

# Hard fail: p95 latency regression > 15%
curr=float(r.get('p95_latency_ms', 0))
base_p95=float(b.get('p95_latency_ms', 0))
if base_p95 > 0:
    reg=((curr-base_p95)/base_p95)*100.0
    if reg > 15.0:
        print(f'CHECK_FAIL p95 latency regression {reg:.2f}% > 15%')
        sys.exit(1)

print('CHECK_OK')
PY
