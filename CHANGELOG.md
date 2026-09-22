# Changelog

## 1.0.0, first public release

- Watches one or more engine sockets, Docker or Podman, given as
  `MESHDECK_SOCKETS=engine=path,…` (one agent per privilege domain); each alert
  names the engine it came from.
- Alerts: down, unhealthy, recovered, disk. The rules match the meshDeck app's
  own cards; the first sweep adopts silently.
- Alerts are sealed on the host (ChaCha20-Poly1305) under a key only the phone
  holds; the relay forwards opaque blobs and digests.
- The relay is named by the app in `MESHDECK_RELAY_HOST`; nothing is built in.
- A socket that does not answer at start is kept and retried; each list and
  inspect runs under a 30 s deadline; none answering exits 69.
- Images for linux/amd64 and linux/arm64 on `scratch`, with provenance, an
  SBOM and a cosign signature.
