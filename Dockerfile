FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/downly ./cmd/downly

FROM debian:bookworm-slim
ARG TARGETARCH=amd64
# yt-dlp needs a JavaScript runtime for YouTube; Deno is its default.
# https://github.com/yt-dlp/yt-dlp/wiki/EJS
ARG DENO_VERSION=v2.9.7

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl python3 ffmpeg tini unzip \
    && case "$TARGETARCH" in \
         amd64) deno_arch=x86_64 ;; \
         arm64) deno_arch=aarch64 ;; \
         *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
       esac \
    && curl -fsSL "https://github.com/denoland/deno/releases/download/${DENO_VERSION}/deno-${deno_arch}-unknown-linux-gnu.zip" -o /tmp/deno.zip \
    && unzip -q /tmp/deno.zip -d /usr/local/bin \
    && rm /tmp/deno.zip \
    && deno --version \
    && apt-get purge -y unzip && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

# Run as an unprivileged user. yt-dlp lives in a directory that user owns so
# the auto-updater (yt-dlp -U) can replace it in place.
RUN useradd --system --uid 10001 --home-dir /app --shell /usr/sbin/nologin downly \
    && mkdir -p /opt/yt-dlp /app/tmp /app/cookies \
    && curl -fsSL https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /opt/yt-dlp/yt-dlp \
    && chmod 0755 /opt/yt-dlp/yt-dlp \
    && chown -R downly:downly /opt/yt-dlp /app
ENV PATH="/opt/yt-dlp:${PATH}"

WORKDIR /app
COPY --from=builder /out/downly /app/downly

USER downly
VOLUME ["/app/tmp"]
EXPOSE 8080

# tini reaps orphaned yt-dlp/ffmpeg children and forwards SIGTERM.
ENTRYPOINT ["/usr/bin/tini", "--", "/app/downly"]
CMD ["-config", "/app/config.yaml"]
