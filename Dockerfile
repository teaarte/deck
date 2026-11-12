# deck on a server, and on a Mac in Docker: deck with the tailnet, Claude Code, Codex, Goose, uv and a browser;
# every foreign program runs in a box of its own (bubblewrap). compose.yaml runs it; see README, "Install".

# The versions of the CLIs, which tests/linux builds its image with too.
ARG CLAUDE_VERSION=2.1.281
ARG CODEX_VERSION=0.165.0
# Goose, the ACP agent that runs agent turns on any model of deck's settings.
ARG GOOSE_VERSION=1.53.0

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS build
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# A build without COMMIT takes the commit of the HEAD of its checkout (.dockerignore lets in HEAD and the refs only);
# a worktree's .git is a file naming a folder outside, so install.sh gives COMMIT.
RUN if [ -z "$COMMIT" ] && [ -f .git/HEAD ]; then \
      ref=$(sed -n 's/^ref: //p' .git/HEAD); \
      COMMIT=$(if [ -z "$ref" ]; then cat .git/HEAD; elif [ -f ".git/$ref" ]; then cat ".git/$ref"; \
        else sed -n "s| $ref\$||p" .git/packed-refs 2>/dev/null; fi | cut -c1-12); \
    fi; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags tailnet -trimpath -buildvcs=false \
      -ldflags "-s -w -X deck2/internal/release.Version=$VERSION -X deck2/internal/release.Commit=$COMMIT" \
      -o /out/deck ./cmd/deck

FROM ghcr.io/astral-sh/uv:0.13.6@sha256:af22a52f5eafef3d78e551599d405c02df61919d18028f0cea8eda93bc9986d0 AS uv

FROM node:24-bookworm-slim@sha256:c86ecead9e55a5210ac1727de38a4ca840bf03dcaf34dc9749addbf383b1920c
ARG CLAUDE_VERSION CODEX_VERSION GOOSE_VERSION TARGETARCH
# The programs a module may name are those of skills/module-builder/SKILL.md; the test of the server finds each here.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git curl openssh-client python3 bubblewrap socat \
       ffmpeg unzip chromium chromium-sandbox fonts-liberation fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/google/chrome && ln -s /usr/bin/chromium /opt/google/chrome/chrome \
    && npm install -g @anthropic-ai/claude-code@$CLAUDE_VERSION @openai/codex@$CODEX_VERSION && npm cache clean --force \
    && arch=$(case "$TARGETARCH" in arm64) echo aarch64;; *) echo x86_64;; esac) \
    && curl -fsSL "https://github.com/block/goose/releases/download/v$GOOSE_VERSION/goose-$arch-unknown-linux-gnu.tar.gz" \
       | tar -xz -C /usr/local/bin ./goose && chmod 755 /usr/local/bin/goose && goose --version \
    && useradd --uid 1002 --user-group --create-home --shell /bin/bash deck \
    && mkdir -p /var/lib/deck && chown deck:deck /var/lib/deck && chmod 700 /var/lib/deck
COPY --from=uv /uv /uvx /usr/local/bin/
COPY --from=build /out/deck /usr/local/bin/deck
# The image starts as root only to give deck its volume once, files v0.1's user agent left among them, and to copy
# the logins of v0.1's volume /home/agent into deck's; deck runs as deck.
COPY --chmod=755 <<'EOF' /usr/local/bin/deck-start
#!/bin/sh
set -eu
old=/home/agent mark="$DECK_HOME/state/owned"
if [ "$(id -u)" != 0 ]; then
  [ ! -d "$old" ] || echo "deck-start: not root, so the logins in $old stay where they are" >&2
  exec /usr/local/bin/deck "$@"
fi
mkdir -p "$DECK_HOME"
if [ ! -e "$mark" ]; then
  find "$DECK_HOME" ! -user deck -exec chown -h deck:deck {} +
  setpriv --reuid=deck --regid=deck --init-groups sh -c 'mkdir -p "$(dirname "$1")" && touch "$1"' - "$mark"
fi
if [ -d "$old" ]; then
  find "$old" ! -user deck -exec chown -h deck:deck {} +
  for f in claude/.claude/.credentials.json codex/.codex/auth.json codex/.codex/config.toml; do
    login="$DECK_HOME/agents/${f%%/*}/login"
    if [ -f "$old/${f#*/}" ] && [ ! -e "$login/${f##*/}" ]; then
      setpriv --reuid=deck --regid=deck --init-groups sh -c 'mkdir -p "$1" && cp "$2" "$1/"' - "$login" "$old/${f#*/}"
    fi
  done
  # deck copies the sessions that may go on from the old homes of the CLIs into the homes of their projects.
  for d in .claude .codex; do if [ -d "$old/$d" ]; then ln -sfn "$old/$d" "$HOME/$d"; fi; done
fi
exec setpriv --reuid=deck --regid=deck --init-groups /usr/local/bin/deck "$@"
EOF
ENV HOME=/home/deck SHELL=/bin/bash DECK_HOME=/var/lib/deck DECK_ADDR=0.0.0.0:7700 DECK_IMAGE=ghcr.io/teaarte/deck
WORKDIR /
EXPOSE 7700 7701
# A deck that answers nothing on its port, hung or still starting after ten minutes, is unhealthy in docker ps.
HEALTHCHECK --interval=1m --timeout=10s --start-period=10m --retries=3 \
    CMD curl -fsS -o /dev/null --max-time 10 "http://127.0.0.1:${DECK_ADDR##*:}/" || exit 1
VOLUME ["/var/lib/deck"]
ENTRYPOINT ["/usr/local/bin/deck-start"]
