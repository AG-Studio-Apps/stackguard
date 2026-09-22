#!/usr/bin/env bash
# Generates the stackGuard channel signing roster: a primary key (the publish
# workflow signs with it) and an emergency key (kept offline; the rotation
# story, which the app also trusts). Run on a machine you trust, by the
# maintainer.
#
#   scripts/provision-keys.sh [DIR] [--only primary|emergency] [--set-secrets]
#
# DIR (default ~/appfactory/stackguard_keys) is a private directory outside
# any repository. It receives, per role:
#   <role>.key <role>.pub       the minisign key pair; .key is encrypted by
#                               minisign itself with the passphrase below
#   <role>.passphrase.age       that passphrase, age-encrypted with a master
#                               passphrase you type once
# The public halves are also copied into this repository's keys/ so anyone
# can verify a manifest, and the base64 payloads are printed for
# AgentDeployment.swift in the app.
#
# --only ROLE makes just that key and leaves the other role's files and
# secrets alone: this is how the primary is rotated while fielded app builds
# keep trusting the emergency key (which must survive the rotation). Without
# --only, both are made: the first provisioning, or a full re-key that will
# need an app release.
#
# --set-secrets puts the generated role(s) straight into this repository's
# Actions secrets with gh (MINISIGN_KEY / MINISIGN_PASSWORD for the primary,
# MINISIGN_KEY2 / MINISIGN_PASSWORD2 for the emergency), fed on stdin so no
# secret is on a command line or a clipboard. Without it, the passphrases are
# printed once at the end and you set the secrets yourself.
#
# Everything is built in a private temporary directory and moved into DIR
# only once the keys have signed and verified a probe and the passphrases are
# encrypted; an interruption leaves nothing behind. It refuses to overwrite an
# existing key: move the old files aside first.
set -euo pipefail
umask 077

[ "${BASH_VERSINFO[0]:-0}" -ge 4 ] || { echo "bash 4 or newer is needed (macOS: brew install bash)" >&2; exit 2; }

OUT="$HOME/appfactory/stackguard_keys"
SET_SECRETS=0
ROLES="primary emergency"
while [ $# -gt 0 ]; do
  case "$1" in
    --set-secrets) SET_SECRETS=1 ;;
    --only) shift; case "${1:-}" in primary|emergency) ROLES="$1" ;; *) echo "--only takes primary or emergency" >&2; exit 2 ;; esac ;;
    --*) echo "unknown option $1" >&2; exit 2 ;;
    *) OUT="$1" ;;
  esac
  shift
done
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
REPO="AG-Studio-Apps/stackguard"

for tool in minisign age openssl; do
  command -v "$tool" >/dev/null || { echo "$tool is not installed" >&2; exit 1; }
done
if [ "$SET_SECRETS" -eq 1 ]; then
  command -v gh >/dev/null || { echo "gh is not installed" >&2; exit 1; }
  gh auth status >/dev/null 2>&1 || { echo "gh is not logged in" >&2; exit 1; }
fi

mkdir -p "$OUT"
chmod 700 "$OUT"
for role in $ROLES; do
  for f in "$OUT/$role.key" "$OUT/$role.pub" "$OUT/$role.passphrase.age"; do
    [ -e "$f" ] && { echo "$f already exists; move it aside to rotate" >&2; exit 1; }
  done
done

# Built here; moved into $OUT only at the very end. Nothing survives a failure.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
chmod 700 "$TMP"

PASS_PRIMARY=""
PASS_EMERGENCY=""
passphrase_of() { if [ "$1" = primary ]; then printf '%s' "$PASS_PRIMARY"; else printf '%s' "$PASS_EMERGENCY"; fi; }

PROBE="$TMP/probe.json"
echo '{"probe":true}' > "$PROBE"
for role in $ROLES; do
  # 33 random bytes: 44 base64 characters; minisign encrypts the secret key
  # with it (scrypt), so the .key file is useless without it.
  pass="$(openssl rand -base64 33 | tr -d '\n')"
  if [ "$role" = primary ]; then PASS_PRIMARY="$pass"; else PASS_EMERGENCY="$pass"; fi
  printf '%s\n%s\n' "$pass" "$pass" \
    | minisign -G -p "$TMP/$role.pub" -s "$TMP/$role.key" -c "stackGuard $role signing key" >/dev/null
  chmod 600 "$TMP/$role.key" "$TMP/$role.pub"
  # Prove the key signs and its public half verifies before it is kept.
  printf '%s\n' "$pass" | minisign -S -s "$TMP/$role.key" -t "stackguard probe" -m "$PROBE" >/dev/null
  minisign -V -q -p "$TMP/$role.pub" -m "$PROBE" >/dev/null || { echo "$role key failed to verify" >&2; exit 1; }
  rm -f "$PROBE.minisig"
  echo "generated $role key: id $(sed -n 1p "$TMP/$role.pub" | awk '{print $NF}')"
done

# Each passphrase encrypted with a master passphrase you type now (age asks
# twice). Keep that in your password manager: without it the .key files
# cannot sign and GitHub holds the only working copies of the secrets.
echo
echo "age will now ask for a master passphrase to encrypt the passphrase(s)."
for role in $ROLES; do
  passphrase_of "$role" | age -p -o "$TMP/$role.passphrase.age"
  chmod 600 "$TMP/$role.passphrase.age"
done

if [ "$SET_SECRETS" -eq 1 ]; then
  for role in $ROLES; do
    if [ "$role" = primary ]; then keyname=MINISIGN_KEY; pwname=MINISIGN_PASSWORD; else keyname=MINISIGN_KEY2; pwname=MINISIGN_PASSWORD2; fi
    gh secret set "$keyname" -R "$REPO" < "$TMP/$role.key"
    passphrase_of "$role" | gh secret set "$pwname" -R "$REPO"
    echo "secrets set on $REPO: $keyname, $pwname"
  done
fi

# Keep, and publish the public halves.
for role in $ROLES; do
  mv "$TMP/$role.key" "$TMP/$role.pub" "$TMP/$role.passphrase.age" "$OUT/"
  mkdir -p "$REPO_DIR/keys"
  cp "$OUT/$role.pub" "$REPO_DIR/keys/$role.pub"
  chmod 644 "$REPO_DIR/keys/$role.pub"
done

echo
echo "Roster for the app (Packages/MeshDeckCore/Sources/MeshDeckAlerts/AgentDeployment.swift):"
echo
for role in $ROLES; do
  if [ "$role" = primary ]; then name=primaryKeyBase64; else name=emergencyKeyBase64; fi
  echo "    public static let $name = \"$(sed -n 2p "$OUT/$role.pub")\""
done
echo
echo "Kept in $OUT (0600): <role>.key (encrypted by minisign), <role>.passphrase.age."
echo "Commit keys/*.pub in this repository, and ship the roster in an app build."

if [ "$SET_SECRETS" -eq 0 ]; then
  echo
  echo "Set these as Actions secrets on $REPO (shown once; also in <role>.passphrase.age):"
  for role in $ROLES; do
    if [ "$role" = primary ]; then keyname=MINISIGN_KEY; pwname=MINISIGN_PASSWORD; else keyname=MINISIGN_KEY2; pwname=MINISIGN_PASSWORD2; fi
    echo "  $keyname = contents of $OUT/$role.key"
    echo "  $pwname = $(passphrase_of "$role")"
  done
fi
