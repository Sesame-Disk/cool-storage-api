#!/usr/bin/env bash
# prod-preflight.sh
#
# Two-in-one helper for preparing a production deployment:
#
#   1. --init-env      seed a .env from .env.example and fill in every
#                      auto-generatable secret with fresh random bytes
#   2. (no flag)       validate the current environment and refuse to start
#                      production if any required setting is missing or
#                      still set to a development default
#
# Intended to run either manually on the host:
#
#      ./scripts/prod-preflight.sh --init-env    # once, to seed .env
#      $EDITOR .env                              # fill in IdP / AWS / DB
#      set -a; source .env; set +a
#      ./scripts/prod-preflight.sh               # validate
#
# or as a one-shot service in docker-compose (see the `preflight` service
# in docker-compose.yaml) so `docker compose up` blocks on the validation
# step.
#
# In validation mode the script reads environment variables only — no
# config parsing, no curl, no container runtime probes — so it is usable
# from inside a minimal alpine container and from any CI runner.
#
# Exit codes:
#   0  validation passed, OR --init-env completed successfully
#   1  validation failed (at least one hard-fail check tripped), OR
#      --init-env could not find .env.example
#   2  usage error
#
# You can skip validation checks you deliberately don't want with
# SKIP=comma,list (e.g. SKIP=onlyoffice,oidc).

set -u

PROG="$(basename "$0")"

# Known dev-default / placeholder values that must never reach production.
DEV_DEFAULT_ONLYOFFICE_JWT_SECRET="change-me-to-a-random-string"
DEV_DEFAULT_SHARE_LINK_HMAC_KEY="dev-share-link-hmac-key-change-me"
DEV_DEFAULT_MINIO_USER="minioadmin"
DEV_DEFAULT_MINIO_PASS="minioadmin"

# -------- output --------
if [ -t 2 ] && [ "${NO_COLOR:-}" = "" ]; then
  C_RESET="$(printf '\033[0m')"
  C_RED="$(printf '\033[31m')"
  C_GREEN="$(printf '\033[32m')"
  C_YELLOW="$(printf '\033[33m')"
  C_CYAN="$(printf '\033[36m')"
  C_BOLD="$(printf '\033[1m')"
else
  C_RESET=""; C_RED=""; C_GREEN=""; C_YELLOW=""; C_CYAN=""; C_BOLD=""
fi

PASS_COUNT=0
WARN_COUNT=0
FAIL_COUNT=0
FAIL_MESSAGES=()

pass() {
  PASS_COUNT=$((PASS_COUNT+1))
  printf "  %s[ok]%s   %s\n" "$C_GREEN" "$C_RESET" "$*" >&2
}

warn() {
  WARN_COUNT=$((WARN_COUNT+1))
  printf "  %s[warn]%s %s\n" "$C_YELLOW" "$C_RESET" "$*" >&2
}

fail() {
  FAIL_COUNT=$((FAIL_COUNT+1))
  FAIL_MESSAGES+=("$*")
  printf "  %s[FAIL]%s %s\n" "$C_RED" "$C_RESET" "$*" >&2
}

section() {
  printf "\n%s== %s ==%s\n" "$C_BOLD" "$*" "$C_RESET" >&2
}

# -------- helpers --------
is_set() {
  # True if var is defined and non-empty
  [ -n "${!1:-}" ]
}

is_true() {
  case "${!1:-}" in
    true|TRUE|True|1|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

is_false_or_unset() {
  if [ -z "${!1:-}" ]; then return 0; fi
  case "${!1}" in
    false|FALSE|False|0|no|NO|off|OFF) return 0 ;;
    *) return 1 ;;
  esac
}

min_len() {
  local var="$1"; local want="$2"
  local val="${!var:-}"
  [ "${#val}" -ge "$want" ]
}

is_skipped() {
  local name="$1"
  case ",${SKIP:-}," in
    *",$name,"*) return 0 ;;
  esac
  return 1
}

# ========================================================================
# --init-env helpers (only used when --init-env is passed)
# ========================================================================

gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  elif [ -r /dev/urandom ]; then
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  else
    echo "ERROR: no openssl and no /dev/urandom; cannot generate secrets" >&2
    exit 1
  fi
}

