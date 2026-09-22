#!/usr/bin/env bash
# Generates the stackGuard channel signing roster: a primary key (the publish
# workflow signs with it) and an emergency key (kept offline; the rotation
# story, which the app also trusts). Run once, on a machine you trust, by the
# maintainer.
#
#   scripts/provision-keys.sh [DIR] [--set-secrets]
#
# DIR (default ~/appfactory/stackguard_keys) is a private directory outside
# any repository. It receives:
#   primary.key   primary.pub     the minisign key pair; .key is encrypted by
#   emergency.key emergency.pub   minisign itself with the passphrase below
#   PASSPHRASES.age               both passphrases, age-encrypted with a master
#                                 passphrase you type once
# The two public keys are also copied into this repository's keys/ so anyone
# can verify a manifest, and the base64 payloads are printed for
# AgentDeployment.swift in the app.
#
# --set-secrets puts the four values straight into this repository's Actions
# secrets with gh (MINISIGN_KEY / MINISIGN_PASSWORD for the primary key,
# MINISIGN_KEY2 / MINISIGN_PASSWORD2 for the emergency key), so nothing
# secret is ever pasted through a clipboard. Without it, the script prints the
# passphrases once at the end and you set the secrets yourself.
#
# It refuses to overwrite an existing key: to rotate, move the old files
# aside first, then re-run and ship an app build with the new roster.
set -euo pipefail
umask 077

OUT="$HOME/appfactory/stackguard_keys"
SET_SECRETS=0
for arg in "$@"; do
  case "$arg" in
    --set-secrets) SET_SECRETS=1 ;;
    --*) echo "unknown option $arg" >&2; exit 2 ;;
    *) OUT="$arg" ;;
  esac
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
for role in primary emergency; do
  if [ -e "$OUT/$role.key" ] || [ -e "$OUT/$role.pub" ]; then
    echo "$OUT/$role.key or .pub already exists; move it aside to rotate" >&2
    exit 1
  fi
done

declare -A PASS
for role in primary emergency; do
  # 33 random bytes → 44 base64 characters, no newline; minisign encrypts the
  # secret key with it (scrypt), so the .key file is useless without it.
  PASS[$role]="$(openssl rand -base64 33 | tr -d '\n')"
  printf '%s\n%s\n' "${PASS[$role]}" "${PASS[$role]}" \
    | minisign -G -p "$OUT/$role.pub" -s "$OUT/$role.key" -c "stackGuard $role signing key" >/dev/null
  chmod 600 "$OUT/$role.key" "$OUT/$role.pub"
  echo "generated $role key: id $(sed -n 1p "$OUT/$role.pub" | awk '{print $NF}')"
done

# Prove each key signs and its public half verifies before anything is kept.
PROBE="$(mktemp)"
echo '{"probe":true}' > "$PROBE"
for role in primary emergency; do
  echo "${PASS[$role]}" | minisign -S -s "$OUT/$role.key" -t "stackguard probe" -m "$PROBE" >/dev/null
  minisign -V -q -p "$OUT/$role.pub" -m "$PROBE" >/dev/null || { echo "$role key failed to verify" >&2; exit 1; }
done
rm -f "$PROBE" "$PROBE.minisig"

# The passphrases, encrypted once with a master passphrase you type now.
# Keep that in your password manager; without it the .key files cannot sign.
echo
echo "age will now ask for a master passphrase to encrypt the two passphrases."
printf 'primary=%s\nemergency=%s\n' "${PASS[primary]}" "${PASS[emergency]}" | age -p -o "$OUT/PASSPHRASES.age"
chmod 600 "$OUT/PASSPHRASES.age"

# Public halves into the repository, for anyone verifying a manifest.
mkdir -p "$REPO_DIR/keys"
cp "$OUT/primary.pub" "$REPO_DIR/keys/primary.pub"
cp "$OUT/emergency.pub" "$REPO_DIR/keys/emergency.pub"
chmod 644 "$REPO_DIR/keys/primary.pub" "$REPO_DIR/keys/emergency.pub"

if [ "$SET_SECRETS" -eq 1 ]; then
  gh secret set MINISIGN_KEY -R "$REPO" < "$OUT/primary.key"
  gh secret set MINISIGN_PASSWORD -R "$REPO" --body "${PASS[primary]}"
  gh secret set MINISIGN_KEY2 -R "$REPO" < "$OUT/emergency.key"
  gh secret set MINISIGN_PASSWORD2 -R "$REPO" --body "${PASS[emergency]}"
  echo "secrets set on $REPO: MINISIGN_KEY, MINISIGN_PASSWORD, MINISIGN_KEY2, MINISIGN_PASSWORD2"
fi

cat <<SWIFT

Roster for the app (Packages/MeshDeckCore/Sources/MeshDeckAlerts/AgentDeployment.swift):

    public static let primaryKeyBase64 = "$(sed -n 2p "$OUT/primary.pub")"
    public static let emergencyKeyBase64 = "$(sed -n 2p "$OUT/emergency.pub")"

Kept in $OUT (0600): primary.key, emergency.key (encrypted by minisign),
PASSPHRASES.age (the passphrases, encrypted with your master passphrase).
Commit keys/primary.pub and keys/emergency.pub in this repository.
SWIFT

if [ "$SET_SECRETS" -eq 0 ]; then
  cat <<SECRETS

Set these as Actions secrets on $REPO (shown once; they are also in PASSPHRASES.age):
  MINISIGN_KEY        = contents of $OUT/primary.key
  MINISIGN_PASSWORD   = ${PASS[primary]}
  MINISIGN_KEY2       = contents of $OUT/emergency.key
  MINISIGN_PASSWORD2  = ${PASS[emergency]}
SECRETS
fi
