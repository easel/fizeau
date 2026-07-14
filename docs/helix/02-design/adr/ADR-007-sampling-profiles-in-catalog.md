---
ddx:
  id: ADR-007
  depends_on:
    - ADR-005
    - ADR-006
  review:
    self_hash: 8a70a46a9efdc916ea7e3146dce7050a12d054c1c9d56c006bb4c3245b4d4300
    deps:
      ADR-005: e47168fa6ebdb3a0f57d9a5e34cc638563f74fe5c529f73e0bee327259c7bec5
      ADR-006: 511bae45baeef8e764665393c35d5d80490ef1ed29a1efd769a063965a9e33bc
    reviewed_at: "2026-07-14T20:00:14Z"
---
# ADR-007: Sampling Profiles Belong in the Model Catalog

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-04-27 | Accepted | Fizeau maintainers | `ADR-005`, `ADR-006` | High |

## Context

The native agent harness sends no sampling fields (`temperature`, `top_p`, `top_k`, `min_p`, `repetition_penalty`) to providers by default. The wire path at `internal/provider/openai/openai.go:227-256` honors any non-nil values on `agent.Options` and silently omits the rest, but the only source feeding those options today is `providers.<name>.sampling` in user config (`cmd/agent/main.go:241-258`). For a fresh install, every request is field-omitted.

Field-omitted means the server picks. On oMLX (vidar, grendel) and on most OpenAI-compat local servers, an omitted `temperature` defaults to **0.0 — greedy / argmax decoding**. This was confirmed empirically against `Qwen3.6-27B-MLX-8bit` on both vidar:1235 and grendel:1235 on 2026-04-27: identical bodies produced bit-identical outputs across two runs, and the server's own "code" preset did not override client values when the client supplied them.

Greedy decoding combined with reasoning-mode Qwen3.x models causes deterministic tool-call loops — the same tool invocation with the same arguments emits repeatedly until the harness's loop-detector aborts. This is the failure mode visible in the 2026-04-27 harness-parity run (`benchmark-results/beadbench/run-20260427T122221Z-1549045/`): four of five local arms failed before producing output, with the one that produced output failing on `agent: identical tool calls repeated, aborting loop`.

The Qwen team's own Hugging Face model cards for the reasoning-capable Qwen3 family ([Qwen3-8B](https://huggingface.co/Qwen/Qwen3-8B), [Qwen3-30B-A3B](https://huggingface.co/Qwen/Qwen3-30B-A3B), [Qwen3-235B-A22B](https://huggingface.co/Qwen/Qwen3-235B-A22B)) include an explicit warning in the `enable_thinking=True` section: **"DO NOT use greedy decoding, as it can lead to performance degradation and endless repetitions."** Our bug is the harness doing exactly what the upstream guidance says not to.

