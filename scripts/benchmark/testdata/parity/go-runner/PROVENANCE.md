# Go Runner Baseline

This directory contains parity fixture cell reports from the Go runner (cmd/bench).

## Fixtures

These are representative cells from three benchmark tasks, run with the Go runner:

- `cancel-async-tasks/` — cell report
- `configure-git-webserver/` — cell report  
- `log-summary-date-ranges/` — cell report

## Generation

Generated from sindri-lucebox + tb-2-1-canary, 3 reps, committed as the baseline for parity comparison.

Before deactivation of the Go runner, these fixtures were run through the bash runner and committed to bash-runner/ with the same structure.

The diff.sh script compares these go-runner fixtures against bash-runner fixtures, filtering out allowlisted field divergences per ALLOWLIST.md.
