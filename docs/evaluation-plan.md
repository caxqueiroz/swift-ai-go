# Address Resolution Evaluation Plan

This document describes how to measure resolver and cache quality using:

- **DataAddr SQLite** shards (curated addresses at scale)
- **CRF + GeoNames pipeline** (production Stage 2)
- **Two LLMs** (e.g. Claude + OpenAI) as offline labeler/judge
- **`examples/client`** and **`iso-cache-fill`** (existing tooling)

Related runbooks:

- [offline-cache-and-evaluation.md](./offline-cache-and-evaluation.md) — cache build, API modes, DataAddr evaluator basics
- [README.md](../README.md) — commands and environment variables

---

## Goals

Answer four separate questions with numbers, not one blended “accuracy”:

| Question | Primary metric |
|----------|----------------|
| Is the **live resolver** good enough? | **Precision@committed** on holdout (`resolved` + `high`) |
| How much traffic can we **auto-commit**? | **Coverage** (% `resolved` + `high`) |
| Does **offline LLM** help the cache? | **Precision when labeler+judge agree** vs SQLite / gold |
| Is **Stage 1 cache** safe? | **False cache hit rate** on holdout (must be ~0) |

**99.99% production SLO** means: among answers you treat as final (`resolved` + `high`), wrong country **or** town happens ≤ 1 in 10,000. That is **precision@committed**, not raw accuracy on every row.

DataAddr SQLite labels are **weak ground truth**:

- `country` is usually reliable
- `town` may mean city, district, suburb, or a source-specific label (not always GeoNames locality)

Use SQLite for regression, coverage, and system comparison. Use a smaller **human-verified** (or LLM+GeoNames adjudicated) set to validate the SLO.

---

## Assets

| Asset | Role in eval |
|-------|----------------|
| DataAddr SQLite (`address_shards/*.sqlite`) | Stratified samples, volume, per-country metrics |
| CRF + GeoNames (`iso-api` without `DATABASE_URL`, or `iso-run`) | Production resolver (Track A) |
| Postgres + pgvector cache | Cache behavior only (Track F); never mix with resolver eval on same rows |
| LLM A (e.g. Claude) | Offline labeler or judge |
| LLM B (e.g. OpenAI) | Offline judge (different model from labeler) |
| `examples/client` | Calls `POST /convert`, compares country/town to SQLite |
| `iso-cache-fill` | Offline CRF + optional single judge; `--dry-run` for no DB writes |

---

## Metrics glossary

### Per row

- **country_ok** — canonical SQLite `country` == API `structured.country`
- **town_ok** — canonical SQLite `town` == API `structured.town` (see `canonicalTown` in `examples/client`)
- **both_ok** — `country_ok` && `town_ok`
- **committed** — `resolution_status == "resolved"` AND `confidence_band == "high"`

### Aggregates

| Metric | Formula | Use |
|--------|---------|-----|
| **Raw country / town / both accuracy** | correct / N | Directional; includes wrong high-confidence answers |
| **Coverage** | committed / N | How much you auto-answer |
| **Precision@committed** | both_ok among committed / committed | **Primary SLO metric** |
| **Country precision@committed** | country_ok among committed / committed | Country-only SLO |
| **False positive rate (committed)** | 1 − precision@committed | Wrong auto-answers |

The evaluator JSON summary reports metrics **overall** and by **stratum** (`common` / `medium` / `rare` towns). Run one country per command for country-level scorecards. Use `--log-all` JSONL to post-process additional cuts by **`served_by`** and **`input_mode`**.

---

## Step 0: Fixed holdout (required)

Never cache-fill and evaluate the same rows unless measuring cache lookup only.

### Option A — Deterministic ID split (recommended)

```text
cache-fill:  stable_hash(address_id) % 10 in [0, 8)   (80%)
eval holdout: stable_hash(address_id) % 10 in [8, 10)  (20%)
```

Use matching flags:

```bash
# Cache-fill complement: buckets 0-7.
task cache-fill:dataaddr-local \
  COUNTRY=CL \
  HOLDOUT_MOD=10 \
  HOLDOUT_MIN_BUCKET=0 \
  HOLDOUT_MAX_BUCKET=8

# Eval holdout: buckets 8-9.
task eval:dataaddr-client \
  COUNTRY=CL LIMIT=20000 INPUT_MODE=address \
  TIMEOUT=0 REQUEST_TIMEOUT=10m LOG_ALL=1 HOLDOUT_MOD=10 HOLDOUT_MIN_BUCKET=8 \
  MISMATCHES=/tmp/eval-cl-address.jsonl
```

