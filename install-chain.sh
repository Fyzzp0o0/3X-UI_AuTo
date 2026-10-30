#!/usr/bin/env bash
# ChainForge installer for the 3X-UI AuTo fork (Linux + systemd).
# Usage: bash install-chain.sh [options]   (see --help)
set -euo pipefail

REPO="${CHAIN_REPO:-Fyzzp0o0/3X-UI_AuTo}"
INSTALL_DIR="/usr/local/x-ui"
DATA_DIR="/etc/x-ui"
LOG_DIR="/var/log/x-ui"
ENV_FILE="/etc/default/x-ui"
SERVICE_FILE="/etc/systemd/system/x-ui.service"
SERVICE_NAME="x-ui"

MODE="install"
ARCHIVE=""
VERSION=""
LOCAL_TARBALL=""
PORT=""
USERNAME=""
PASSWORD=""
NO_START=0
PURGE=0

if [ -t 1 ]; then
  C_GREEN='\033[32m'; C_YELLOW='\033[33m'; C_RED='\033[31m'; C_RESET='\033[0m'
else
  C_GREEN=''; C_YELLOW=''; C_RED=''; C_RESET=''
fi
info() { printf "${C_GREEN}%s${C_RESET}\n" "$*"; }
warn() { printf "${C_YELLOW}%s${C_RESET}\n" "$*" >&2; }
die()  { printf "${C_RED}%s${C_RESET}\n" "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
ChainForge installer for 3X-UI AuTo (Linux/systemd)

Usage:
  bash install-chain.sh [options]
  bash install-chain.sh --uninstall [--purge]
  bash install-chain.sh --local x-ui-linux-amd64.tar.gz [--no-start]

Options:
  --version <tag>    Release tag to install (default: latest release)
  --local <file>     Install from a local .tar.gz instead of downloading
  --port <n>         Panel port for a fresh install (default: 2053)
  --username <name>  Admin username for a fresh install (default: admin)
  --password <pass>  Admin password (default: generated and printed once)
  --no-start         Install files and settings but do not start the service
  --uninstall        Stop and remove the panel (keeps /etc/x-ui unless --purge)
  --purge            With --uninstall, also delete /etc/x-ui, logs and env file
  -h, --help         Show this help

Environment:
  CHAIN_REPO         GitHub repo slug (default: Fyzzp0o0/3X-UI_AuTo)
  CHAIN_VERSION      Same as --version

Fresh installs enable credential encryption (NODE_TOKEN_ENCRYPTION=required)
and write a 32-byte key into /etc/default/x-ui (mode 0600). Upgrades keep the
existing env file, database and settings untouched.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)  VERSION="${2:-}"; [ -n "$VERSION" ] || die "--version needs a value"; shift 2 ;;
    --local)    LOCAL_TARBALL="${2:-}"; [ -n "$LOCAL_TARBALL" ] || die "--local needs a file"; shift 2 ;;
    --port)     PORT="${2:-}"; [ -n "$PORT" ] || die "--port needs a value"; shift 2 ;;
    --username) USERNAME="${2:-}"; [ -n "$USERNAME" ] || die "--username needs a value"; shift 2 ;;
    --password) PASSWORD="${2:-}"; [ -n "$PASSWORD" ] || die "--password needs a value"; shift 2 ;;
    --no-start) NO_START=1; shift ;;
    --uninstall) MODE="uninstall"; shift ;;
    --purge)    PURGE=1; shift ;;
    -h|--help)  usage; exit 0 ;;
    *)          die "unknown option: $1 (see --help)" ;;
  esac
done

[ -n "${CHAIN_VERSION:-}" ] && [ -z "$VERSION" ] && VERSION="$CHAIN_VERSION"

require_linux() {
  [ "$(uname -s)" = "Linux" ] || die "This installer targets Linux + systemd. Use --local on other systems."
  command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found)."
}

require_root() {
  [ "$(id -u)" -eq 0 ] || die "Please run as root (sudo bash install-chain.sh ...)."
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "Unsupported CPU architecture: $(uname -m). Supported: x86_64, aarch64." ;;
  esac
}

stop_service() {
  if systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1; then
    systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  fi
}

service_active() {
  systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null
}

panel_port() {
  local value
  value="$(cd "$INSTALL_DIR" && ./x-ui setting -show true 2>/dev/null | awk -F': ' '/^port:/{print $2; exit}')"
  printf '%s' "${value:-2053}"
}