# Known dev-default values that --init-env should always replace.
is_known_default() {
  local key="$1" val="$2"
  case "$key" in
    ONLYOFFICE_JWT_SECRET)
      [ "$val" = "$DEV_DEFAULT_ONLYOFFICE_JWT_SECRET" ] && return 0
      ;;
    SHARE_LINK_HMAC_KEY)
      [ "$val" = "$DEV_DEFAULT_SHARE_LINK_HMAC_KEY" ] && return 0
      [ "$val" = "dev-share-link-hmac-key" ] && return 0
      ;;
  esac
  return 1
}

# Read the current value of KEY from an env file. Returns "" if unset or empty.
env_file_get() {
  local file="$1" key="$2"
  awk -v k="$key" '
    /^[[:space:]]*#/   { next }
    $0 ~ "^[[:space:]]*"k"=" {
      sub("^[[:space:]]*"k"=", "")
      # strip optional surrounding quotes and CR
      gsub(/\r$/, "")
      if (substr($0,1,1) == "\"" && substr($0,length($0),1) == "\"") {
        print substr($0,2,length($0)-2); exit
      }
      if (substr($0,1,1) == "\047" && substr($0,length($0),1) == "\047") {
        print substr($0,2,length($0)-2); exit
      }
      print; exit
    }
  ' "$file"
}

# Set KEY=VALUE in an env file. If the key already exists (anywhere in the
# file, ignoring commented lines), rewrite that line in place. Otherwise
# append. Portable across GNU/BSD sed by writing to a temp file.
env_file_set() {
  local file="$1" key="$2" val="$3"
  local tmp; tmp="$(mktemp)"
  awk -v k="$key" -v v="$val" '
    BEGIN { replaced=0 }
    /^[[:space:]]*#/ { print; next }
    {
      if ($0 ~ "^[[:space:]]*"k"=") {
        print k"="v
        replaced=1
        next
      }
      print
    }
    END {
      if (!replaced) print k"="v
    }
  ' "$file" > "$tmp"
  mv "$tmp" "$file"
}

# Generate (or replace a known default) for a single key in .env. Leaves
# any user-supplied value untouched.
maybe_generate_in_env_file() {
  local file="$1" key="$2"
  local cur; cur="$(env_file_get "$file" "$key")"
  if [ -z "$cur" ]; then
    env_file_set "$file" "$key" "$(gen_secret)"
    printf "  %s[gen]%s   generated %s\n" "$C_GREEN" "$C_RESET" "$key" >&2
    return 0
  fi
  if is_known_default "$key" "$cur"; then
    env_file_set "$file" "$key" "$(gen_secret)"
    printf "  %s[gen]%s   replaced dev default for %s\n" "$C_YELLOW" "$C_RESET" "$key" >&2
    return 0
  fi
  printf "  %s[keep]%s  %s already set — not touched\n" "$C_CYAN" "$C_RESET" "$key" >&2
  return 1
}

