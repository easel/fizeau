ARG DDX_BASE_IMAGE=ddx-attempt-runner:dev
FROM ${DDX_BASE_IMAGE}

ARG HUGO_VERSION=0.160.0
ARG GOSEC_VERSION=latest
ARG GOVULNCHECK_VERSION=latest
ARG NODE_MAJOR=24

ENV NPM_CONFIG_CACHE=/opt/npm-cache
ENV PIP_CACHE_DIR=/opt/pip-cache
ENV PIP_FIND_LINKS=/opt/pip-wheelhouse
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

WORKDIR /opt/fizeau-project

# Fizeau CI uses these Go quality tools in Makefile targets.
RUN set -eux; \
  GOBIN=/usr/local/bin go install "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}"; \
  GOBIN=/usr/local/bin go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"; \
  gosec -version; \
  govulncheck -version

# Website and benchmark smoke tests need the same Hugo version as CI.
RUN set -eux; \
  arch="$(dpkg --print-architecture)"; \
  case "$arch" in \
    amd64) hugo_arch=amd64 ;; \
    arm64) hugo_arch=arm64 ;; \
    *) echo "unsupported architecture for Hugo: $arch" >&2; exit 1 ;; \
  esac; \
  curl -fsSL "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VERSION}/hugo_extended_${HUGO_VERSION}_linux-${hugo_arch}.deb" -o /tmp/hugo.deb; \
  apt-get update; \
  apt-get install -y --no-install-recommends /tmp/hugo.deb; \
  rm -rf /tmp/hugo.deb /var/lib/apt/lists/*; \
  hugo version

# Fizeau's JS tooling includes packages that require Node 22+.
RUN set -eux; \
  npm install --global n; \
  n "${NODE_MAJOR}"; \
  hash -r; \
  node --version; \
  npm --version

# Cache Go dependencies for the root module plus nested modules.
COPY go.mod go.sum ./
RUN go mod download

COPY cli/go.mod ./cli/go.mod
RUN cd cli && go mod download

COPY website/go.mod website/go.sum ./website/
RUN cd website && go mod download

# Cache JS dependencies without relying on node_modules under /work.
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --cache /opt/npm-cache \
  && rm -rf node_modules \
  && npm cache verify --cache /opt/npm-cache

# Cache Python wheels and install pytest for the adapter-pytest target.
COPY scripts/website/requirements.txt ./scripts/website/requirements.txt
RUN python3 -m pip download --dest /opt/pip-wheelhouse \
    -r scripts/website/requirements.txt \
    pytest \
  && python3 -m pip install --break-system-packages --no-index \
    --find-links=/opt/pip-wheelhouse \
    -r scripts/website/requirements.txt \
    pytest

# Preinstall the browser used by make benchmark-workbench-smoke.
RUN npx --yes playwright install --with-deps chromium
