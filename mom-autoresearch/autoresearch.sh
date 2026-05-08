#!/usr/bin/env bash
set -euo pipefail

# Bench runner for MOM-adoption policy experiments.
# Expects evaluator to output JSON to mom-autoresearch/eval/results.json

ROOT_DIR="mom-autoresearch"
EVAL_SCRIPT="$ROOT_DIR/scripts/eval_mom_policy.py"
OUT_DIR="$ROOT_DIR/eval"
OUT_FILE="$OUT_DIR/results.json"

mkdir -p "$OUT_DIR"

if [[ ! -f "$EVAL_SCRIPT" ]]; then
  echo "Missing evaluator: $EVAL_SCRIPT" >&2
  exit 2
fi

python3 "$EVAL_SCRIPT" \
  --dataset "$ROOT_DIR/datasets/mom_policy_cases.jsonl" \
  --traces "$ROOT_DIR/eval/traces.jsonl" \
  --baseline "$ROOT_DIR/baseline.json" \
  --output "$OUT_FILE"

# Expected JSON keys:
# policy_score, p95_latency_ms, token_regression_pct,
# citation_compliance_rate, fabricated_claims

policy_score=$(python3 - <<'PY'
import json
p='mom-autoresearch/eval/results.json'
with open(p) as f:
    d=json.load(f)
print(d['policy_score'])
PY
)

p95=$(python3 - <<'PY'
import json
p='mom-autoresearch/eval/results.json'
with open(p) as f:
    d=json.load(f)
print(d.get('p95_latency_ms', 0))
PY
)

token_reg=$(python3 - <<'PY'
import json
p='mom-autoresearch/eval/results.json'
with open(p) as f:
    d=json.load(f)
print(d.get('token_regression_pct', 0))
PY
)

cite_rate=$(python3 - <<'PY'
import json
p='mom-autoresearch/eval/results.json'
with open(p) as f:
    d=json.load(f)
print(d.get('citation_compliance_rate', 0))
PY
)

fabricated=$(python3 - <<'PY'
import json
p='mom-autoresearch/eval/results.json'
with open(p) as f:
    d=json.load(f)
print(d.get('fabricated_claims', 0))
PY
)

echo "METRIC policy_score=${policy_score}"
echo "METRIC p95_latency_ms=${p95}"
echo "METRIC token_regression_pct=${token_reg}"
echo "METRIC citation_compliance_rate=${cite_rate}"
echo "METRIC fabricated_claims=${fabricated}"
