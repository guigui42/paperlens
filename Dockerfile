FROM golang:1.27.3-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/paperlens ./cmd/paperlens

FROM debian:trixie-slim AS runtime
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Paperlens" \
    org.opencontainers.image.description="Search-first Paperless frontend with an isolated Copilot SDK runtime" \
    org.opencontainers.image.source="https://github.com/guigui42/paperlens" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    io.paperlens.copilot-sdk.version="1.0.31" \
    io.paperlens.copilot-cli.version="1.0.109"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl libstdc++6 \
    && rm -rf /var/lib/apt/lists/*
# Pair verified against the SDK's v1.0.31 copilotCliVersion metadata.
# Checksums pin the exact runtime bytes; runtime homes must stay isolated.
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) arch=x64; hash=45a7f37e822e8c8725eda6b27deaed55dad2688ff4163ed27ec22e23293cb761 ;; \
      arm64) arch=arm64; hash=679db06c3168457d8154cd8ce93b822f7c46d3169e61da4c0b78ac6e110c4af6 ;; \
      *) echo "Unsupported architecture" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/github/copilot-cli/releases/download/v1.0.109/copilot-linux-${arch}.tar.gz" -o /tmp/copilot.tar.gz; \
    echo "$hash  /tmp/copilot.tar.gz" | sha256sum -c -; \
    mkdir /opt/copilot; tar -xzf /tmp/copilot.tar.gz -C /opt/copilot; \
    rm /tmp/copilot.tar.gz; \
    groupadd --gid 10001 paperlens; useradd --uid 10001 --gid 10001 --no-create-home paperlens; \
    mkdir /data /work /runtime; chown 10001:10001 /data /work /runtime
COPY --from=build /out/paperlens /usr/local/bin/paperlens
USER 10001:10001
WORKDIR /work
ENV PAPERLENS_LISTEN=0.0.0.0:8080 \
    HOME=/runtime/home \
    PAPERLENS_STATE_DIR=/data \
    PAPERLENS_RUNTIME_DIR=/runtime/jobs \
    COPILOT_CLI_PATH=/opt/copilot/copilot
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD ["paperlens", "health"]
ENTRYPOINT ["paperlens"]
