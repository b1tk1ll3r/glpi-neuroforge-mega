#!/bin/sh
set -eu
fail=0
bad() { echo "secret-scan: ERROR: $*" >&2; fail=1; }

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
TMP_BASE=${TMPDIR:-/tmp}/neuroforge-secret-scan.$$
FILES="$TMP_BASE.files"
KEYS="$TMP_BASE.keys"
TOKENS="$TMP_BASE.tokens"
trap 'rm -f "$FILES" "$KEYS" "$TOKENS"' EXIT HUP INT TERM

# Release archives intentionally do not contain .git. Use Git's tracked-file view
# when available, otherwise scan every regular file in the extracted release.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git ls-files > "$FILES"
  HAVE_GIT=true
else
  find . -type f ! -path './.git/*' -print | sed 's#^\./##' | sort > "$FILES"
  HAVE_GIT=false
fi

# Private keys and common live-token shapes must not be committed. Placeholders in
# templates/docs are intentionally allowed.
if grep -E '(^|/)\.env$|\.pem$|\.p12$|\.pfx$|(^|/)id_rsa$|(^|/)id_ed25519$' "$FILES" \
    | grep -Ev '^deployments/[^/]+/\.env$' >/dev/null; then
  bad "private environment/key material found"
fi

# Deployment role .env files are intentionally shipped as complete templates.
# Reject them if a sensitive field is populated with anything other than an
# empty value or an explicit CHANGE_ME placeholder.
for f in deployments/*/.env; do
  [ -f "$f" ] || continue
  awk -F= '
    /^[[:space:]]*#/ || NF < 2 { next }
    $1 ~ /(PASSWORD|SECRET|TOKEN|API_KEY|CLIENT_SECRET)$/ {
      v=$0; sub(/^[^=]*=/,"",v)
      if (v != "" && v !~ /^CHANGE_ME/) {
        print FILENAME ": unsafe populated secret field " $1 > "/dev/stderr"
        bad=1
      }
    }
    END { exit bad ? 1 : 0 }
  ' "$f" || bad "deployment .env contains a populated secret: $f"
done

if [ "$HAVE_GIT" = true ]; then
  if git grep -nE -- '-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----' -- ':!*.example' ':!*.md' >"$KEYS" 2>/dev/null; then
    cat "$KEYS" >&2; bad "private key material found"
  fi
  if git grep -nE -- '(AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{24,})' -- ':!*.example' >"$TOKENS" 2>/dev/null; then
    cat "$TOKENS" >&2; bad "token-like credential found"
  fi
else
  # Fallback for release ZIPs: recurse through extracted source while preserving
  # the same exclusions as the Git-backed scan.
  if grep -RInE --exclude='*.example' --exclude='*.md' --exclude-dir='.git' -- \
      '-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----' . >"$KEYS" 2>/dev/null; then
    cat "$KEYS" >&2; bad "private key material found"
  fi
  if grep -RInE --exclude='*.example' --exclude-dir='.git' -- \
      '(AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{24,})' . >"$TOKENS" 2>/dev/null; then
    cat "$TOKENS" >&2; bad "token-like credential found"
  fi
fi

# Reject accidental binary blobs outside explicitly expected assets.
while IFS= read -r f; do
  [ -f "$f" ] || continue
  case "$f" in *.png|*.jpg|*.jpeg|*.gif|*.ico|*.woff|*.woff2|*.pdf|*.zip) continue;; esac
  if [ "$(LC_ALL=C grep -Il . "$f" 2>/dev/null || true)" = "" ] && [ -s "$f" ]; then
    bad "unexpected binary file: $f"
  fi
done < "$FILES"

[ "$fail" -eq 0 ] || exit 1
echo "secret-scan: passed"