write_env_file() {
  if [ -f "$ENV_FILE" ]; then
    chmod 600 "$ENV_FILE"
    info "Keeping existing $ENV_FILE (database key and settings preserved)."
    return
  fi
  local key old_umask
  key="$(openssl rand -base64 32 | tr -d '\n')"
  old_umask="$(umask)"
  umask 077
  cat > "$ENV_FILE" <<EOF
# ChainForge environment (mode 0600). Keep this file: it contains the key that
# encrypts proxy credentials and node API tokens.
XUI_DB_FOLDER=$DATA_DIR
XUI_LOG_FOLDER=$LOG_DIR
XUI_CHAIN_ENABLED=true
NODE_TOKEN_ENCRYPTION=required
XUI_NODE_TOKEN_KEY=$key
EOF
  chmod 600 "$ENV_FILE"
  umask "$old_umask"
  info "Created $ENV_FILE with a fresh credential-encryption key."
}

write_service() {
  cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=3X-UI AuTo (ChainForge) panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-$ENV_FILE
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/x-ui
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$SERVICE_FILE"
  systemctl daemon-reload
}

install_cli() {
  if [ -f "$INSTALL_DIR/x-ui.sh" ]; then
    install -m 755 "$INSTALL_DIR/x-ui.sh" /usr/bin/x-ui
  fi
}

apply_settings() {
  local first_install="$1"
  local user="$USERNAME" pass="$PASSWORD" port="$PORT"
  local generated=""
  if [ "$first_install" -eq 1 ]; then
    [ -n "$user" ] || user="admin"
    [ -n "$port" ] || port="2053"
    if [ -z "$pass" ]; then
      pass="$(openssl rand -base64 12 | tr -d '/+=' | cut -c1-12)"
      generated="$pass"
    fi
  fi
  [ -n "$user$pass$port" ] || return 0

  local args=()
  [ -n "$user" ] && args+=(-username "$user")
  [ -n "$pass" ] && args+=(-password "$pass")
  [ -n "$port" ] && args+=(-port "$port")
  ( cd "$INSTALL_DIR" && ./x-ui setting "${args[@]}" >/dev/null )

  if [ -n "$generated" ]; then
    printf '\n  Username: %s\n  Password: %s\n  Port:     %s\n\n' "$user" "$generated" "$port"
    warn "Save this password now; it will not be shown again."
  fi
}

wait_for_port() {
  local port="$1" i=0
  command -v curl >/dev/null 2>&1 || return 0
  while [ "$i" -lt 20 ]; do
    if curl -s -o /dev/null --max-time 2 "http://127.0.0.1:${port}/"; then
      return 0
    fi
    i=$((i + 1)); sleep 1
  done
  return 1
}

public_ip() {
  local ip=""
  if command -v curl >/dev/null 2>&1; then
    ip="$(curl -4 -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true)"
  fi
  if [ -z "$ip" ]; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  printf '%s' "${ip:-<server-ip>}"
}

uninstall() {
  require_linux
  require_root
  info "Stopping and removing the panel service..."
  systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
  systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
  rm -f "$SERVICE_FILE"
  systemctl daemon-reload || true
  rm -rf "$INSTALL_DIR"
  rm -f /usr/bin/x-ui
  if [ "$PURGE" -eq 1 ]; then
    warn "Purging database, logs and environment (credential key included)."
    rm -rf "$DATA_DIR" "$LOG_DIR" "$ENV_FILE"
  else
    info "Kept $DATA_DIR (database + credential key live in $ENV_FILE)."
  fi
  info "Uninstalled. Re-run without --uninstall to install again."
  exit 0
}

resolve_version() {
  [ -n "$VERSION" ] && return 0
  local api="https://api.github.com/repos/${REPO}/releases/latest"
  VERSION="$(curl -fsSL --max-time 20 "$api" 2>/dev/null \
    | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
    | head -n1 | sed 's/.*"\([^"]*\)"$/\1/')" || true
  [ -n "$VERSION" ] || die "No release found for ${REPO}. Publish one first (GitHub Actions → Chain Release) or pass --local <tar.gz>."
}

