# ssh-chat container image.
#
# Build:  podman build -t ssh-chat .
# Run:    see deploy/compose.yaml

# ── build ──────────────────────────────────────────────────────────────────
FROM docker.io/library/golang:1.24-alpine AS build

WORKDIR /src

# Dependencies first, as their own layer: go.mod and go.sum change far less
# often than source, so a code-only edit reuses the cached module download.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
# CGO_ENABLED=0 is not an optimisation here, it is the design: the SQLite
# driver is pure Go (modernc.org/sqlite), so the result is a fully static
# binary with no libc dependency, no gcc in this image, and no possibility of
# a glibc/musl mismatch against the runtime image.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/ssh-chat ./cmd/server

# ── runtime ────────────────────────────────────────────────────────────────
FROM docker.io/library/alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 chat \
 && adduser  -S -u 10001 -G chat -h /data chat

COPY --from=build --chown=root:root --chmod=0755 /out/ssh-chat /usr/local/bin/ssh-chat

# The data directory holds the SQLite database (and WAL/SHM files), the SSH
# host key (must persist across restarts or clients get a host-key-changed
# warning every time), and the admins.txt fingerprint list.
RUN mkdir -p /data && chown -R chat:chat /data
VOLUME ["/data"]

USER chat
WORKDIR /data

EXPOSE 2222

ENTRYPOINT ["/usr/local/bin/ssh-chat"]
# Settings are environment variables (SSHCHAT_*), so a .env / env_file / quadlet
# EnvironmentFile can change them without touching the launch command. These
# are only the image's defaults; see .env.example for every option. (They are
# deliberately not command-line flags here: flags would override the
# environment.)
ENV SSHCHAT_ADDR=:2222 \
    SSHCHAT_DB=/data/chat.db \
    SSHCHAT_HOSTKEY=/data/hostkey \
    SSHCHAT_ADMINS=/data/admins.txt
