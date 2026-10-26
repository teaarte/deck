FROM golang:1.27-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends bubblewrap python3 && rm -rf /var/lib/apt/lists/* \
  && useradd -m -u 1000 deck
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