do_init_env() {
  local env_file=".env"
  local template=".env.example"

  section "sesamefs .env bootstrap"

  if [ ! -f "$env_file" ]; then
    if [ ! -f "$template" ]; then
      printf "  %s[FAIL]%s Neither %s nor %s found. Run from the repo root.\n" \
        "$C_RED" "$C_RESET" "$env_file" "$template" >&2
      exit 1
    fi
    cp "$template" "$env_file"
    printf "  %s[copy]%s  %s -> %s\n" "$C_GREEN" "$C_RESET" "$template" "$env_file" >&2
  else
    printf "  %s[keep]%s  %s already exists (will only fill empty values)\n" "$C_CYAN" "$C_RESET" "$env_file" >&2
  fi

  echo >&2
  printf "%sGenerating random values for auto-generatable secrets:%s\n" "$C_BOLD" "$C_RESET" >&2
  maybe_generate_in_env_file "$env_file" SHARE_LINK_HMAC_KEY
  maybe_generate_in_env_file "$env_file" ONLYOFFICE_JWT_SECRET
  maybe_generate_in_env_file "$env_file" OIDC_JWT_SIGNING_KEY

  echo >&2
  printf "%sStill required — these come from external systems, cannot be auto-generated:%s\n" "$C_BOLD" "$C_RESET" >&2

  local missing=0
  needs_manual() {
    local key="$1" note="$2"
    local cur; cur="$(env_file_get "$env_file" "$key")"
    if [ -z "$cur" ]; then
      printf "  %s[MISSING]%s %s — %s\n" "$C_RED" "$C_RESET" "$key" "$note" >&2
      missing=$((missing+1))
    else
      printf "  %s[ok]%s      %s is set\n" "$C_GREEN" "$C_RESET" "$key" >&2
    fi
  }

  needs_manual SERVER_URL                    "your public base URL"
  needs_manual BILLING_URL                   "accounts portal billing URL"
  needs_manual ACCOUNTS_PASSWORD_CHANGE_URL  "accounts portal password-change URL"
  needs_manual ACCOUNTS_DELETE_ACCOUNT_URL   "accounts portal delete-account URL"
  needs_manual OIDC_ISSUER                   "your IdP issuer URL"
  needs_manual OIDC_CLIENT_ID                "issued by your IdP"
  needs_manual OIDC_CLIENT_SECRET            "issued by your IdP"
  needs_manual OIDC_REDIRECT_URIS            "your https://<host>/sso callback"
  needs_manual S3_BUCKET                     "your production S3 bucket"
  needs_manual S3_REGION                     "your AWS region"
  needs_manual AWS_ACCESS_KEY_ID             "from AWS IAM — cannot be generated"
  needs_manual AWS_SECRET_ACCESS_KEY         "from AWS IAM — cannot be generated"
  needs_manual CASSANDRA_HOSTS               "your Cassandra contact points"
  needs_manual CASSANDRA_USERNAME            "Cassandra role with access to the keyspace"
  needs_manual CASSANDRA_PASSWORD            "must match the Cassandra side"

  echo >&2
  if [ "$missing" -gt 0 ]; then
    printf "%s$missing values still need to be filled in manually.%s\n" "$C_YELLOW" "$C_RESET" >&2
    printf "Edit %s and then run:\n" "$env_file" >&2
    printf "    set -a; source %s; set +a\n" "$env_file" >&2
    printf "    ./scripts/prod-preflight.sh\n" >&2
  else
    printf "%sAll values set.%s Run the validator next:\n" "$C_GREEN" "$C_RESET" >&2
    printf "    set -a; source %s; set +a\n" "$env_file" >&2
    printf "    ./scripts/prod-preflight.sh\n" >&2
  fi
}

# ========================================================================
# Checks
# ========================================================================

check_auth_flags() {
  section "Auth dev flags"

  if is_true AUTH_DEV_MODE; then
    fail "AUTH_DEV_MODE is set to a truthy value. It MUST be false in production."
    fail "  Risk: any request without credentials becomes superadmin."
    fail "  Fix:  export AUTH_DEV_MODE=false (or remove the override)"
  else
    pass "AUTH_DEV_MODE is false/unset"
  fi

}

check_share_link_hmac_key() {
  section "Share-link HMAC key"

  if ! is_set SHARE_LINK_HMAC_KEY; then
    fail "SHARE_LINK_HMAC_KEY is unset. Required for password-protected share/upload links."
    fail "  Fix:  export SHARE_LINK_HMAC_KEY=\$(openssl rand -hex 32)"
    return
  fi
  if [ "$SHARE_LINK_HMAC_KEY" = "$DEV_DEFAULT_SHARE_LINK_HMAC_KEY" ]; then
    fail "SHARE_LINK_HMAC_KEY is set to the documented dev default."
    fail "  Fix:  rotate to a fresh random value"
    return
  fi
  if ! min_len SHARE_LINK_HMAC_KEY 32; then
    fail "SHARE_LINK_HMAC_KEY is shorter than 32 characters (got ${#SHARE_LINK_HMAC_KEY})."
    return
  fi
  pass "SHARE_LINK_HMAC_KEY is set (${#SHARE_LINK_HMAC_KEY} chars) and not a known default"
}

