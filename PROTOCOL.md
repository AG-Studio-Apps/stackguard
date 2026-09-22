# Relay protocol v1

The wire contract between the meshDeck app, this agent, and the relay that
delivers pushes. The base URL is whatever the app enrolled the host with; the
agent takes it from `MESHDECK_RELAY_HOST`. JSON bodies, `Authorization: Bearer …`.
Bodies are capped at 8 KB. Every write answers 204 on success unless it mints
something. Failed authentication always answers 403 (one answer for an unknown
id and a wrong secret) and is globally rate limited; 429 means back off.

## Kinds
`down` (exited non-zero, OOM-killed, restart loop: the agent's call, detail in
the blob) · `unhealthy` · `disk` · `updates` (daily digest) · `recovered` ·
`silent` (relay-made: the host stopped heartbeating; state `silent` or `back`).
Per-device defaults: all on except `updates`.

## Devices (the app)
- `POST /v1/devices` `{deviceToken, kinds?, deviceID?, deviceSecret?}` →
  `{deviceID, deviceSecret}`. Rate limited per client network (/24 or /64).
  An APNs token is not a secret, so it never proves identity on its own:
  registering a token already on file mints a **fresh, unattached** identity
  and leaves the existing one untouched. An app that still holds its
  credentials sends them as `deviceID` + `deviceSecret`; the relay then keeps
  that identity, its hosts and its preferences, and rotates the secret.
- `PATCH /v1/devices/{id}/prefs` bearer deviceSecret `{kinds:{down?:bool,…}}`
  with PATCH semantics: absent kinds are unchanged. Enforced at the relay, so a
  toggle reaches an agent that is already running.
- `DELETE /v1/devices/{id}` bearer deviceSecret. Hosts left with no device
  are deleted (their agent's token stops working).

## Hosts (the app, bearer deviceSecret)
- `POST /v1/hosts` `{deviceID}` → `{hostID, hostToken}` (creator attached;
  409 over the per-device cap).
- `POST /v1/hosts/{hostID}/attach` `{deviceID, hostToken}` → 204 (idempotent;
  409 over the per-host cap).
- `DELETE /v1/hosts/{hostID}/devices/{deviceID}` → 204 (self only; the last
  device out deletes the host).
- `DELETE /v1/hosts/{hostID}` `{deviceID}` → 204. Only the host's **owner**
  (the device that created it) may delete it; a device that joined by
  presenting the host token can detach itself but not destroy the enrolment.
  403 covers both "not yours" and "no such host", so this is not an
  existence oracle.

## Agent (bearer hostToken)
- `POST /v1/hosts/{hostID}/events` `{kind, collapse, blob}` → 204 when
  accepted: pushed, cooled down (same host + collapse + kind within 10 min),
  or muted by every device; the agent is fire-and-forget and must not retry
  a 204. 400 bad kind, collapse (1–64 of `[A-Za-z0-9_.:-]`) or blob (base64,
  ≤ 2048 chars).

  **`collapse` must be opaque.** It groups repeats of one alert, so it has to
  be stable per container, and it must not be a container name or anything
  else that identifies the host's contents: send a digest, for example the
  first 32 hex of `sha256(containerID)`. The relay hashes whatever arrives
  and keeps only the digest, so a mistake here does not persist; but the
  value still crosses the wire, and the product's promise is that the relay
  never learns what a host runs. 403 unknown host or token: back off to hourly and surface
  "alerts not enrolled" in the agent's own log. 502: no device could be
  reached and at least one push failed on transport: retry with backoff (no
  cooldown was recorded).
  `recovered` clears the cooldown for that collapse key, so
  down → recovered → down all show. Per-host limit 60 events/min.
- `POST /v1/hosts/{hostID}/heartbeat` `{}` → 204. Send every 2 minutes. A host
  is watched only once it has heartbeated at least once, so enrolling one
  never raises a silence alert by itself. After `SILENT_AFTER` (10 min)
  without a heartbeat, every attached device that wants `silent` is pushed;
  the alert is marked delivered only when a push actually lands, so a
  transient APNs failure retries on the next tick. The next heartbeat pushes
  "back".

## The push
```json
{"aps": {"alert": {"title": "meshDeck", "body": "<fixed sentence per kind>"},
         "sound": "default", "thread-id": "<hostID>", "mutable-content": 1},
 "mdHost": "<hostID>", "mdKind": "down", "mdBlob": "<base64>"}
```
Relay-made: `"mdKind": "silent", "mdState": "silent"|"back"` and no blob.
Headers: `apns-push-type alert`; `apns-priority` 10 for down / unhealthy /
disk / silent, 5 for updates / recovered / back; `apns-collapse-id` =
sha256(hostID|collapse)[:32] so a later push for the same container (or
host) replaces the banner; `apns-expiration` now + 1 h (24 h for updates).
`sound` is present only on priority-10 pushes.

## The blob (agent and app; the relay never touches it)
v1: ChaCha20-Poly1305. A 32-byte payload key minted by the app when it
creates the host and handed to the agent with the hostToken (stack secrets).
12-byte random nonce. AAD = the hostID as UTF-8.
`blob = base64(nonce ‖ ciphertext ‖ tag)`, ≤ 2048 characters encoded.
Plaintext, JSON:
```json
{"v": 1, "host": "core-02", "engine": "podman", "container": "sonarr", "kind": "down",
 "detail": "restart loop: 6 restarts in 10 min, exit 137", "at": "2026-09-18T18:02:11Z"}
```
`host` is the display name the user gave the host in the app (the agent is
told it at enrolment); `engine` (`docker` / `podman`) is optional and omitted
when unknown; `detail` is one short human sentence the extension shows as the
body. Apple CryptoKit on iOS (`ChaChaPoly`), swift-crypto on
Linux for the agent.