The empirical fix is a non-greedy sampler bundle. The [Qwen3.6-27B model card](https://huggingface.co/Qwen/Qwen3.6-27B) "Best Practices" section recommends `T=0.6, top_p=0.95, top_k=20` for thinking-mode precise-coding tasks, with `presence_penalty=0` and `repetition_penalty=1.0` (i.e., both penalties disabled). The plumbing to deliver these values exists. What is missing is **policy** for where the values come from.

## Decision

### 1. Sampling is catalog policy, not user configuration

Per ADR-006, manual overrides are routing-failure signals. The same logic applies to sampling: a user reaching for `--temperature` or editing `providers.*.sampling` to make a model decode usefully is a signal that the catalog failed to carry the right defaults. The right home for sampler policy is the model catalog, exactly where `reasoning_levels` and `reasoning_control` already live.

### 2. Three resolution layers

Sampling values resolve through a precedence chain. Each layer can set any subset of fields; nil at all layers means the wire field is omitted and the server's own default applies (the "let the server handle it" case is preserved as a first-class outcome).

1. **L1 — Catalog `sampling_profiles`** (manifest top level). Named bundles keyed by use-case: `code`, eventually `creative`, `tool-loop`, `review`. The active profile is selected by the caller; v1 hardcodes `code`.

2. **L2 — Provider config (`providers.*.sampling`)** (existing). Per-(user, provider) override. Already implemented; semantics unchanged.

3. **L3 — CLI flags** (deferred, not in v1). Per-request override.

Higher layers stomp lower layers **per field, not per bundle**. If L1 sets `{T:0.6, top_p:0.95, top_k:20}` and L2 sets `{temperature: 0}`, the resolved bundle is `{T:0, top_p:0.95, top_k:20}`.

A per-`(model, profile)` override layer between L1 and L2 is **explicitly out of scope for v1**. It is recoverable additively if a real model demands a profile-divergent bundle; until then it is speculation.

### 3. Sampling composes with reasoning at the provider seam

The resolver does not know what reasoning encoding will be sent on the wire, and reasoning-mode models can want different sampler bundles than non-thinking-mode runs of the same model. To avoid the resolver and `reasoningRequestOptions` (`internal/provider/openai/openai.go:286`) silently fighting on the same request body, **the openai-compat provider is the single owner of final wire-field composition**. The provider receives the resolved sampling profile *and* the reasoning policy and is the canonical home for any future clipping or substitution rule.

For v1, the seam ships **without an active composition rule**: the seeded `code` profile (`T=0.6, top_p=0.95, top_k=20`) happens to match Qwen's published "thinking-mode precise-coding" recommendation exactly, and is also non-greedy enough to be safe in the non-thinking-mode case (slightly off the upstream non-thinking optimum, but never in the loop-inducing regime). The provider seam is established as the architectural home for `(model_family × reasoning_state × profile)` clipping; it just doesn't have a rule to apply yet. When a future profile adds a value that diverges between thinking and non-thinking states for some family, the rule lives at the seam — not in the resolver, not in the catalog.

This keeps the catalog flat (one `sampling_profiles.code` bundle, not a `code` × thinking-state matrix) and concentrates the interaction logic at one site when it appears.

### 4. Wrapped harnesses do not honor catalog sampling

Pi, codex, and claude-code drive their own SDKs and pin samplers internally. The catalog's `sampling_profiles` field is **descriptive metadata only** for runs that resolve to those harnesses; nothing flows on the wire. The resolver returns a zero-value profile when `selection.Harness != "agent"`, by contract.

`ModelEntry.sampling_control` records this state:

- `client_settable` (default) — provider honors all five fields.
- `harness_pinned` — provider drops everything; resolver short-circuits.
- `partial` (reserved, not implemented in v1) — provider honors a subset (e.g., Anthropic Messages API: `temperature`, `top_p`, `top_k` only). The honored-field list is part of the catalog entry when this state is used.

The Anthropic-direct path is `partial` in principle but ships in v1 as `client_settable` with its provider-side filter unchanged; deferring the field-list enforcement is an acceptable v1 simplification until a sampling profile is seeded that includes Anthropic-incompatible fields.

### 5. Telemetry: source-of-truth per request

`LLMRequestData` (`internal/session/event.go:31`) gains a `sampling_source` field. Values: `catalog`, `provider_config`, `cli`, or comma-separated when fields come from multiple layers. This is the routing-failure signal for sampling, mirroring ADR-006's override telemetry. Without it, post-hoc analysis cannot tell whether a degraded run used catalog defaults or a stale per-provider override.

### 6. No client-side range validation

Out-of-range values (`top_p=2.5`, `temperature=-1`) pass through unchanged. The server is the authoritative checker and will reject. We do not maintain per-server ranges.

### 7. Manifest evolution and feature degradation

The model catalog is a **versioned data artifact published on a cadence independent of binary releases** (see [`plan-2026-04-10-catalog-distribution-and-refresh.md`](../plan-2026-04-10-catalog-distribution-and-refresh.md), CONVERGED). The published source of truth lives at the project's catalog channel (`https://documentdrivendx.github.io/agent/catalog` by default); the embedded copy is a bootstrap snapshot used only when no external manifest is installed.

This decoupling drives three rules for any feature — including ADR-007 itself — that depends on new catalog data:

1. **Additive schema evolution.** New fields are added within the existing `version`. The YAML decoder operates non-strictly (verified: `internal/modelcatalog/manifest.go` does not enable `KnownFields`/`Strict` mode), so old code reading a new manifest silently ignores unknown keys, and new code reading an old manifest sees missing fields as nil/zero. Schema-version bumps are reserved for breaking changes.

2. **Graceful degradation when the installed manifest predates the feature.** Code that depends on new catalog data MUST behave correctly when those fields are absent. For sampling profiles specifically: the resolver's L1 lookup fails silently when `sampling_profiles.code` is missing, returning a zero-value bundle and falling through to L2 (provider config) or to the server's own defaults. The user does not get the new behavior, but no run breaks.

3. **`catalog_version` bumps are the user-facing pull signal.** Whenever a code release introduces a feature that depends on new catalog data, the embedded manifest's `catalog_version` MUST bump alongside it. This drives `fiz catalog check` to report an update is available. Optionally, the published bundle's `min_fizeau_version` can be raised when the manifest itself depends on new code semantics — but that direction is for protecting old binaries from new manifests, not for protecting new binaries from old manifests. (Forward-looking; no such field exists in `internal/modelcatalog/` today.)

4. **One actionable nudge per feature, not silent regressions.** A feature whose graceful-degraded behavior is materially worse than its catalog-on behavior MUST log a single, actionable warning at first-use noting that the installed manifest predates the feature and pointing at `fiz catalog update`. For ADR-007 v1: when the resolver is called with a `code` profile name and the catalog returns no profile, log once. Repeat warnings per-process are noise; per-session-per-feature is the right granularity.

Embedded manifest edits never silently override an installed external manifest. The embedded snapshot's role is bootstrap-only.

## Consequences

**Positive:**

- Native agent on Qwen3.x stops hitting greedy-decoding tool-call loops. The 2026-04-27 harness-parity failure mode is addressed by a single seeded `code` profile.
- ADR-006 invariant (overrides are signals) extends cleanly to sampling: any `provider_config` or `cli` source on `sampling_source` is a candidate datum for catalog-default tightening.
- Per-(model × profile) overrides remain available as an additive change without rewrite.

**Negative:**

- One new manifest dimension and one new struct field add catalog complexity.
- The reasoning × sampling composition rule lives at the provider seam, not in the resolver. Anyone touching `reasoningRequestOptions` must remember it co-owns wire fields with sampling. The provider seam is documented but the coupling is real.
- Wrapped-harness rows carry a sampling field that is not enforced on the wire — descriptive metadata invites confusion in catalog audits.

**Mitigations:**

- The implementation bead carries a table-driven test of the per-field merge and a reasoning-active wire test pinning Qwen3 thinking-mode composition behavior before any catalog values ship.
- `sampling_control` defaults to `client_settable`; the `harness_pinned` short-circuit is exercised in the resolver tests so the wrapped-harness contract is enforceable, not aspirational.

## Reference: per-server defaults and the T=0 footgun

The decision above ("sampling is catalog policy") is grounded in a survey of
what each server applies when the client OMITS sampler fields on a
`/v1/chat/completions` call. The empirical surprise is that defaults differ
sharply across servers — and that several local servers default to `T=0`
(greedy / argmax), which interacts badly with reasoning-mode Qwen3.x.

### Server defaults (field-omitted behavior)

| Server | Default temperature | Default top_p | Default top_k | Top-level non-OpenAI fields accepted |
|---|---|---|---|---|
| oMLX (jundot/omlx, mlx-lm) | **0.0 (greedy)** | 1.0 | 0 (off) | `top_k`, `min_p`, `repetition_penalty` YES |
| LM Studio | 1.0 | 1.0 | preset-driven (silently ignored on API path) | `top_k`, `min_p`, `repeat_penalty` YES |
| vLLM | 1.0 | 1.0 | -1 (off) | `top_k`, `min_p`, `repetition_penalty` YES |
| llama.cpp / llama-server | 0.80 | 0.95 | 40 | `top_k`, `min_p`, `repeat_penalty` YES |
| Ollama (OpenAI-compat endpoint) | 0.8 (Modelfile may override) | 0.9 | 40 (native API only) | `top_k`, `min_p`, `repetition_penalty` **NO** — must be set in Modelfile or sent via native `/api/chat` `options` |
| Lucebox (jondot/lucebox) | follows underlying mlx-lm; same **T=0 footgun** as oMLX | 1.0 | 0 (off) | (verify per release) |

oMLX and Lucebox **do not** auto-apply HuggingFace `generation_config.json`
from the model. vLLM does not unless launched with `--generation-config
auto`. llama.cpp does not (GGUF metadata only). Ollama applies Modelfile
`PARAMETER` lines.

### Recommended sampling for reasoning-mode model families (selected)

| Family | Temp | top_p | top_k | rep_pen | Source / notes |
|---|---|---|---|---|---|
| Qwen3 thinking | 0.6 | 0.95 | 20 | 1.0 | Qwen3 model card best practices. **DO NOT use greedy.** |
| Qwen3.5 thinking, code | 0.6 | 0.95 | 20 | 1.0 | Qwen3.5 card; `presence_penalty=1.5` for non-tool general work |
| Qwen3.6 thinking, code | 0.6 | 0.95 | 20 | 1.0 | Qwen3.6 card; same recipe |
| Qwen3-Coder (3.x variants) | 0.7 | 0.8 | 20 | 1.05 | Coder card; agentic mode is primary |
| MiniMax M2 / M2.x | 1.0 | 0.95 | 40 | unknown | MiniMax cards; lower T causes loops on long agent traces |
| GPT-OSS 20B / 120B | 1.0 | 1.0 | unknown | unknown | openai/gpt-oss; explicit T=1.0/top_p=1.0 |
| Llama 3.1 / 3.3 instruct | 0.6 | 0.9 | unknown | unknown | meta-llama generation.py; same recipe for tool use |
| DeepSeek-R1 | 0.6 | 0.95 | unknown | unknown | DeepSeek-R1 card — must stay 0.5–0.7; greedy explicitly forbidden |
| DeepSeek-V3 | 0.3 | unknown | unknown | unknown | DeepSeek API param guide (general preset) |
| Gemma 3 / 4 | 1.0 | 0.95 | 64 | unknown | Gemma model cards |

### Cross-cutting findings

1. **oMLX (and underlying mlx-lm) defaults to T=0.0 (greedy) when the
   client omits temperature.** This is the single biggest harness footgun
   and is the empirical origin of the deterministic tool-call loops on
   Qwen3 reasoning-mode models that motivated this ADR. Every other server
   defaults to T ≥ 0.7.
2. **Ollama's OpenAI endpoint silently drops `top_k`, `min_p`, and
   `repetition_penalty`.** Setting Qwen3-recommended `top_k=20` on the
   wire has no effect; Modelfile is the only path. The catalog resolver
   must know this when targeting Ollama.
3. **vLLM does not apply HF `generation_config.json` by default.** Without
   `--generation-config auto`, the model runs at T=1.0/top_p=1.0
   regardless of what the model card says.
4. **llama-server's defaults already include sane min_p=0.05, top_k=40,
   T=0.8** — omitting fields is much safer there than on
   oMLX/vLLM/LM Studio. Different servers fail differently.
5. **GPT-OSS recommends T=1.0/top_p=1.0** (no nucleus filtering at all),
   which is the opposite of the Qwen3 thinking recipe (T=0.6/top_p=0.95/
   top_k=20). A single one-size-fits-all harness preset will mis-serve at
   least one of these families. This is why §1 of the decision keys
   sampling to the model catalog rather than a global default.

### Source provenance

Per-server defaults table, per-family recommended sampling, the T=0
footgun finding, and the cross-cutting failure modes extracted from
`docs/research/sampling-defaults-survey-2026-04-27.md` (Sections 1, 2,
and the "5-line summary of surprising defaults"). Folded here in ADR-007
because this ADR is the policy origin for sampling-as-catalog; SD-005
covers provider config plumbing but not the per-family recommendations.

## Out of scope

- CLI flags for sampler fields (`--temperature`, etc.).
- Per-`(model, profile)` overrides on `ModelEntry`.
- Per-request profile selection (v1 hardcodes `code`).
- `partial` sampling_control enforcement (Anthropic field-list clipping).
- Range validation.
- Server-side `seed` plumbing — empirically ignored by oMLX as of 2026-04-27 (`/tmp/probe_samplers.py` log).
