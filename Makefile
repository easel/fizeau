.PHONY: build build-ci install-quality-tools test test-no-race test-race test-install-sh lint lint-go contract004-import-lint vet fmt fmt-check gosec govulncheck ci-checks ci adapter-pytest check clean coverage coverage-ratchet coverage-bump coverage-history catalog-dist rename-noise-check demos-capture demos-capture-docker demos-capture-subcommands demos-docker-build demos-regen docs-cli docs-embedding docs-tools docs-adrs benchmark-data benchmark-workbench-smoke website-serve capture-machine-info probe-reasoning

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BINARY_NAME := fiz
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"
PORT ?= 1314

build:
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/fiz

catalog-dist:
	go run ./cmd/catalogdist \
		--manifest internal/modelcatalog/catalog/models.yaml \
		--out website/static/catalog \
		--channel stable \
		--min-agent-version "$${MIN_AGENT_VERSION:-$$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || echo dev)}"

rename-noise-check:
	go run ./cmd/renamecheck --repo . --fail

# probe-reasoning sends a 6-row reasoning matrix to a live endpoint and prints
# a verdict table showing which reasoning wire knob the upstream actually honors.
#
# Usage:
#   make probe-reasoning PROFILE=scripts/benchmark/profiles/fiz-openrouter-qwen3-6-27b.yaml
#   make probe-reasoning PROFILE=<path> PROBE_ARGS="--json --artifact-dir /tmp/my-probe"
#
# Requires: OPENROUTER_API_KEY (or the api_key_env named in the profile) to be set.
PROBE_ARGS ?=
probe-reasoning:
	go run ./cmd/fizeau-probe-reasoning $(if $(PROFILE),--profile $(PROFILE),) $(PROBE_ARGS)

# docs-cli regenerates the Hugo CLI reference at website/content/docs/cli/
# from the live Cobra command tree. Run after changing fiz commands or flags.
docs-cli:
	go run ./cmd/docgen-cli --out website/content/docs/cli

# docs-embedding regenerates the embedding (Go library) reference at
# website/content/docs/embedding/ from the public fizeau API source via
# go/doc. Run after changing the public surface in service.go,
# public_api.go, or public_cli_api.go.
docs-embedding:
	go run ./cmd/docgen-embedding --pkg . --out website/content/docs/embedding/_index.md

# docs-tools regenerates the tool-calling reference at
# website/content/docs/tools/_index.md by enumerating
# fizeau.BuiltinToolsForPreset for every prompt preset and emitting each
# tool's name, description, JSON Schema, and parallel-safety. Run after
# changing the registry in internal/tool/builtin.go or any tool's schema.
docs-tools:
	go run ./cmd/docgen-tools --out website/content/docs/tools/_index.md

# docs-adrs republishes Architecture Decision Records from
# docs/helix/02-design/adr/ into website/content/docs/architecture/adr/ with
# Hextra-compatible front matter. Generates a per-section index and per-ADR
# pages. Idempotent: running it twice with no source changes produces no diff.
# Run after adding, editing, or superseding an ADR.
docs-adrs:
	go run ./cmd/docgen-adrs --src docs/helix/02-design/adr --out website/content/docs/architecture/adr

# benchmark-data regenerates the microsite's normalized benchmark analytics
# feeds from per-trial report.json files plus profile and machine metadata.
# Parquet output requires scripts/website/requirements.txt.
BENCHMARK_DATA_PYTHON ?= $(if $(wildcard .venv-report/bin/python),.venv-report/bin/python,python3)
benchmark-data:
	$(BENCHMARK_DATA_PYTHON) scripts/website/build-benchmark-data.py

# website-serve starts the Hugo dev server with the same /fizeau/ base path
# used by GitHub Pages. Override the port with `make website-serve PORT=1315`.
website-serve:
	cd website && hugo server --bind 0.0.0.0 --port $(PORT) --baseURL http://0.0.0.0:$(PORT)/fizeau/ --appendPort=false

# benchmark-workbench-smoke builds the static Hugo site and verifies that the
# browser-side benchmark explorer loads DuckDB/Perspective data and core UI
# slices correctly. Requires `hugo`; installs Playwright Chromium on demand.
benchmark-workbench-smoke:
	cd website && go test ./... -run TestBenchmarkWorkbenchSmoke -count=1

