# deck on a server: deck with the tailnet, Claude Code, Codex, uv and a browser; agents and scripts run as the
# user agent, deck as deck, so deck's state stays closed to them. compose.yaml runs it; see README, "Server".

# The versions of the CLIs, which tests/linux builds its image with too.
ARG CLAUDE_VERSION=2.1.281
ARG CODEX_VERSION=0.165.0

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS build
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags tailnet -trimpath \
      -ldflags "-s -w -X deck2/internal/release.Version=$VERSION -X deck2/internal/release.Commit=$COMMIT" \
      -o /out/deck ./cmd/deck

FROM ghcr.io/astral-sh/uv:0.13.6@sha256:af22a52f5eafef3d78e551599d405c02df61919d18028f0cea8eda93bc9986d0 AS uv

FROM node:24-bookworm-slim@sha256:c86ecead9e55a5210ac1727de38a4ca840bf03dcaf34dc9749addbf383b1920c
ARG CLAUDE_VERSION CODEX_VERSION
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git curl openssh-client python3 bubblewrap socat \
       libcap2-bin chromium chromium-sandbox fonts-liberation fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/google/chrome && ln -s /usr/bin/chromium /opt/google/chrome/chrome \
    && npm install -g @anthropic-ai/claude-code@$CLAUDE_VERSION @openai/codex@$CODEX_VERSION && npm cache clean --force \
    && useradd --create-home --home-dir /home/agent --shell /bin/bash agent \
    && useradd --no-create-home --home-dir /home/agent --shell /bin/bash deck \
    && mkdir -p /var/lib/deck && chown deck:deck /var/lib/deck && chmod 700 /var/lib/deck /home/agent
COPY --from=uv /uv /uvx /usr/local/bin/
COPY --from=build /out/deck /usr/local/bin/deck
COPY --from=build /out/deck /usr/local/lib/deck/bridge
# deck runs the programs of agents as agent, hands them their folders and stops them; only deck may run it.
RUN chown root:deck /usr/local/bin/deck && chmod 0750 /usr/local/bin/deck \
    && setcap cap_setuid,cap_setgid,cap_chown,cap_fowner,cap_dac_override,cap_kill+ep /usr/local/bin/deck
ENV HOME=/home/agent SHELL=/bin/bash DECK_HOME=/var/lib/deck DECK_ADDR=0.0.0.0:7700 DECK_AGENT_USER=agent \
    DECK_IMAGE=ghcr.io/teaarte/deck DECK_BRIDGE=/usr/local/lib/deck/bridge
USER deck
WORKDIR /
EXPOSE 7700 7701
VOLUME ["/var/lib/deck", "/home/agent"]
ENTRYPOINT ["/usr/local/bin/deck"]