`examples/client` buckets SQLite `addresses.id`. `iso-cache-fill` buckets the optional CSV/TSV `id` column when present and falls back to input text for sources that do not carry IDs. A materialized manifest is still useful for auditability, but the shared bucket flags are enough to keep cache-fill and eval separated.

### Option B — Fixed evaluator seed

- Eval: `examples/client --seed 42`
- Cache-fill: different seed or explicit ID list

Document which countries and seeds each run used.

### SQLite index (per shard)

```bash
for c in cl sg us ae at; do
  sqlite3 /Volumes/cax-t7/Data/DataAddr/address_shards/$c.sqlite \
    "CREATE INDEX IF NOT EXISTS idx_addresses_eval_country_town_id \
     ON addresses(country, town, id) \
     WHERE town IS NOT NULL AND town <> '';"
done
```

---

## Input modes

| `input-mode` | Sent to `/convert` | What it measures |
|--------------|-------------------|------------------|
| `address` | Street lines only | **Production-like** (hardest) |
| `address-town` | address + town | Town normalization + country inference |
| `address-town-country` | address + town + country | **Town matching diagnostic** (easiest) |

Public API is text-only. **`address`** is what matters for real traffic; other modes isolate failure modes.

---

## Evaluation tracks

### Track A — CRF + Geo baseline (resolver only)

**Purpose:** Measure Stage 2 without cache pollution.

**API setup** — no `DATABASE_URL`, strict thresholds:

```bash
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
EMBEDDING_BACKEND=onnx \
go run ./cmd/iso-api \
  --resources-dir resources \
  --model-dir resources/models \
  --high-confidence-threshold 0.99 \
  --medium-confidence-threshold 0.80
```

**Run evaluator** (per country):

```bash
task eval:dataaddr-client \
  SQLITE=/Volumes/cax-t7/Data/DataAddr/address_shards/cl.sqlite \
  COUNTRY=CL LIMIT=20000 SEED=42 \
  INPUT_MODE=address \
  TIMEOUT=0 REQUEST_TIMEOUT=10m LOG_ALL=1 HOLDOUT_MOD=10 HOLDOUT_MIN_BUCKET=8 \
  MISMATCHES=/tmp/eval-cl-address.jsonl

task eval:dataaddr-client \
  SQLITE=/Volumes/cax-t7/Data/DataAddr/address_shards/cl.sqlite \
  COUNTRY=CL LIMIT=20000 SEED=42 \
  INPUT_MODE=address-town-country \
  TIMEOUT=0 REQUEST_TIMEOUT=10m LOG_ALL=1 HOLDOUT_MOD=10 HOLDOUT_MIN_BUCKET=8 \
  MISMATCHES=/tmp/eval-cl-atc.jsonl
```

**Outputs:** `country_accuracy`, `town_accuracy`, `both_accuracy`, `coverage`, `precision_at_committed_*`, `false_positive_rate_committed`, and `metrics.strata` in JSON stdout. With `LOG_ALL=1`, the JSONL file contains every evaluated row for post-processing.

Repeat for: **CL, SG, US** (volume) + one hard country (**AE** or **AT**).

---

### Track B — Threshold sweep

**Purpose:** Coverage vs precision tradeoff.

Run Track A with:

| `high-confidence-threshold` | Notes |
|----------------------------|--------|
| 0.95 | Default |
| 0.99 | Precision-first (documented for low FP) |
| (optional) 0.995 | If coverage still too high with wrong commits |

Same holdout, same `input_mode=address`. Plot coverage vs precision@committed.

---

### Track C — Single LLM judge (offline)

**Purpose:** Measure what would be cached as `llm_assisted` before enabling online trust.

**Flow:**

1. Export holdout rows from SQLite → CSV with `id` and `address` columns (and optional batch-only country hints for `iso-run`, not `/convert`).
2. Run pipeline + judge (no cache writes):

```bash
./bin/iso-cache-fill --dry-run \
  --input-path /tmp/holdout-cl.csv \
  --resources-dir resources \
  --model-dir resources/models \
  --embedding-backend onnx \
  --enable-llm-judge \
  --judge-model "$JUDGE_MODEL" \
  --judge-api-key "$OPENAI_API_KEY" \
  --high-confidence-threshold 0.99 \
  --medium-confidence-threshold 0.80 \
  --review-path /tmp/review-cl-judge.json
```

**Metrics:**

- Judge **precision** vs SQLite when judge resolves
- **Uplift:** CRF wrong → judge right
- **Harm:** CRF right → judge wrong
- **Review rate:** `judge_unresolved` / total medium-band rows