build-ci:
	go build ./...

install-quality-tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

test:
	CGO_ENABLED=1 go test -race ./...

test-no-race:
	go test -count=1 ./...

test-race:
	CGO_ENABLED=1 go test -race -count=1 ./...

test-integration:
	CGO_ENABLED=1 go test -race -tags=integration ./...

test-e2e:
	CGO_ENABLED=1 go test -race -tags=e2e ./...

test-fuzz:
	go test -fuzz=. -fuzztime=30s ./...

test-install-sh:
	bash tests/install_sh_acceptance.sh

contract004-import-lint:
	go test ./internal/lint/...

lint-go:
	golangci-lint run

lint: contract004-import-lint lint-go

vet:
	go vet ./...

fmt:
	gofmt -l . | grep -v '^\.claude/' | grep -v '^\.ddx/' | grep . && exit 1 || true

fmt-check:
	@unformatted="$$(gofmt -l . | grep -v '^\.claude/' | grep -v '^\.ddx/')"; \
	if [ -n "$$unformatted" ]; then \
		echo "Files not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

gosec:
	gosec -exclude-dir=.claude -exclude-dir=.ddx ./...

govulncheck:
	govulncheck ./...

ci-checks: build-ci vet lint gosec govulncheck fmt-check rename-noise-check test-no-race test-race
	@echo "All CI checks passed."

# adapter-pytest mirrors the .github/workflows/ci.yml adapter-pytest job.
adapter-pytest:
	python -m pytest scripts/benchmark/harness_adapters

# ci runs every gate that .github/workflows/ci.yml runs (both jobs).
ci: ci-checks adapter-pytest
	@echo "All CI jobs (test + adapter-pytest) passed."

check: fmt vet lint test coverage-ratchet
	@echo "All checks passed."

# Coverage targets
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	@echo ""
	@echo "Full coverage report: coverage.html"
	go tool cover -html=coverage.out -o coverage.html

coverage-ratchet:
	@echo "Running coverage ratchet check..."
	@go run scripts/coverage-ratchet.go

coverage-bump: coverage-ratchet
	@echo "Auto-bumping coverage floors where coverage exceeds floor by >10%..."
	@go run scripts/coverage-ratchet.go --bump

coverage-history:
	@echo "Coverage history (from .helix-ratchets/coverage-floor.json):"
	@cat .helix-ratchets/coverage-floor.json | jq '.history'

coverage-trend: coverage-ratchet
	@echo "Coverage trend from history:"
	@go run scripts/coverage-ratchet.go --trend

# Capture fresh fiz session JSONLs against a real OpenRouter model and write
# them into demos/sessions/. Requires $OPENROUTER_API_KEY. Live LLM calls.
demos-capture:
	./demos/capture.sh

# Capture demo session JSONLs inside the CPU-only Docker image (bundled
# llama-server + Qwen2.5-Coder-0.5B). No GPU, no API key, no internet.
# Builds the image on first run (~5 min, mostly model download).
demos-capture-docker:
	./demos/capture-docker.sh

# Build the demos Docker image without capturing (handy for CI cache).
demos-docker-build:
	docker build -f demos/docker/Dockerfile.cpu -t fiz-demos-cpu:local .

# Regenerate homepage demo asciicasts from canonical session JSONLs in
# demos/sessions/. Deterministic — no live LLM calls, no `asciinema rec`.
demos-regen:
	./demos/regen.sh

# Capture asciicasts for non-LLM-loop fiz subcommands (usage, update,
# JSONL inspection). Runs each command for real, captures stdout
# verbatim, and emits asciicast v2 directly (bypasses regen.py).
demos-capture-subcommands:
	./demos/capture-subcommands.sh

# Capture this host's hardware + serving-engine inventory and emit a YAML
# block keyed by hostname. Run ON the inference machine you want to record;
# pipe to a file and paste under the matching key in
# scripts/benchmark/machines.yaml. See the script header for prerequisites.
capture-machine-info:
	./scripts/benchmark/capture-machine-info.sh

clean:
	rm -f $(BINARY_NAME)
	go clean ./...
