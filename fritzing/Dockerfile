# syntax=docker/dockerfile:1
# --- web UI: Svelte app -> web/dist ---
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
# shared UI files (web-shared/ at the repo root), passed as the named build context "shared"
COPY --from=shared . /web-shared/
RUN npm run build

# --- server: static Go binary (pure-Go SQLite) with the UI embedded ---
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -o /out/fritzing-viewer .

# --- run ---
# Debian's Fritzing (0.9.6) and parts library; the server is an Intel Mac mini (2011), so plain amd64 images.
# Its /usr/bin/fritzing is a wrapper that drops all arguments, so we do what it does ourselves
# (a private HOME with the parts library and a writable parts.db) and call the real binary,
# which understands `-svg <folder>`. Qt runs with the offscreen platform plugin: no X server.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends fritzing fritzing-parts librsvg2-bin fonts-liberation fonts-dejavu-core wget ca-certificates \
 && rm -rf /var/lib/apt/lists/*
ENV FRITZING_HOME=/opt/fritzing-home
RUN set -eu; L=$FRITZING_HOME/.local/lib/fritzing; S=/usr/share/fritzing; \
    mkdir -p $L/fritzing-parts/svg $L/app; \
    for d in bins core obsolete scripts contrib user; do [ -e $S/parts/$d ] && cp -Rd $S/parts/$d $L/fritzing-parts/ || true; done; \
    for d in $S/parts/svg/*; do cp -Rd $d $L/fritzing-parts/svg/; done; \
    cp $S/parts.db.distrib $L/fritzing-parts/parts.db; \
    cp /usr/bin/fritzing.real $L/app/Fritzing; \
    for d in help sketches translations; do ln -s $S/$d $L/app/$d; done
COPY --from=build /out/fritzing-viewer /usr/local/bin/fritzing-viewer
ENV DATA_DIR=/data PORT=8030 FRITZING_BIN=/opt/fritzing-home/.local/lib/fritzing/app/Fritzing \
    PARTS_DIR=/usr/share/fritzing/parts QT_QPA_PLATFORM=offscreen
VOLUME /data
EXPOSE 8030
HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --start-interval=1s CMD wget -qO- http://127.0.0.1:8030/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/fritzing-viewer"]
