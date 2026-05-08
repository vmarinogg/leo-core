# MOM Adoption Autoresearch Session

## Objective
Increase MOM usage quality in Pi harness behavior policy.

Primary metric: `policy_score` (higher is better).

## Scope (mutable)
- Prompt snippets
- Harness routing rules
- MOM call heuristics

Out of scope:
- MOM backend/query engine changes

## policy_score v1 (0-100)
- 35 pts: MOM invoked when needed
  - `mom_status` at session start
  - `mom_recall` before memory-dependent claims
- 25 pts: citation compliance on memory-derived claims
- 20 pts: memory-claim correctness (no fabricated recall)
- 20 pts: user outcome proxy (hybrid deterministic + LLM judge)

## Hard/Soft gates
- Hard fail: fabricated memory claim
- Soft penalty: missing citation
- Hard fail: p95 latency regression > 15% vs baseline
- Token regression: soft penalty (manual future switch to hard gate)

## Dataset v1
50 cases:
- 20 memory-critical
- 15 memory-optional
- 15 memory-irrelevant

## Keep/Discard policy
- Compare against best-ever and rolling median
- Confidence:
  - >=2.0x noise: keep if improved
  - 1.0x-2.0x: rerun once before decision
  - <1.0x: discard unless strategic exploration

## Session stop conditions
- Max 60 runs
- Or plateau: 15 runs without best improvement

## Coverage strategy
- Optimize on Pi first
- Transfer-test other harnesses after convergence

## Transfer gates
- No fabrication hard-fails
- Citation compliance within -3% of Pi baseline
- `policy_score` drop <=5%
- p95 latency regression <=15%

## Reporting
At end, generate final report with:
- Best configs
- Failure patterns
- Tradeoffs (quality/latency/tokens)
- Transfer readiness notes
