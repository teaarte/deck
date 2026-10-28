FROM golang:1.27-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends bubblewrap python3 nodejs npm && rm -rf /var/lib/apt/lists/* \
  && npm install -g @openai/codex@0.165.0 && npm cache clean --force \
  && useradd -m -s /bin/bash -u 1000 deck
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
