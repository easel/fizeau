<bead-review>
  <bead id="fizeau-d5e91885" iter=1>
    <title>Implement versioned FHI formula and claim generator</title>
    <description>
After records exist in the benchmark evidence ledger, Fizeau needs a versioned FHI derivation and claim generator that can produce defensible statements like local oMLX Qwen is 6 FHI points behind Opus, or fiz-native is 0.7 TerminalBench points below Claude Code on the same model/subset. This slice owns report derivation from ledger records, not importers.

In-scope files:
- cmd/bench fhi or evidence claim subcommands
- FHI formula/config file under scripts/benchmark or docs/research as appropriate
- tests with small ledger fixtures covering local-vs-frontier, harness-vs-harness, and insufficient-coverage refusal

Out-of-scope:
- changing benchmark runners
- importing new evidence sources
- changing catalog model power generation directly

Implementation notes:
- FHI formula must be versioned, list included benchmarks/weights, evidence window, exclusion rules, invalid-run denominator handling, and confidence/coverage notes.
- Claim generator must refuse or confidence-penalize comparisons when benchmark coverage, formula version, evidence window, scorer, or denominator rules differ.
- Support benchmark-specific deltas and cross-benchmark FHI rankings separately.
    </description>
    <acceptance>
1. A ledger fixture produces a TerminalBench-specific delta claim with pinned model/provider/benchmark/subset/scorer/reps and source artifact hashes.
2. A ledger fixture produces a local-vs-frontier FHI claim including Fizeau version, local runtime version, quantization, hardware, provider endpoint, formula version, included benchmarks, and delta.
3. A fixture with mismatched benchmark coverage causes the claim generator to refuse the comparison or emit an explicit confidence penalty.
4. Output includes invalid-run counts/classes and denominator handling.
5. `go test ./cmd/bench` passes.
    </acceptance>
    <labels>area:benchmark, area:fhi, kind:task, phase:build</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="4ce28f6475e5e63507aca16d0be873d25885cc3f">
<untrusted-data>
commit 4ce28f6475e5e63507aca16d0be873d25885cc3f
Merge: 7a7631d9 93f6b435
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 21:52:59 2026 -0400

    Merge bead fizeau-d5e91885 attempt 20260507T013846- into master
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
