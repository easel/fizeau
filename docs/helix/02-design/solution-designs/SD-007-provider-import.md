---
ddx:
  id: SD-007
  depends_on:
    - SD-005
  review:
    self_hash: 86409bb8c20bb697653c495f9c44b15070518356e06c5005e52616676827da67
    deps:
      SD-005: e0acdb5a9db144a415aa5831485fe198aa3f9c7fdf0ac7d100f5a01a117df1a0
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Solution Design: SD-007 — Provider Import from Pi and OpenCode

## Problem

Users of pi and opencode have already configured their LLM providers — API
keys, LM Studio endpoints, custom model definitions. Fizeau shouldn't require
them to duplicate this work. But runtime coupling to other tools' config
formats creates fragile dependencies and maintenance burden.

## Design: Import-Time Translation

Fizeau reads other tools' configs at **import time** (explicit user action),
translates them to Fizeau-native `.fizeau/config.yaml` (per SD-005 schema),
and records the import source so it can detect drift later.

### CLI Commands

```
fiz import pi              # import from ~/.pi/agent/{auth,settings,models}.json
fiz import opencode         # import from ~/.local/share/opencode/auth.json + opencode.json
fiz import pi --diff        # show what pi has that Fizeau doesn't (dry run)
fiz import opencode --diff  # same for opencode
fiz import pi --merge       # merge new providers without overwriting existing
```

### Zero-Config Discovery

When fiz starts with no providers configured (no `.fizeau/config.yaml`, no
`~/.config/fizeau/config.yaml`, no `FIZEAU_*` env vars, no standard API key env
vars), it checks for importable configs and shows a notice:

```
fiz: no providers configured. Found pi config at ~/.pi/agent/ — run 'fiz import pi' to import.
```

This is a one-line stderr notice, not an error. Fizeau still runs if env vars
provide a usable provider.

### Import Sources

#### Pi (`fiz import pi`)

**Reads:**
- `~/.pi/agent/auth.json` — OAuth tokens and API keys per provider
- `~/.pi/agent/settings.json` — defaultProvider, defaultModel
- `~/.pi/agent/models.json` — custom provider definitions (LM Studio, Ollama)

**Two-source merge algorithm:**

Pi splits config across auth.json (credentials) and models.json (endpoints).
The import merges them:

1. Start with models.json providers — these have `baseUrl` and model IDs
2. For each models.json provider, look up the matching auth.json entry for
   credentials. If models.json has its own `apiKey`, use that (local providers
   like LM Studio use placeholder keys like `"lmstudio"`)
3. For auth.json entries with NO matching models.json provider (e.g.,
   `anthropic`, `openai-codex`, `openrouter`), create fiz providers using
   well-known defaults (built-in URL, type mapping)
4. Apply settings.json `defaultProvider` + `defaultModel` for the `default:`
   field, mapping pi provider names to the Fizeau provider name

**Pi provider name → Fizeau provider mapping:**

| Pi auth name | Fizeau name | Concrete type | Default URL | Notes |
|-------------|------------|------|-------------|-------|
| `anthropic` | `anthropic` | `anthropic` | (SDK default) | OAuth access token as API key |
| `openai-codex` | `openai` | `openai` | `https://api.openai.com/v1` | OAuth access token as bearer |
| `openrouter` | `openrouter` | `openrouter` | `https://openrouter.ai/api/v1` | API key from auth.json |
| `qwen` / `dashscope` | `qwen` | `qwen` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | Qwen/DashScope cloud API; both aliases map to the same Fizeau provider |
| `minimax` | `minimax` | `minimax` | `https://api.minimaxi.chat/v1` | MiniMax cloud API |
| `z.ai` / `zai` | `z.ai` | `zai` | `https://api.z.ai/v1` | both aliases map to the same Fizeau provider |
| `google-gemini-cli` | skipped | — | — | Not yet supported |
| `github-copilot` | skipped | — | — | Proprietary auth flow |
| Custom (models.json) | pi provider name | concrete type from provider name or normalized `baseUrl` | from `baseUrl` | the `api` field selects a protocol reader but never becomes provider identity |

