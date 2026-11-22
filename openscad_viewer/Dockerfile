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
    CGO_ENABLED=0 go build -o /out/openscad-viewer .

# --- OpenSCAD libraries ---
FROM alpine:3.20 AS libs
ARG BOSL2_REF=master
RUN apk add --no-cache git \
 && git clone --depth 1 --branch "$BOSL2_REF" https://github.com/BelfrySCAD/BOSL2.git /libs/BOSL2 \
 && rm -rf /libs/BOSL2/.git

# --- run ---
# An OpenSCAD development snapshot (AppImage, extracted: no FUSE in a container) with the
# Manifold backend instead of Debian's 2021.01 with CGAL: on the Mac mini (i5-2415M, no AVX2,
# runs fine) laser_tag_puck went from 130 s to 3.6 s, small models from ~1-3 s to <0.1 s.
# Pinned: snapshots are daily, bump OPENSCAD_SNAPSHOT deliberately. The apt list is what the
# AppImage expects from the host (checked with ldd). xvfb gives the PNG export (snapshots)
# an X display; STL export doesn't need it.
FROM debian:trixie-slim
ARG OPENSCAD_SNAPSHOT=2026.11.18
RUN apt-get update \
 && apt-get install -y --no-install-recommends xvfb xauth fonts-liberation wget ca-certificates \
      libgpg-error0 libgl1 libegl1 libglib2.0-0t64 libfontconfig1 libx11-6 libxkbcommon0 \
      libdbus-1-3 libharfbuzz0b libopengl0 libglx0 libfreetype6 libcom-err2 \
 && rm -rf /var/lib/apt/lists/* \
 && cd /opt \
 && wget -q "https://files.openscad.org/snapshots/OpenSCAD-${OPENSCAD_SNAPSHOT}-x86_64.AppImage" -O openscad.AppImage \
 && chmod +x openscad.AppImage \
 && ./openscad.AppImage --appimage-extract >/dev/null \
 && rm openscad.AppImage \
 && mv squashfs-root openscad \
 && if find /opt/openscad -name 'openscad*' -type f -perm -u+x -exec ldd {} + | grep 'not found'; then exit 1; fi \
 && /opt/openscad/AppRun --version
COPY --from=libs /libs /usr/local/share/openscad/libraries
COPY --from=build /out/openscad-viewer /usr/local/bin/openscad-viewer
# AppRun resolves its own directory, so call it directly rather than through a symlink.
ENV DATA_DIR=/data PORT=8020 OPENSCADPATH=/usr/local/share/openscad/libraries \
    OPENSCAD=/opt/openscad/AppRun OPENSCAD_ARGS=--backend=manifold
VOLUME /data
EXPOSE 8020
# Traefik only routes to a healthy container, so check every second while starting.
HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --start-interval=1s CMD wget -qO- http://127.0.0.1:8020/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/openscad-viewer"]
