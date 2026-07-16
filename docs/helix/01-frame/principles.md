---
ddx:
  id: helix.principles
  depends_on:
    - helix.product-vision
  review:
    self_hash: 5856324a09b90b09eb20a7111c81f2eacb3f780f2c2c559e9326386a7554979f
    deps:
      helix.product-vision: eb5af3663734d35e7b42963ce12e39adc19147aa2df25fe9bd3887793217836c
    reviewed_at: "2026-07-16T07:25:15Z"
---
# Product Principles — Fizeau

1. **The embeddable runtime is the product.** Public behavior begins at
   `fizeau.New(...)` and `FizeauService.Execute`; commands and websites prove or
   explain that contract rather than defining parallel runtimes.
2. **Measurement is part of execution.** A run is not complete unless its LLM
   turns, tool attempts, timing, token streams, route identity, and cost
   semantics can be inspected and replayed.
3. **Local and cloud are provider systems, not product tiers.** Both pass
   through the same request, capability, evidence, and result contracts.
4. **Responsibility boundaries stay explicit.** Fizeau selects and executes one
   attributable route. Callers own task meaning, semantic retries, and
   cross-harness escalation.
5. **Evidence outranks claims.** Catalog facts carry provenance and freshness;
   unknown values stay unknown; benchmark cells are self-describing.