download_release() {
  local name="x-ui-linux-${ARCH}.tar.gz"
  local url="https://github.com/${REPO}/releases/download/${VERSION}/${name}"
  ARCHIVE="$WORK_DIR/$name"
  info "Downloading ${VERSION} (${ARCH})..."
  curl -fL --retry 3 --retry-delay 2 -o "$ARCHIVE" "$url" \
    || die "Download failed: $url"
  if curl -fsL --retry 2 -o "$ARCHIVE.sha256" "${url}.sha256" 2>/dev/null; then
    ( cd "$WORK_DIR" && sha256sum -c "${name}.sha256" >/dev/null ) \
      || die "Checksum verification failed for $url"
    info "Checksum verified."
  else
    warn "No .sha256 asset found; skipping checksum verification."
  fi
}

do_install() {
  require_linux
  require_root
  command -v curl >/dev/null 2>&1 || die "curl is required."
  command -v tar >/dev/null 2>&1 || die "tar is required."
  command -v openssl >/dev/null 2>&1 || die "openssl is required."
  detect_arch

  local was_installed=0
  [ -x "$INSTALL_DIR/x-ui" ] && was_installed=1

  WORK_DIR="$(mktemp -d)"
  trap 'rm -rf "$WORK_DIR"' EXIT
  mkdir -p "$WORK_DIR/src"

  if [ -n "$LOCAL_TARBALL" ]; then
    [ -f "$LOCAL_TARBALL" ] || die "Local tarball not found: $LOCAL_TARBALL"
    ARCHIVE="$WORK_DIR/local.tar.gz"
    cp "$LOCAL_TARBALL" "$ARCHIVE"
    info "Installing from local archive: $LOCAL_TARBALL"
  else
    resolve_version
    download_release
  fi

  tar -xzf "$ARCHIVE" -C "$WORK_DIR/src"
  [ -x "$WORK_DIR/src/x-ui/x-ui" ] || die "Archive does not contain x-ui/x-ui."

  info "Stopping any running panel..."
  stop_service
  if [ -f "$DATA_DIR/x-ui.db" ]; then
    mkdir -p "$DATA_DIR/backups"
    cp "$DATA_DIR/x-ui.db" "$DATA_DIR/backups/x-ui-$(date +%Y%m%d-%H%M%S).db" 2>/dev/null || true
    info "Database backed up under $DATA_DIR/backups/."
  fi

  mkdir -p "$INSTALL_DIR" "$DATA_DIR" "$LOG_DIR"
  info "Installing files to $INSTALL_DIR ..."
  cp -a "$WORK_DIR/src/x-ui/." "$INSTALL_DIR/"
  chmod +x "$INSTALL_DIR/x-ui"

  # The panel resolves bin/xray-linux-<arch>; older artifacts shipped bin/xray.
  if [ -x "$INSTALL_DIR/bin/xray" ] && [ ! -e "$INSTALL_DIR/bin/xray-linux-${ARCH}" ]; then
    mv "$INSTALL_DIR/bin/xray" "$INSTALL_DIR/bin/xray-linux-${ARCH}"
  fi
  if [ -x "$INSTALL_DIR/bin/xray-linux-${ARCH}" ]; then
    chmod +x "$INSTALL_DIR/bin/xray-linux-${ARCH}"
  else
    warn "bin/xray-linux-${ARCH} is missing from the archive; the panel will start but the core cannot run."
  fi

  write_env_file
  write_service
  install_cli

  # Commands below must see the same DB folder as the service.
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a

  if [ "$was_installed" -eq 1 ]; then
    apply_settings 0
    info "Upgrade complete; existing database and settings were kept."
  else
    apply_settings 1
  fi

  if [ "$NO_START" -eq 1 ]; then
    warn "Service not started (--no-start). Start it with: systemctl start x-ui"
    return 0
  fi

  systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
  systemctl restart "$SERVICE_NAME"
  local port; port="$(panel_port)"
  if service_active; then
    info "Panel is running."
  else
    warn "Service did not report active; check: journalctl -u x-ui -n 80 --no-pager"
  fi
  if ! wait_for_port "$port"; then
    warn "Panel port ${port} is not answering yet; check the journal if it stays down."
  fi
  printf '\n  Panel:  http://%s:%s\n  Config: %s\n\n' "$(public_ip)" "$port" "$ENV_FILE"
  info "Manage with: x-ui   (menu) or systemctl status x-ui"
  info "Rotate credential keys later with: x-ui encrypt-tokens"
}

case "$MODE" in
  uninstall) uninstall ;;
  install)   do_install ;;
esac
