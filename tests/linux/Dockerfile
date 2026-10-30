# CODEX_VERSION is the one of deck's Dockerfile; linux_test.go passes it.
ARG CODEX_VERSION

FROM node:24-bookworm-slim@sha256:c86ecead9e55a5210ac1727de38a4ca840bf03dcaf34dc9749addbf383b1920c AS node

FROM ghcr.io/astral-sh/uv:0.13.6@sha256:af22a52f5eafef3d78e551599d405c02df61919d18028f0cea8eda93bc9986d0 AS uv

FROM golang:1.27-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9
ARG CODEX_VERSION
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
  && apt-get update && apt-get install -y --no-install-recommends bubblewrap python3 && rm -rf /var/lib/apt/lists/* \
  && npm install -g @openai/codex@$CODEX_VERSION && npm cache clean --force \
  && useradd -m -s /bin/bash -u 1000 deck && useradd -m -s /bin/bash -u 1001 agent
COPY --from=uv /uv /usr/local/bin/uv
