<bead-review>
  <bead id="fizeau-2b024ccf" iter=1>
    <title>Integrate v0.10.9 point release</title>
    <description>
Context: latest published release tag before this work is v0.10.8. A local annotated tag v0.10.9 was created on master at commit 86cfbee64e51c3e38147918435898e44a5ecfd3f with message Release v0.10.9, but it has not been pushed yet. The working tree also had unrelated DDx tracker/attachment changes at the time this bead was filed; do not revert user or tracker work that is not required for the release integration.

Goal: complete the v0.10.9 point-release integration using the repo release conventions. Ensure the release notes/changelog and any release metadata accurately describe the code shipped since v0.10.8, verify the tag points at the intended commit, and push the release branch/tag only after local verification.

In-scope files/actions:
- CHANGELOG.md if the release entry is missing or incomplete.
- Release metadata/scripts only if existing project conventions require them.
- Git refs for master and tag v0.10.9.

Out-of-scope:
- Do not close or mutate unrelated manual benchmark beads.
- Do not rewrite, rebase, squash, amend, or filter execute-bead history.
- Do not include unrelated uncommitted tracker/attachment changes unless they are required release evidence.
    </description>
    <acceptance>
1. `git tag -n99 v0.10.9` shows an annotated release tag for v0.10.9 and `git rev-list -n1 v0.10.9` resolves to the intended release commit.
2. `git log --oneline v0.10.8..v0.10.9` contains the release changes intended for v0.10.9 and no rewritten execute-bead history.
3. `CHANGELOG.md` contains an accurate v0.10.9 entry dated 2026-05-06, or the bead notes explicitly justify why no changelog change is required under this repo release convention.
4. `go test ./...` passes locally before pushing the release tag.
5. `git ls-remote --tags origin v0.10.9` shows the pushed tag, and `git ls-remote origin master` contains the tagged commit or a descendant.
6. Close the bead only with evidence listing the tag SHA, pushed remote refs, and verification commands.
    </acceptance>
    <notes>
evidence: tag object SHA e42a6d36ac68f4615fb17f27f3e37838ca8f1587; release commit SHA 29a12eea5f9ac902bb1f0525a5697ab8833bd9e6; remote refs after push: refs/tags/v0.10.9 -&gt; e42a6d36ac68f4615fb17f27f3e37838ca8f1587, refs/heads/master -&gt; 29a12eea5f9ac902bb1f0525a5697ab8833bd9e6; verification commands: git tag -n99 v0.10.9, git rev-list -n1 v0.10.9, git log --oneline v0.10.8..v0.10.9, go test ./..., git ls-remote --tags origin v0.10.9, git ls-remote origin master
    </notes>
    <labels>area:release, kind:release</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="136a56e1ead7e04a72285ac6ef32474573f4832b">
<untrusted-data>
commit 136a56e1ead7e04a72285ac6ef32474573f4832b
Merge: 8154767 29a12ee
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 00:55:14 2026 -0400

    Merge bead fizeau-2b024ccf attempt 20260506T045046- into master
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
