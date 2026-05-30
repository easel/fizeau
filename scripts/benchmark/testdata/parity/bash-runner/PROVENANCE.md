# Bash Runner Provenance

This directory contains parity fixture cell reports from the bash runner (scripts/benchmark/benchmark).

## Fixtures

These are representative cells from three benchmark tasks, run with the bash runner:

- `cancel-async-tasks/` — cell report
- `configure-git-webserver/` — cell report
- `log-summary-date-ranges/` — cell report

## Generation

Generated from the same canary as go-runner fixtures (sindri-lucebox + tb-2-1-canary, 3 reps).

The diff.sh script compares these bash-runner fixtures against go-runner fixtures, filtering out allowlisted field divergences per ALLOWLIST.md.

## Parity Assurance

Before the Go runner (cmd/bench) deactivation commit, these fixtures must match the go-runner baselines per diff.sh (allowlisted divergences only).