Judge is constrained to GeoNames candidates (`internal/judge.ValidateDecision`).

---

### Track D — Dual LLM (labeler + judge)

**Purpose:** Estimate safety of agreement-gated cache writes.

**Flow** (per holdout row):

```text
1. CRF + Geo → top country/town candidates
2. Labeler (LLM A): propose {country, town} from candidates
3. Judge (LLM B): verify or alternate pick from candidates
4. Programmatic: ValidateDecision + require agreement
5. Score vs SQLite; log disagreements for review
```

**Use different models** (e.g. Claude labeler, OpenAI judge).

| Metric | Meaning |
|--------|---------|
| Labeler accuracy | vs SQLite |
| Judge accuracy | vs SQLite |
| Agreement rate | labeler == judge |
| **Precision when agree** | vs SQLite when both agree (proxy for cache safety) |
| Disagreement rate | Should → human review, not cache |

*Not yet in-repo; implement as `cmd/iso-eval-llm` or batch script over exported candidates.*

---

### Track E — Gold / oracle slice (calibrate SQLite)

**Purpose:** Only track where 99.99% can be claimed honestly.

**Sample:** 500–2000 stratified rows where CRF, SQLite, and/or LLMs disagree.

**Protocol:**

- Both LLMs pick only from GeoNames candidates (same constraint as judge).
- Both LLMs agree + SQLite agrees → silver label (likely correct).
- Both LLMs agree, SQLite differs → spot-check 50 (often locality definition mismatch).
- LLMs disagree → human label → `human_verified` gold set.

Grow this set over time; measure precision@committed on it separately.

---

### Track F — Cache eval (separate)

**Purpose:** Cache hit rate, latency, false cache hits — not resolver quality.

**Only after** holdout IDs are excluded from cache-fill.

```bash
# Fill on 80% (not holdout)
task cache-fill:dataaddr-local \
  COUNTRY=CL \
  HOLDOUT_MOD=10 \
  HOLDOUT_MIN_BUCKET=0 \
  HOLDOUT_MAX_BUCKET=8

# API with DATABASE_URL; eval holdout 20%
task eval:dataaddr-client \
  SQLITE=.../cl.sqlite COUNTRY=CL LIMIT=5000 SEED=42 \
  INPUT_MODE=address \
  TIMEOUT=0 REQUEST_TIMEOUT=10m LOG_ALL=1 HOLDOUT_MOD=10 HOLDOUT_MIN_BUCKET=8 \
  MISMATCHES=/tmp/eval-cl-cache.jsonl
```

**Metrics:**

- Cache hit rate (`served_by == stage1_cache`)
- Precision@committed on cache hits
- **False cache hit:** stage1 served but SQLite country/town wrong (critical)

Do not enable `--trust-llm-assisted` until Track C/D holdout numbers are acceptable.

---

## Eval matrix (recommended order)

| # | System | Input mode | N / country | Primary metrics |
|---|--------|------------|-------------|-----------------|
| 1 | CRF API, no cache | `address` | 10k–50k | precision@committed, coverage, strata |
| 2 | CRF API, no cache | `address-town-country` | 10k | town accuracy (diagnostic) |
| 3 | CRF @ 0.95 vs 0.99 | `address` | 10k | coverage vs precision |
| 4 | Single LLM judge | holdout CSV | medium-band | judge precision, uplift/harm |
| 5 | Dual LLM | same | medium-band | precision when agree |
| 6 | Cache enabled | `address` | holdout 20% | false cache hit, hit rate |

---

## Analysis: precision@committed

The evaluator writes only **mismatches** to JSONL by default. Add `--log-all` (or `LOG_ALL=1` through Task) to write every row. Each row includes `status`, `band`, `served_by`, `input_mode`, `expected_*`, `actual_*`, and `stratum`.

Example mismatch row:

```json
{
  "id": "...",
  "address": "6 LAS GAVIOTAS\nCURACO DE VELEZ\nCL",
  "input_mode": "address-town-country",
  "expected_country": "CL",
  "expected_town": "CURACO DE VELEZ",
  "actual_country": "CL",
  "actual_town": "CURACO",
  "served_by": "stage2_pipeline",
  "status": "needs_review",
  "band": "medium"
}
```

The JSON summary already computes committed metrics. Use this pseudocode only when post-processing full JSONL from `--log-all`:

