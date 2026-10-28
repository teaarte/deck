# deck on a server: deck with the tailnet, Claude Code, Codex, uv and a browser; agents and scripts run as the
# user agent, deck as deck, so deck's state stays closed to them. compose.yaml runs it; see README, "Server".
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags tailnet -trimpath \
      -ldflags "-s -w -X deck2/internal/release.Version=$VERSION -X deck2/internal/release.Commit=$COMMIT" \
      -o /out/deck ./cmd/deck

FROM ghcr.io/astral-sh/uv:0.13.6 AS uv

FROM node:24-bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git curl openssh-client python3 bubblewrap socat \
       libcap2-bin chromium chromium-sandbox fonts-liberation fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/google/chrome && ln -s /usr/bin/chromium /opt/google/chrome/chrome \
    && npm install -g @anthropic-ai/claude-code @openai/codex && npm cache clean --force \
    && useradd --create-home --home-dir /home/agent --shell /bin/bash agent \
    && useradd --no-create-home --home-dir /home/agent --shell /bin/bash deck \
    && mkdir -p /var/lib/deck && chown deck:deck /var/lib/deck && chmod 700 /var/lib/deck /home/agent
COPY --from=uv /uv /uvx /usr/local/bin/
COPY --from=build /out/deck /usr/local/bin/deck
# deck runs the programs of agents as agent, hands them their folders and stops them.
RUN setcap cap_setuid,cap_setgid,cap_chown,cap_fowner,cap_dac_override,cap_kill+ep /usr/local/bin/deck
ENV HOME=/home/agent SHELL=/bin/bash DECK_HOME=/var/lib/deck DECK_ADDR=0.0.0.0:7700 DECK_AGENT_USER=agent \
    DECK_IMAGE=ghcr.io/teaarte/deck
USER deck
WORKDIR /
EXPOSE 7700 7701
VOLUME ["/var/lib/deck", "/home/agent"]
ENTRYPOINT ["/usr/local/bin/deck"]