check_external_urls() {
  section "External URLs"

  local required=(SERVER_URL BILLING_URL ACCOUNTS_PASSWORD_CHANGE_URL ACCOUNTS_DELETE_ACCOUNT_URL)
  for v in "${required[@]}"; do
    if ! is_set "$v"; then
      fail "$v is unset. Required for user-facing redirects."
    elif [[ "${!v}" != https://* ]] && [[ "${!v}" != http://* ]]; then
      fail "$v does not look like a URL: ${!v}"
    else
      pass "$v is set"
    fi
  done
}

check_oidc() {
  section "OIDC"
  if is_skipped oidc; then
    warn "OIDC checks skipped via SKIP=oidc"
    return
  fi

  if ! is_true OIDC_ENABLED; then
    warn "OIDC_ENABLED is not true — skipping OIDC deep checks (is this intentional?)"
    return
  fi

  local required=(OIDC_ISSUER OIDC_CLIENT_ID OIDC_CLIENT_SECRET OIDC_JWT_SIGNING_KEY OIDC_REDIRECT_URIS)
  for v in "${required[@]}"; do
    if ! is_set "$v"; then
      fail "$v is unset. Required when OIDC_ENABLED=true."
    else
      pass "$v is set"
    fi
  done

  if is_set OIDC_JWT_SIGNING_KEY && ! min_len OIDC_JWT_SIGNING_KEY 32; then
    fail "OIDC_JWT_SIGNING_KEY is shorter than 32 characters (got ${#OIDC_JWT_SIGNING_KEY})."
  fi

  if is_false_or_unset OIDC_REQUIRE_PKCE; then
    warn "OIDC_REQUIRE_PKCE is not true. PKCE should be required for browser login flows."
  else
    pass "OIDC_REQUIRE_PKCE is true"
  fi

  if [[ "${OIDC_ISSUER:-}" == http://* ]]; then
    fail "OIDC_ISSUER uses http://, not https://. Refuse to establish identity over cleartext."
  fi
}

check_onlyoffice() {
  section "OnlyOffice"
  if is_skipped onlyoffice; then
    warn "OnlyOffice checks skipped via SKIP=onlyoffice"
    return
  fi

  if is_false_or_unset ONLYOFFICE_ENABLED; then
    warn "ONLYOFFICE_ENABLED is not true — skipping OnlyOffice deep checks"
    return
  fi

  if ! is_set ONLYOFFICE_JWT_SECRET; then
    fail "ONLYOFFICE_JWT_SECRET is unset. Required when OnlyOffice is enabled."
    fail "  Fix:  export ONLYOFFICE_JWT_SECRET=\$(openssl rand -hex 32)"
  elif [ "$ONLYOFFICE_JWT_SECRET" = "$DEV_DEFAULT_ONLYOFFICE_JWT_SECRET" ]; then
    fail "ONLYOFFICE_JWT_SECRET is still the documented dev default ('change-me-...')."
    fail "  Risk: the OnlyOffice editor callback can be forged by any attacker."
    fail "  Fix:  rotate to a fresh random value of at least 32 chars"
  elif ! min_len ONLYOFFICE_JWT_SECRET 32; then
    fail "ONLYOFFICE_JWT_SECRET is shorter than 32 characters (got ${#ONLYOFFICE_JWT_SECRET})."
  else
    pass "ONLYOFFICE_JWT_SECRET is set (${#ONLYOFFICE_JWT_SECRET} chars) and not a known default"
  fi

  if is_set ONLYOFFICE_API_JS_URL && [[ "$ONLYOFFICE_API_JS_URL" == http://* ]]; then
    warn "ONLYOFFICE_API_JS_URL uses http:// — browsers will likely block mixed content on an HTTPS site."
  fi
}

check_object_storage() {
  section "Object storage"
  if is_skipped storage; then
    warn "Storage checks skipped via SKIP=storage"
    return
  fi

  local required=(S3_BUCKET S3_REGION AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY)
  for v in "${required[@]}"; do
    if ! is_set "$v"; then
      fail "$v is unset. Required for S3-backed storage."
    else
      pass "$v is set"
    fi
  done

  if [ "${AWS_ACCESS_KEY_ID:-}" = "$DEV_DEFAULT_MINIO_USER" ] \
     || [ "${AWS_SECRET_ACCESS_KEY:-}" = "$DEV_DEFAULT_MINIO_PASS" ]; then
    fail "AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are the MinIO dev defaults ('minioadmin')."
  fi

  if is_set S3_ENDPOINT && [[ "$S3_ENDPOINT" == *"minio"* ]]; then
    warn "S3_ENDPOINT points at a host named 'minio'. If this is truly production, confirm it is not the bundled dev MinIO."
  fi
}

check_database() {
  section "Database"
  if is_skipped database; then
    warn "Database checks skipped via SKIP=database"
    return
  fi

  if ! is_set CASSANDRA_HOSTS; then
    fail "CASSANDRA_HOSTS is unset."
    return
  fi
  pass "CASSANDRA_HOSTS is set"

  if ! is_set CASSANDRA_USERNAME; then
    warn "CASSANDRA_USERNAME is unset. Only acceptable if Cassandra is on a trusted private network."
  else
    pass "CASSANDRA_USERNAME is set"
  fi

  if ! is_set CASSANDRA_PASSWORD; then
    warn "CASSANDRA_PASSWORD is unset. Only acceptable if Cassandra is on a trusted private network."
  else
    pass "CASSANDRA_PASSWORD is set"
  fi
}

check_config_file() {
  section "Config file"
  if is_skipped config; then
    warn "Config file checks skipped via SKIP=config"
    return
  fi

  local cfg="${CONFIG_PATH:-/app/config.yaml}"
  if [ ! -r "$cfg" ]; then
    warn "Config file not readable at $cfg (skipping content check)"
    return
  fi

  if grep -Eq '^[[:space:]]*dev_mode:[[:space:]]*true' "$cfg"; then
    fail "Config file $cfg has auth.dev_mode: true baked in. Mount configs/config.prod.yaml over it."
  fi
  if grep -Eq '^[[:space:]]*-[[:space:]]*"\*"' "$cfg" \
     && grep -q 'allowed_origins' "$cfg"; then
    warn "Config file $cfg has cors.allowed_origins: [\"*\"]. sesamefs uses cookie auth; tighten to an explicit allow-list."
  fi
  if grep -Eq 'change-me-to-a-random-string' "$cfg"; then
    fail "Config file $cfg contains the 'change-me-to-a-random-string' placeholder."
  fi
  pass "Config file content looks sane"
}

# ========================================================================
# Main
# ========================================================================

usage() {
  cat <<EOF
$PROG — production readiness tool

Usage:
  $PROG                 validate the current environment (default)
  $PROG --init-env      seed .env from .env.example and fill in every
                        auto-generatable secret with fresh random bytes
  $PROG --help

Environment (validation mode):
  SKIP=list   Comma-separated list of check groups to skip.
              Valid groups: oidc, onlyoffice, storage, database, config
              Example: SKIP=onlyoffice,storage $PROG

  CONFIG_PATH Path to the baked config file (default: /app/config.yaml)

Exit codes:
  0   validation passed, OR --init-env completed successfully
  1   validation failed, OR --init-env could not find .env.example
  2   usage error
EOF
}

case "${1:-}" in
  -h|--help)   usage; exit 0 ;;
  --init-env)  do_init_env; exit 0 ;;
  "")          ;;
  *)           usage; exit 2 ;;
esac

section "sesamefs production preflight"
printf "  time:   %s\n"  "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >&2
printf "  SKIP:   %s\n"  "${SKIP:-<none>}" >&2

check_auth_flags
check_share_link_hmac_key
check_external_urls
check_oidc
check_onlyoffice
check_object_storage
check_database
check_config_file

section "Result"
printf "  pass:  %s%d%s\n" "$C_GREEN"  "$PASS_COUNT" "$C_RESET" >&2
printf "  warn:  %s%d%s\n" "$C_YELLOW" "$WARN_COUNT" "$C_RESET" >&2
printf "  fail:  %s%d%s\n" "$C_RED"    "$FAIL_COUNT" "$C_RESET" >&2

if [ "$FAIL_COUNT" -gt 0 ]; then
  printf "\n%sPreflight FAILED.%s Fix the items above and re-run.\n" "$C_RED" "$C_RESET" >&2
  exit 1
fi

printf "\n%sPreflight OK.%s Safe to launch sesamefs.\n" "$C_GREEN" "$C_RESET" >&2
exit 0