```text
for each row:
  country_ok = canonical(expected_country) == canonical(actual_country)
  town_ok    = canonical(expected_town) == canonical(actual_town)
  both_ok    = country_ok and town_ok
  committed  = (status == "resolved" and band == "high")

  N += 1
  if both_ok: raw_correct += 1
  if committed:
    committed_n += 1
    if both_ok: committed_correct += 1

raw_both_accuracy       = raw_correct / N
coverage                = committed_n / N
precision_at_committed  = committed_correct / committed_n
```

**Interpretation:**

| Pattern | Meaning |
|---------|---------|
| Low raw accuracy, high precision@committed, low coverage | Safe abstention; tune thresholds for more coverage |
| High raw accuracy, low precision@committed | **Dangerous** — do not trust `resolved` fields |
| High precision@committed on gold set, lower on SQLite | SQLite locality mismatch; trust gold |

---

## Scorecard template

Per country, per run (`date`, `git_sha`, `threshold`, `input_mode`):

```markdown
## CL — address — 2026-05-28 — threshold 0.99

| Metric | Value |
|--------|-------|
| N | 20000 |
| Raw both accuracy | |
| Coverage (resolved+high) | |
| Precision@committed (both) | |
| Precision@committed (country) | |
| Strata: common both acc | |
| Strata: rare both acc | |
| Notes | |
```

---

## Decision diagram

```text
                         high coverage
                              ▲
                              │
              noisy auto      │     target zone
              (high raw acc,  │     (high precision@committed,
               low prec@commit)│      acceptable coverage)
                              │
              abstain most    │
              (high prec@commit,│
               low coverage)  │
                              └──────────────────────────────►
                                   high precision@committed
```

**Ship threshold tuning** from Track B.  
**Ship dual-LLM cache policy** from Track D (`precision when agree`).  
**Ship online cache** from Track F (false cache hit ≈ 0).  
**Ship per-country auto-commit** only where Track A `address` mode meets SLO on holdout.

---

## One-week schedule

| Day | Task |
|-----|------|
| 1 | Holdout rule; index shard DBs; document seeds |
| 2 | Track A: `address` mode, CL/SG/US, 20k each |
| 3 | Track B: 0.95 vs 0.99; compute precision@committed |
| 4 | Export medium-band holdout; Track C (judge); Track D (dual LLM) |
| 5 | Track E: 500 disagreements, 50 human labels |
| 6 | Track F cache (if A–D acceptable) |
| 7 | Per-country scorecard + go/no-go |

---

## Tooling gaps (recommended follow-ups)

| Item | Benefit |
|------|---------|
| Shared **holdout manifest** (`id` list) | Auditable split across client + `iso-cache-fill` |
| `cmd/iso-eval-llm` | Batch labeler+judge over exported candidates |
| Report script | One command: JSONL → metrics table by stratum/country |

---

## Operating rules (eval hygiene)

1. **Resolver eval:** API without `DATABASE_URL`.
2. **Cache eval:** holdout never in cache-fill; separate report.
3. Do not treat DataAddr `both_accuracy` as 99.99% proof without gold subset.
4. Clients must treat only `resolved` + `high` as authoritative (or API should omit fields otherwise).
5. Restrict Stage 2 **write-back** to high-confidence rows when measuring cache quality over time.
6. Do not enable `--trust-llm-assisted` until offline judge/dual-LLM holdout metrics pass.

---

## Quick reference commands

**Resolver-only API:**

```bash
ISO20022_ONNX_RUNTIME=$PWD/.onnxruntime/onnxruntime-osx-arm64-1.26.0/lib/libonnxruntime.1.26.0.dylib \
EMBEDDING_BACKEND=onnx \
go run ./cmd/iso-api --resources-dir resources --model-dir resources/models \
  --high-confidence-threshold 0.99 --medium-confidence-threshold 0.80
```

**Eval:**

```bash
task eval:dataaddr-client \
  SQLITE=/path/to/address_shards/cl.sqlite \
  COUNTRY=CL LIMIT=20000 SEED=42 \
  INPUT_MODE=address \
  TIMEOUT=0 REQUEST_TIMEOUT=10m LOG_ALL=1 HOLDOUT_MOD=10 HOLDOUT_MIN_BUCKET=8 \
  MISMATCHES=/tmp/eval-cl.jsonl
```

**Offline judge (dry-run):**

```bash
./bin/iso-cache-fill --dry-run \
  --input-path /tmp/holdout.csv \
  --resources-dir resources --model-dir resources/models \
  --embedding-backend onnx --enable-llm-judge \
  --judge-model "$JUDGE_MODEL" --judge-api-key "$OPENAI_API_KEY" \
  --high-confidence-threshold 0.99 \
  --review-path /tmp/review.json
```
