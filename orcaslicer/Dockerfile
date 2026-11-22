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

# --- server: static Go binary with the UI embedded ---
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY . .
COPY --from=web /web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/orcaslicer .

# --- OrcaSlicer: the release AppImage, extracted (--appimage-extract needs no FUSE) ---
# The Ubuntu 24.04 build needs glibc >= 2.39, so this runs on trixie (glibc 2.41), not bookworm.
FROM debian:trixie-slim AS orca
ARG ORCA_VERSION=2.4.2
ARG ORCA_SHA256=d12fb8c8eac1aecd2dfb6377acd48f994f8fa439ed5292fa532dd82880f029fd
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates wget \
 && rm -rf /var/lib/apt/lists/*
RUN mkdir /tmp/orca && cd /tmp/orca \
 && wget -q "https://github.com/OrcaSlicer/OrcaSlicer/releases/download/v${ORCA_VERSION}/OrcaSlicer_Linux_AppImage_Ubuntu2404_V${ORCA_VERSION}.AppImage" -O orca.AppImage \
 && echo "${ORCA_SHA256}  orca.AppImage" | sha256sum -c - \
 && chmod +x orca.AppImage \
 && ./orca.AppImage --appimage-extract >/dev/null \
 && mv squashfs-root /opt/orcaslicer \
 && cd / && rm -rf /tmp/orca

# --- run ---
# The CLI links GTK 3 and WebKitGTK 4.1 (the AppImage bundles only a few libraries), so those
# come from Debian. Slicing needs no display: the CLI runs with DISPLAY unset (checked with 2.4.2).
FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates wget tzdata fonts-dejavu-core \
      libgtk-3-0t64 libwebkit2gtk-4.1-0 libsecret-1-0 libmspack0t64 \
      libgl1 libglu1-mesa libegl1 libgbm1 libxkbcommon0 libsm6 libice6 libfontconfig1 libdbus-1-3 \
      libgstreamer1.0-0 libgstreamer-plugins-base1.0-0 libgstreamer-gl1.0-0 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=orca /opt/orcaslicer /opt/orcaslicer
COPY --from=build /out/orcaslicer /usr/local/bin/orcaslicer
ENV DATA_DIR=/data PORT=8050 \
    ORCA=/opt/orcaslicer/AppRun \
    ORCA_PROFILES=/opt/orcaslicer/resources/profiles \
    ORCA_VERSION=2.4.2
VOLUME /data
EXPOSE 8050
# Traefik only routes to a healthy container, so check every second while starting.
HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --start-interval=1s CMD wget -qO- http://127.0.0.1:8050/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/orcaslicer"]
