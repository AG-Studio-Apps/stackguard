# stackGuard

The alert agent for [meshDeck](https://meshdeck.ag-applications.com): one
small Go binary you run as a container on a Docker or Podman host. It watches
the engine and tells your phone, through a relay the app names, when a
container goes down, turns unhealthy, recovers, or the host is running out of
disk. meshDeck deploys and configures it for you; this repository exists so
that you can read what you are running before you let it near a socket.

It is deliberately small, and this README is written for the person who
wants to check that claim rather than take it.

## What it reads, and what it sends

**On the host** it opens the engine sockets it is given, read-only, and makes
exactly these requests over the Docker Engine API (Podman answers the same API):

| Request | Why |
|---|---|
| `GET /version` | once, at start, to log what answered |
| `GET /containers/json?all=1` | the sweep, once a minute |
| `GET /containers/{id}/json` | only for a container that needs a closer look |
| `GET /events?filters={"type":["container"]}` | the event stream, held open |

It also reads free space on its own root filesystem (`statfs("/")`), which is
the filesystem the engine's storage sits on.

**Off the host** it makes exactly two kinds of request, both HTTPS `POST`s to
the relay named in `MESHDECK_RELAY_HOST`, verified against the system trust
store:

| Request | Body |
|---|---|
| `/v1/hosts/{id}/events` | `{kind, collapse, blob}`: the kind of alert, an opaque digest to group repeats, and an encrypted blob |
| `/v1/hosts/{id}/heartbeat` | `{}` every two minutes |

The blob is sealed with ChaCha20-Poly1305 under a 32-byte key that only your
phone and this agent hold; the relay forwards it unread and never learns a host
or container name. `collapse` is `sha256("container:" + id)` truncated, never a
name. The wire contract is in [PROTOCOL.md](PROTOCOL.md).

**What it never does:** no shell, no `exec`, no writes to the engine, no
inbound port, no other network destination, no telemetry. Every action in
meshDeck goes through the app's own connection to the host, never through
here. `grep -n "http.MethodPost\|Dial\|exec" *.go` is a short read.

## How it is run

Everything is configuration through the environment; nothing is baked into the
image: not a secret, and not the relay's address.

| Variable | | |
|---|---|---|
| `MESHDECK_HOST_NAME` | required | the display name you gave the host in the app |
| `MESHDECK_HOST_ID` | required | relay host id (minted by the app) |
| `MESHDECK_HOST_TOKEN` | required | relay bearer token (minted by the app) |
| `MESHDECK_KEY` | required | base64 of the 32-byte payload key (minted by the app) |
| `MESHDECK_RELAY_HOST` | required | the relay the app enrolled the host with |
| `MESHDECK_RELAY_PORT` | `443` | |
| `MESHDECK_SOCKETS` | | `engine=path` per socket, comma-separated: `docker=/var/run/docker.sock,podman=/run/podman/podman.sock` |
| `MESHDECK_DOCKER_SOCKET` | `/var/run/docker.sock` | the one Docker socket, when `MESHDECK_SOCKETS` is unset |
| `MESHDECK_SWEEP_SECONDS` | `60` | the full list-and-compare backstop |
| `MESHDECK_HEARTBEAT_SECONDS` | `120` | so the relay's "host silent" alert means something |
| `MESHDECK_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

The app creates the container like this, and nothing more:

```
image:          ghcr.io/ag-studio-apps/stackguard:1
user:           65532:65532 (the image's own), plus group_add <the socket's gid>
                or, for a rootless Podman socket, the user itself under userns keep-id
binds:          /var/run/docker.sock:/var/run/docker.sock:ro   (one per socket)
cap_drop:       ALL
privileged:     false
ports:          none
restart:        unless-stopped
```

One agent watches every socket of its privilege domain (root's Docker and
root's Podman together; a user's rootless Podman on its own), and each alert
says which engine it came from.

**It does not run as root.** The image sets a fixed non-root user (65532).
The host socket is owned `root:docker` (or `root:root` for rootful Podman)
with a group id that differs per host, so the app reads that group off the
host and adds it to the container at deploy; a rootless Podman agent runs as
the user who owns the socket, mapped to the same uid inside (`keep-id`). The
one exception is a host the app cannot inspect (a Portainer host): there it
runs the agent as root and says so on the install screen.

Read the Dockerfile before deciding what any of this means, because the
honest framing, which the app repeats on its install screen, is that
**socket access is host access** whatever the uid: anything that can talk to
an engine socket can ask it to start a container with the host's filesystem
in it. Deploy this only on hosts you already trust meshDeck with.

## What it decides

The rules are ported from the app's own cards, so a notification never
contradicts what the app shows:

- **down**: exited non-zero, dead, OOM-killed, or in a restart loop.
- **unhealthy**: running, but the health check is failing.
- **recovered**: a container that had a problem is genuinely running again.
- **disk**: the filesystem is below 10% free; clears above 15%, so it does not flap.

The first sweep *adopts* the fleet silently. An alert is about a change, and
the app already shows what is broken. A container someone stops has not
"recovered". Stopped cleanly, paused, created and removed all clear silently.
`watcher_test.go` is the specification.

## Verifying the image

Images are built by [the workflow in this repository](.github/workflows/publish.yml)
on a version tag, for `linux/amd64` and `linux/arm64`, with a static
`-trimpath` build, no cgo, on `scratch`. Each image carries SLSA provenance
and an SBOM, and is signed with cosign (keyless, GitHub OIDC), so you can check
that what you pull came from this repository's CI at a commit you can read:

```sh
cosign verify ghcr.io/ag-studio-apps/stackguard:1 \
  --certificate-identity-regexp '^https://github.com/AG-Studio-Apps/stackguard/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
docker buildx imagetools inspect ghcr.io/ag-studio-apps/stackguard:1 --format '{{json .Provenance}}'
```

The app pins the major tag (`:1`), never `latest`: a change to the
environment or wire contract gets a new major and arrives only when the app
asks for it.

## Building it yourself

```sh
go vet ./... && go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o stackguard .
```

Or `docker build .`. The only dependency outside the standard library is
`golang.org/x/crypto`, for ChaCha20-Poly1305.

You can point it at a relay of your own: implement [PROTOCOL.md](PROTOCOL.md)
and set `MESHDECK_RELAY_HOST`. The encryption between agent and phone does not
involve the relay at all.

## Licence

Apache License 2.0; see [LICENSE](LICENSE). The names stackGuard and meshDeck
are not licensed (see [NOTICE](NOTICE)). Security reports: [SECURITY.md](SECURITY.md).