`type: openai-compat` is not valid provider identity. For a custom source, the
importer must resolve a concrete Fizeau type from explicit source identity or
config-normalization rules. If it cannot, the importer skips the source with a
warning that asks the operator to choose a concrete type; it does not emit a
protocol name into routing, billing, telemetry, or cost evidence.

**Output uses SD-005 field names exactly:**

```yaml
providers:
  anthropic:
    type: anthropic          # SD-005: type field
    api_key: sk-ant-oat01-...  # SD-005: api_key field
    model: claude-sonnet-4-20250514

  vidar:
    type: lmstudio          # concrete SD-005 provider type
    base_url: http://vidar:1234/v1  # SD-005: base_url field
    api_key: lmstudio         # SD-005: api_key field (placeholder for local)
    model: qwen/qwen3-coder-next

default: anthropic
```

**What gets skipped with warnings:**
- `!command` API key values → warning: "provider X uses shell-resolved key,
  set FIZEAU_API_KEY or add api_key manually"
- Providers whose source identity and `baseUrl` cannot resolve a concrete
  Fizeau provider type
- `headers` values that use `!command` resolution

**Empty model lists:** When models.json has `models: []`, the explicit import
action may make a bounded query to the concrete provider's model-listing
endpoint to discover model identities. If unreachable, it omits the `model`
field (Fizeau will use whatever the provider defaults to). This import-time
listing is not context-window or output-limit evidence. Explicit config wins;
otherwise type-gated limit discovery belongs to explicit refresh or background
snapshot maintenance, and route resolution and execution consume the cached
snapshot without a synchronous provider probe.

#### OpenCode (`fiz import opencode`)

**Reads:**
- `~/.local/share/opencode/auth.json` — `{type: "api", key: "..."}`
- `opencode.json` (project) or `~/.config/opencode/opencode.json` (global)

**Translation uses SD-005 field names:**
- `options.baseURL` → `base_url`
- `options.apiKey` or auth.json key → `api_key`
- `npm: "@ai-sdk/openai-compatible"` selects the protocol reader; provider key
  and normalized `base_url` must still resolve a concrete Fizeau `type`
- `options.headers` → `headers`

Unknown OpenCode sources follow the same rule as custom Pi sources: warn and
skip rather than persisting `openai-compat` as provider identity.

### Secret Handling

**Secrets go to user config, not project config.**

The import writes to `~/.config/fizeau/config.yaml` (user-global) by default,
NOT `.fizeau/config.yaml` (project-level). This prevents accidental commits of
API keys. The `--project` flag writes to `.fizeau/config.yaml` but requires
explicit confirmation and warns:

```
fiz: warning: writing API keys to project config (.fizeau/config.yaml)
fiz: ensure .fizeau/config.yaml is in .gitignore before committing
Proceed? [y/N]
```

**OAuth tokens are never stored alongside refresh tokens.** The import only
persists the `access` token. Refresh tokens in auth.json are ignored.

**File permissions:** Config files with API keys are written with `0600`
(owner read/write only).

**Diff output redacts secrets:** API keys shown as `sk-ant...4f2a` (first 6 +
last 4 chars). Full keys never printed to stdout.

### Drift Detection

The generated config includes a metadata field:

```yaml
imported_from:
  source: pi
  timestamp: "2026-04-07T15:30:00Z"
  source_hash: a1b2c3d4  # SHA-256 of auth.json + models.json concatenated, truncated to 8 hex
```

**Hash covers file content, not individual secrets.** This is acceptable
because the hash is one-way and truncated — it detects "something changed"
without revealing what.

**Check logic:**
- On `fiz providers` or `fiz -p`, if `imported_from` exists and source
  files have a different hash, emit once per day:
  ```
  fiz: pi config changed since import — run 'fiz import pi --diff' to review
  ```
- Debounced by checking mtime of `~/.config/fizeau/.import-check-{source}`
- Per-source, so pi and opencode drift are tracked independently

**Token expiry:** OAuth tokens have an `expires` field in epoch milliseconds.
On import, if a token expires within 24 hours, warn:
```
fiz: warning: anthropic token expires in 3h — use pi to refresh, then re-import
```
If already expired, warn but still import (the token might still work briefly,
or the user may want the endpoint config without the token).

### Merge Mode

