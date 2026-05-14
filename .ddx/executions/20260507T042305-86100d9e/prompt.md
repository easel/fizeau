<bead-review>
  <bead id="fizeau-1bbe2973" iter=1>
    <title>Define TerminalBench 2.1 sweep lanes and resource groups</title>
    <description>
Define the concrete TerminalBench 2.1 sweep matrix before changing runners. The desired full sweep is an experimental design to answer three comparison questions:

1. Provider/quant deltas: how "the same" model family behaves across providers, endpoints, and quantization/configs, especially qwen3.6-27b across local providers such as Vidar oMLX, Bragi LM Studio, and bragi-club-3090 vLLM/autoround. These rows must preserve provider, endpoint, quant/config, context, sampling, and runtime metadata so differences are not collapsed into a misleading single model name.
2. Harness deltas: how different harnesses perform with the same intended underlying model/provider where that is technically meaningful. The plan must distinguish true same-model comparisons from harness-native cases where the harness controls model selection.
3. Frontier harness-vs-fiz deltas: how frontier model+harness combinations compare against fiz using the same model surface where possible, specifically Sonnet via OpenRouter/fiz native vs fiz Claude harness on Sonnet, and GPT-5.4-mini via OpenRouter/fiz native vs fiz Codex harness on GPT-5.4-mini.

Meta goal: make fiz the strongest agentic harness for supported workloads and build evidence for model selection calculus across cost, speed, quality, reliability, and invalid-run behavior.

The sweep is staged, not one undifferentiated cross product:
1. Canary: small 2.1 task subset proving every intended provider/harness lane can start, call its provider, write session/trajectory artifacts, and classify invalid cells separately from graded failures.
2. Local Qwen sweep: local model providers × supported fiz/harness lanes, all running qwen3.6-27b-family models. Expected local providers include Vidar oMLX, Bragi local endpoint(s), and bragi-club-3090 vLLM if reachable. Local endpoints must be grouped by server/base_url so the runner can serialize or cap concurrency per provider.
3. Sonnet comparison: fiz native/provider lane on Sonnet via OpenRouter versus fiz Claude harness lane on Sonnet.
4. GPT comparison: fiz native/provider lane on GPT-5.4-mini via OpenRouter versus fiz Codex harness lane on GPT-5.4-mini.

The output of this bead is a checked-in lane/resource specification that implementation agents can use without chat context. It should explicitly list lane ids, profile ids, provider endpoints, harness pins, model ids, model-family/equivalence labels, quant/config labels, phase names, default reps, allowed concurrency, budget/rate-limit knobs, and what can run in parallel.

In scope:
- docs/research or scripts/benchmark config describing phases/lanes/resources/comparison groups
- updates to the TerminalBench 2.1 migration bead notes if needed

Out of scope:
- runner implementation
- paid/full benchmark execution
- changing historical TB-2.0 fixtures
    </description>
    <acceptance>
1. A checked-in plan/config lists the four phases: canary, local-qwen, sonnet-comparison, gpt-comparison.
2. The plan defines comparison groups for provider/quant deltas, harness deltas, and frontier harness-vs-fiz deltas.
3. The local-qwen phase lists every intended local qwen3.6-27b-family provider and marks each with model_family, exact served model id, quant/config label where known, provider surface, endpoint, and resource_group keyed by server/base_url.
4. The plan distinguishes fiz native/provider lanes from fiz harness-pinned lanes and names the exact FIZEAU_PROVIDER/FIZEAU_HARNESS/FIZEAU_MODEL/FIZEAU_BASE_URL inputs for each lane.
5. The plan identifies which comparisons are true same-model/provider comparisons and which are approximate same-family/provider-or-quant comparisons, so claims cannot overstate equivalence.
6. The plan states default concurrency: local resource groups default to one active cell per provider endpoint unless explicitly overridden; managed OpenRouter lanes use budget/rate caps and may run in parallel when provider limits allow.
7. The plan defines resume semantics: rerun only missing, nonterminal, or explicitly retryable cells; never rerun completed graded/invalid cells unless force is set.
8. The plan identifies metrics required for model-selection calculus: pass rate/reward, invalid class counts, wall time, token counts, cost, effective cost per valid/pass, context/sampling/quant metadata, and session/trajectory provenance.
9. The plan identifies which existing profiles can be reused and which new profiles/config files must be created.
10. rg over the new plan finds terminal-bench/terminal-bench-2-1 and does not relabel historical TB-2.0 artifacts.
    </acceptance>
    <labels>area:benchmark, area:terminalbench, kind:planning, phase:design</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="d4f591d21ecf7aa04e4be48c7a8ae1f450d2ff32">
<untrusted-data>
commit d4f591d21ecf7aa04e4be48c7a8ae1f450d2ff32
Merge: 513f3d2d 544d4b1d
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Thu May 7 00:23:01 2026 -0400

    Merge bead fizeau-1bbe2973 attempt 20260507T040716- into master
</untrusted-data>
  </diff>

  <instructions>
You are reviewing a bead implementation against its acceptance criteria.

For each acceptance-criteria (AC) item, decide whether it is implemented correctly, then assign one overall verdict:

- APPROVE — every AC item is fully and correctly implemented.
- REQUEST_CHANGES — some AC items are partial or have fixable minor issues.
- BLOCK — at least one AC item is not implemented or incorrectly implemented; or the diff is insufficient to evaluate.

## Required output format (schema_version: 1)

Respond with EXACTLY one JSON object as your final response, fenced as a single ```json … ``` code block. Do not include any prose outside the fenced block. The JSON must match this schema:

```json
{
  "schema_version": 1,
  "verdict": "APPROVE",
  "summary": "≤300 char human-readable verdict justification",
  "findings": [
    { "severity": "info", "summary": "what is wrong or notable", "location": "path/to/file.go:42" }
  ]
}
```

Rules:
- "verdict" must be exactly one of "APPROVE", "REQUEST_CHANGES", "BLOCK".
- "severity" must be exactly one of "info", "warn", "block".
- Output the JSON object inside ONE fenced ```json … ``` block. No additional prose, no extra fences, no markdown headings.
- Do not echo this template back. Do not write the words APPROVE, REQUEST_CHANGES, or BLOCK anywhere except as the JSON value of the verdict field.
  </instructions>
</bead-review>
