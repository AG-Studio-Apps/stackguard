# stackGuard — the meshDeck alert agent. A tiny static binary on scratch: no
# shell, no package manager, nothing to exploit if a watched container is not.
# Multi-arch (amd64 + arm64) so it runs on a Pi as happily as a NUC.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
# Modules first, for a cached layer that survives source edits.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# Static, stripped, reproducible. No cgo — nothing here needs libc.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /stackguard .

FROM scratch
# CA roots so the agent can verify the relay's TLS certificate.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /stackguard /stackguard
# No USER: the container runs as root so it can read the host's Docker socket,
# which is owned root:docker with mode 0660 and whose group id differs per host
# — the deployment cannot know it to add it, and a fixed non-root uid is denied
# the socket. This is not the privilege it looks like: the deployment drops
# ALL Linux capabilities and mounts the socket read-only, and socket access is
# host access whatever the uid (the install screen says so). Root here buys
# nothing beyond opening the socket.
ENTRYPOINT ["/stackguard"]