`fiz import pi --merge`:
- Adds new providers that don't exist in agent config
- For existing providers: updates `api_key` only (credentials refresh)
- Never overwrites `base_url`, `model`, `headers` (user may have customized)
- Reports what was added, what was updated, what was skipped

`fiz import pi` (no `--merge`):
- Replaces the entire `providers:` section
- Preserves non-provider config (`max_iterations`, `session_log_dir`, `preset`)
- Preserves `imported_from` metadata
- Warns before overwriting if existing config has providers not from the source

### Standard Env Var Fallback

Independent of import, `config.Load()` creates implicit providers from
standard env vars as a last resort (only when NO explicit provider of that
type is configured):

| Env var | Provider name | Type | Default URL |
|---------|--------------|------|-------------|
| `ANTHROPIC_API_KEY` | `anthropic` | `anthropic` | (SDK default) |
| `OPENAI_API_KEY` | `openai` | `openai` | `https://api.openai.com/v1` |
| `OPENROUTER_API_KEY` | `openrouter` | `openrouter` | `https://openrouter.ai/api/v1` |

These implicit providers have lower precedence than any explicit config.
They don't create a `default:` — the user must specify `--provider` or set
`FIZEAU_PROVIDER` to use them.

## Routing and Capacity Boundary

Import produces provider transport, auth, concrete type, and optional model
hints. It does not pre-resolve a route, create candidate order, or establish a
fallback chain. Imported context/output limits are explicit evidence only when
the source format carries authoritative numeric values; otherwise they remain
zero/unknown and explicit or background refresh may populate the type-gated
snapshot later. Ordinary route resolution and execution do not synchronously
probe the imported provider merely to fill those limits.

SD-005 and CONTRACT-003 remain authoritative after import: the service selects
one route, resolves its execution context from candidate/config/cache/catalog/
default evidence, and dispatches that route for the `Execute` call. A capacity
failure does not advance through imported providers. The caller owns any new
request with different pins or power intent.

### Config Schema Additions to SD-005

SD-005's Config struct gains:

```go
type Config struct {
    // ...existing fields from SD-005...

    // ImportedFrom records the last import source for drift detection.
    ImportedFrom *ImportMetadata `yaml:"imported_from,omitempty"`
}

type ImportMetadata struct {
    Source     string `yaml:"source"`      // "pi" or "opencode"
    Timestamp string `yaml:"timestamp"`   // RFC3339
    SourceHash string `yaml:"source_hash"` // truncated SHA-256 of source files
}
```

The config loader ignores `imported_from` — it's metadata, not provider config.

## Implementation Plan

| # | Task | Depends |
|---|------|---------|
| 1 | Pi auth.json reader (picompat/auth.go) | — |
| 2 | Pi models.json reader (picompat/models.go) | — |
| 3 | Pi settings.json reader + translate to Fizeau config | 1, 2 |
| 4 | OpenCode auth + config reader (occompat/) | — |
| 5 | `fiz import` CLI command with diff/merge/redaction | 3, 4 |
| 6 | Zero-config discovery notice in CLI startup | 5 |
| 7 | Drift detection (hash check + daily debounce) | 5 |
| 8 | Standard env var fallback in config.Load() | — |

## Package Structure

```
agent/
  picompat/           # pi config readers
    auth.go           # reads auth.json → map[provider]credential
    models.go         # reads models.json → map[provider]providerDef
    settings.go       # reads settings.json → default provider/model
    translate.go      # merges auth+models+settings → []config.ProviderConfig
    picompat_test.go

  occompat/           # opencode config readers
    auth.go           # reads auth.json
    config.go         # reads opencode.json
    translate.go      # translates to Fizeau config
    occompat_test.go
```

## Risks

| Risk | Prob | Impact | Mitigation |
|------|------|--------|------------|
| OAuth tokens stale after import | H | M | Warn on <24h expiry; drift detection; merge refreshes keys |
| Pi models.json schema changes | M | L | Ignore unknown fields; only extract baseUrl/apiKey/models/api |
| `!command` API keys unsupported | M | L | Skip with warning + guidance to set env var |
| Empty model lists from local providers | M | L | Query model identity only during explicit import; omit if unreachable and leave limits unknown |
| Accidental key commit | M | H | Default to user config; warn on --project; 0600 permissions |
| Large model lists from OpenRouter | L | L | Import all; user can prune |
