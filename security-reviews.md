# Security reviews

A running record of the security reviews run against stackGuard, what they
found, and what was done. Newest first.

## 2026-09-26: post-1.0.2 review

Reviewed at revision `da3f5ff` (branch `develop`) with a Claude Security
multi-agent scan of the whole repository at medium effort (inventory, threat
model, per-component research, and the three-lens verification panel).

**Result: 1 finding, verification status `verified`.** Unlike the 2026-09-22
report, this one is committed, under `CLAUDE-SECURITY-20260926-213603/`.

### F1 — release pipeline did not restrict the ref to main's ancestry (CWE-862, MEDIUM)

`publish.yml` triggered on any pushed `v*` tag or `workflow_dispatch` branch and
built, pushed, cosign-signed and released whatever commit that ref pointed at,
with no check that it was on the protected `main` branch. Because branch
protection does not extend to tags, an off-main tag (or a dispatch on an off-main
branch) could ship a never-reviewed commit as the repository-signed `:1` image,
passing the exact `cosign verify` the README documents. The sibling
`publish-ios-channel.yml` already gated on `git merge-base --is-ancestor …
origin/main`; `publish.yml` had no equivalent. The panel voted 2/3: the
reachability lens noted the trigger needs an actor who already holds write
access, while the impact and defenses lenses held that a repo-signed image cut
from unreviewed off-main code is a real bypass of the documented release control.

### Fix applied from the scan

`publish.yml` now runs a fail-closed `git merge-base --is-ancestor
"$GITHUB_SHA" origin/main` gate immediately after checkout and before every
build/push/sign/release step, refusing any ref not on main's ancestry — the same
gate `publish-ios-channel.yml` uses. `$GITHUB_SHA` is the built commit for both
triggers, so the off-main tag and off-main dispatch routes are both closed. The
patch was verified by the scan's patch panel (an independent reviewer plus a
fresh researcher who found no bypass and no new attack path) and merged via
PR #13 (commit `821cf4e`).

Recommended as defense in depth, outside the workflow file: a GitHub `v*`
tag-protection ruleset and/or a protected `release` deployment environment, so
the guarantee does not rest on the workflow alone.

## 2026-09-22: pre-1.0.2 review

Reviewed at revision `b8cd01f` (branch `develop`), ahead of cutting `v1.0.2`.
Three layers: a manual focused pass, a multi-agent scan, and dependency/binary
scanning. The agent's threat model throughout: it runs as a fixed non-root
container user with every Linux capability dropped, no inbound port, engine
sockets mounted read-only; that socket access is host access is documented and
accepted, so a finding has to let the agent do more than its GETs, leak its
secrets, or let someone other than the maintainer influence a release.

### Focused pass (manual)

Killed the obvious hardening items so the scan could spend its effort on subtler
things. Fixed:

- **Bounded Engine API reads.** `version`, `list` and `inspect` now decode
  through a 32 MiB `io.LimitReader` (`decodeBounded`), so a buggy or hostile
  daemon response cannot grow the agent's memory without bound.
- **Container id guard.** `inspect` refuses an id that is not plain 12-64 char
  hex, so a daemon returning an `Actor.ID` carrying `/` or `?` cannot retarget
  the API path.
- **Pinned builder base.** The Dockerfile's `golang:1.26-alpine` is pinned by
  digest, so a retagged upstream cannot enter a signed release build.
- **SHA-pinned actions.** Every action in both workflows is pinned to the
  commit its major tag resolved to, and `dependabot.yml` keeps the pins
  current, so a retagged or compromised action cannot enter a release or the
  channel publish.

Noted, not fixed here (app side, batched with the next meshDeck build): the
agent container has no memory/pids limit in `AgentDeployment.spec`; adding one
would bound the host-side blast radius of a runaway read.

### Multi-agent scan (Claude Security, max effort)

Whole repository, max effort (full pipeline plus an adversarial repanel).
**Result: 0 findings, verification status `verified`.** The report is not
committed (it is a local artifact under `CLAUDE-SECURITY-<timestamp>/`).

The researchers raised three candidates; the verification panel eliminated all
three:

- **Sweep/heartbeat integer overflow** (`config.go`), rejected unanimously:
  operator-authored deployment config, self-inflicted busy loop at worst.
- **Unbounded event-stream decode** (`docker.go`), rejected unanimously: the
  event source is the host-local daemon; the only influenceable field is a
  container label, settable only from an already-game-over position against a
  capability-dropped read-only monitor. (The `version`/`list`/`inspect` reads
  are bounded; the long-lived event stream deliberately is not.)
- **Channel digest vs. image signature** (`publish-ios-channel.yml`), raised
  as a real CWE-345 gap, split 2/3 then dropped 1/3 on the adversarial repanel:
  reachable only behind a prior `packages: write` registry compromise plus a
  maintainer manually dispatching the publish. **Closed anyway** (see below):
  it was cheap and a genuine supply-chain tightening.

### Fix applied from the scan

`publish-ios-channel.yml` now runs `cosign verify` on the resolved digest
before it signs the channel manifest, requiring the digest to carry the keyless
signature whose certificate identity is a workflow in this repository (the same
signature the README documents). The channel's trust chain now links to the
image's signature rather than to a mutable tag; a poisoned tag has no such
signature and the publish stops.

### Dependency and binary scanning

- **govulncheck**, run inside the pinned builder image (go1.26.8): **0
  vulnerabilities in the call path.** Run against the local dev toolchain
  (go1.26.4) it reports 5 reachable Go standard-library issues (crypto/tls,
  encoding/asn1, net/http), all fixed in go1.26.5-1.26.6; the shipped image is
  built on go1.26.8 via the pinned base, so the released binary is not affected.
  The dev box's own Go toolchain should be bumped to match, but that does not
  affect the artifact.
- **Dependabot.** 15 open alerts, all `golang.org/x/crypto` for ranges
  `< 0.45.0` / `< 0.52.0`. go.mod is on **v0.57.0**, above every cited range, so
  they were stale/already-patched, and govulncheck confirms the vulnerable
  packages are not in the call path. All 15 dismissed as "not used", with that
  reason recorded on each.
