# Claude Security results

Scanned the whole `stackguard` repository (`/home/james/appfactory/stackguard`) at revision `da3f5ff`, a full codebase scan with no scope narrowing, at medium effort, on 2026-09-26. The working tree was clean. One finding surfaced and survived verification: a single MEDIUM-severity supply-chain authorization gap in the image release pipeline. No CRITICAL, HIGH, or LOW findings. The agent's Go runtime, its crypto, its socket handling, and its relay protocol handling drew no confirmed findings.

## Coverage

The 32 tracked files were partitioned into two components and both were examined: `agent-core` (the 15 Go source and test files — `main.go`, `watcher.go`, `docker.go`, `runner.go`, `relay.go`, `alert.go`, `payload.go`, `cipher.go`, `config.go`, `disk.go`, `attention.go` and their tests) and `build-supply-chain-docs` (the CI workflows under `.github`, the `Dockerfile`, `go.mod`/`go.sum`, the minisign public keys under `keys`, `scripts/provision-keys.sh`, and the Markdown docs). The completeness check passed: every top-level directory (`.github`, `keys`, `scripts`, and the root files) was accounted for, so a clean area here means covered-and-clean rather than not-examined.

The run sized itself from the 32 tracked files into about 2 components of roughly 25 files each (cap 24), one researcher per component-and-category cell. Nine researchers were dispatched across the two components and the root files, for injection/input, authentication/authorization, memory-and-unsafe, and cryptography/secrets; all nine returned, so no reading is missing. The verification panel ran once. No candidates were lost on the way, none were carried to a further run, and no finding had its severity lowered by the panel.

Two areas were deliberately not examined, both non-source: `.git` (version-control internals) and this scan's own output directory. By the researchers' own account, 31 of the 32 files were read to a conclusion; the one not read is `LICENSE`, left as background (Apache-2.0 text, no code). Nothing runs the repository's code in a scan — every finding is derived from reading, not from executing tests or firing an exploit.

## Findings

### F1 — Release pipeline signs and publishes any tag or branch, not just `main` (MEDIUM, confidence medium)

**Impact.** A commit that was never merged to the protected `main` branch can be published to GHCR as `:1`, `:<version>` and `:sha-<sha>`, cosign-signed with the repository's keyless OIDC identity, and cut as a GitHub Release. That signature satisfies the exact `cosign verify …:1 --certificate-identity-regexp '^https://github.com/AG-Studio-Apps/stackguard/' …` command the README tells users to run, so anyone pulling `:1` directly (as the README instructs) or auditing by cosign identity would accept unreviewed code that gets read access to a Docker or Podman socket — host access — when deployed. It bypasses the documented release-authorization control that "main is protected; releases are tagged from main after merge."

**Where.** `.github/workflows/publish.yml:18` in the publish job's `push` trigger (`tags: ["v*"]`) — CWE-862. The unguarded sinks it reaches are the registry push (line 62), the keyless cosign signing (line 71), and the GitHub Release (line 88).

**What.** The workflow triggers on any pushed `v*` tag or any `workflow_dispatch` branch, checks out and builds whatever commit that ref points at, and then pushes, signs, and releases it — with no check that the built commit is on `main`. The sibling workflow `publish-ios-channel.yml` does gate exactly this, with `git merge-base --is-ancestor "$TAG_SHA" origin/main` at its line 57; `publish.yml` has no equivalent, so nothing between the trigger and the signing step verifies release authorization.

**Exploit scenario.** A collaborator with write-but-not-release-manager access, or a leaked CI credential with contents/id-token/packages scope, pushes a tag `v9.0.0` pointing at a feature branch that holds a backdoored agent (or dispatches the workflow on that branch). The workflow builds that commit, pushes it as `ghcr.io/ag-studio-apps/stackguard:9` and `:9.0.0`, and cosign-signs the digest under the repo's OIDC identity. A user who follows the README pulls the tag, cosign-verifies it, sees a valid repository signature, and runs the backdoored image against their engine socket.

**Preconditions.**
- Repository write (push) access, or control of a CI credential/action with contents + id-token + packages scope on this repo. Fork pull requests cannot reach it: there is no `pull_request` trigger and forks receive no id-token.
- No tag-protection ruleset covers `v*` (GitHub's default), so `main`'s branch protection does not extend to tags and a `v*` tag may point at any commit.
- For the dispatch vector, the actor picks an arbitrary branch; the checkout builds that branch's HEAD while `git describe` supplies the major tag.

**Fix.** Gate the build/push/sign steps on the built commit being on `main`'s ancestry, the way `publish-ios-channel.yml` already does — e.g. fail the job when `git merge-base --is-ancestor "$GITHUB_SHA" origin/main` is false, and reject a manual dispatch whose HEAD is not on `main`. Back it with platform controls that do not depend on the workflow file itself: a GitHub tag-protection ruleset for `v*`, and/or a protected `release` deployment environment so only authorized principals can run the signing job.

**Verification.** 2/3 lens verifiers confirmed. The reachability lens dissented, reading the trigger as available only to someone who already holds repository write access and could therefore edit the workflow anyway; the impact and defenses lenses held that a repository-signed `:1` image and a GitHub Release produced from unreviewed, off-`main` code is a real and unmitigated bypass of the stated release control, which is why it is reported at MEDIUM.

## What was verified

These findings come from a team of researcher agents reading the code, followed by a three-lens adversarial panel (reachability, impact, defenses) that votes each candidate up or down; the workflow computes the tally, and only findings a majority confirmed appear here. Two candidates were raised and one was refuted by its panel; F1 survived on a 2-of-3 vote. The scan verified cleanly — all nine researchers returned and the panel completed — so the "verified" status covers the whole run with no missing readings. Verification is analysis, not demonstration: no test was run and no exploit was fired.
