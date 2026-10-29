FROM node:24-bookworm-slim AS node

FROM golang:1.27-bookworm
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
  && apt-get update && apt-get install -y --no-install-recommends bubblewrap python3 && rm -rf /var/lib/apt/lists/* \
  && npm install -g @openai/codex@0.165.0 && npm cache clean --force \
  && useradd -m -s /bin/bash -u 1000 deck && useradd -m -s /bin/bash -u 1001 agent
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
