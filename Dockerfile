# stackGuard, the meshDeck alert agent. A tiny static binary on scratch: no
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
# Static, stripped, reproducible. No cgo: nothing here needs libc.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /stackguard .

FROM scratch
# CA roots so the agent can verify the relay's TLS certificate.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /stackguard /stackguard
# A fixed non-root user. The host socket is owned root:docker (root:root for
# rootful Podman) with a group id that differs per host, so the app reads that
# group off the host and adds it to the container at deploy (GroupAdd) rather
# than the image guessing it; a rootless Podman agent runs as the user itself
# (userns keep-id). Where the app cannot learn the group it runs the agent as
# root and says so on the install screen. Either way every capability is
# dropped, the socket is read-only, and socket access is host access whatever
# the uid.
USER 65532:65532
ENTRYPOINT ["/stackguard"]
