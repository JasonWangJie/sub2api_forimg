#!/usr/bin/env bash
set -Eeuo pipefail

umask 022

APP_ROOT="/opt/sub2api"
DATA_DIR="${APP_ROOT}/data"
RELEASES_DIR="${APP_ROOT}/releases"
SERVICE_FILE="/etc/systemd/system/sub2api.service"
NGINX_SITE="/etc/nginx/sites-available/sub2api"
NGINX_ENABLED="/etc/nginx/sites-enabled/sub2api"
NGINX_MAP="/etc/nginx/conf.d/sub2api-websocket-map.conf"
DEFAULT_REPO="https://github.com/JasonWangJie/sub2api.git"
DEFAULT_BRANCH="main"
PNPM_VERSION="10.34.5"
PNPM_HOME="/usr/local/share/pnpm"
NODE_MAJOR="22"
HEALTH_TIMEOUT_SECONDS="600"

DOMAIN=""
REPO_URL="$DEFAULT_REPO"
BRANCH="$DEFAULT_BRANCH"
SOURCE_DIR=""
CONFIG_SOURCE=""
SKIP_NGINX=false
ENABLE_CERTBOT=false
CERTBOT_EMAIL=""
WORK_DIR=""
GO_STAGE_DIR=""

usage() {
  cat <<'EOF'
Sub2API Ubuntu native deployment

Usage:
  sudo bash deploy/ubuntu-native-deploy.sh --domain api.example.com [options]

Options:
  --domain DOMAIN         Domain used by the Nginx site (required unless --skip-nginx)
  --repo URL              Git repository to clone
                          (default: https://github.com/JasonWangJie/sub2api.git)
  --branch NAME           Branch or tag to deploy (default: main)
  --source-dir PATH       Build an existing source tree instead of cloning the repository
  --config PATH           Install an existing production config.yaml (mode 0600)
  --skip-nginx            Do not create or reload Nginx configuration
  --enable-certbot        Install Certbot and request an HTTPS certificate
  --certbot-email EMAIL   Certbot registration email (required with --enable-certbot)
  -h, --help              Show this help

Examples:
  # New installation: PostgreSQL and Redis already exist; finish setup over SSH.
  sudo bash deploy/ubuntu-native-deploy.sh \
    --domain api.example.com

  # Deploy an existing production instance without running the setup wizard.
  sudo bash deploy/ubuntu-native-deploy.sh \
    --domain api.example.com \
    --config /secure/path/config.yaml

  # Build the source tree already uploaded to this server.
  sudo bash deploy/ubuntu-native-deploy.sh \
    --domain api.example.com \
    --source-dir /home/ubuntu/sub2api

This script installs build tools, Node.js, pnpm and Go. It does not install,
initialize, stop, or reconfigure PostgreSQL or Redis.
EOF
}

log() {
  printf '[sub2api-deploy] %s\n' "$*"
}

warn() {
  printf '[sub2api-deploy] WARNING: %s\n' "$*" >&2
}

die() {
  printf '[sub2api-deploy] ERROR: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "$WORK_DIR" && "$WORK_DIR" == /tmp/sub2api-deploy.* && -d "$WORK_DIR" ]]; then
    rm -rf -- "$WORK_DIR"
  fi
  if [[ -n "$GO_STAGE_DIR" && "$GO_STAGE_DIR" == /opt/go/.sub2api-go-* && -d "$GO_STAGE_DIR" ]]; then
    rm -rf -- "$GO_STAGE_DIR"
  fi
}

on_error() {
  local exit_code=$?
  local line_number="$1"
  trap - ERR
  warn "Deployment failed near line ${line_number} (exit ${exit_code})."
  exit "$exit_code"
}

trap cleanup EXIT
trap 'on_error "$LINENO"' ERR

need_value() {
  local option="$1"
  local remaining="$2"
  [[ "$remaining" -ge 2 ]] || die "${option} requires a value."
}

parse_arguments() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --domain)
        need_value "$1" "$#"
        DOMAIN="$2"
        shift 2
        ;;
      --repo)
        need_value "$1" "$#"
        REPO_URL="$2"
        shift 2
        ;;
      --branch)
        need_value "$1" "$#"
        BRANCH="$2"
        shift 2
        ;;
      --source-dir)
        need_value "$1" "$#"
        SOURCE_DIR="$2"
        shift 2
        ;;
      --config)
        need_value "$1" "$#"
        CONFIG_SOURCE="$2"
        shift 2
        ;;
      --skip-nginx)
        SKIP_NGINX=true
        shift
        ;;
      --enable-certbot)
        ENABLE_CERTBOT=true
        shift
        ;;
      --certbot-email)
        need_value "$1" "$#"
        CERTBOT_EMAIL="$2"
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "Unknown option: $1. Run with --help for usage."
        ;;
    esac
  done
}

validate_arguments() {
  [[ $EUID -eq 0 ]] || die "Run this script as root, for example: sudo bash $0 ..."
  [[ -n "$BRANCH" && "$BRANCH" != -* ]] || die "Invalid --branch value."

  if [[ "$SKIP_NGINX" == false ]]; then
    [[ -n "$DOMAIN" ]] || die "--domain is required unless --skip-nginx is used."
    [[ "$DOMAIN" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ ]] || die "Invalid domain: ${DOMAIN}"
    [[ "$DOMAIN" != *..* ]] || die "Invalid domain: ${DOMAIN}"
  fi

  if [[ "$ENABLE_CERTBOT" == true ]]; then
    [[ "$SKIP_NGINX" == false ]] || die "--enable-certbot cannot be combined with --skip-nginx."
    [[ -n "$CERTBOT_EMAIL" && "$CERTBOT_EMAIL" == *@*.* ]] || \
      die "--certbot-email is required with --enable-certbot."
  fi

  if [[ -n "$SOURCE_DIR" && ! -d "$SOURCE_DIR" ]]; then
    die "Source directory does not exist: ${SOURCE_DIR}"
  fi
  if [[ -n "$CONFIG_SOURCE" && ! -f "$CONFIG_SOURCE" ]]; then
    die "Configuration file does not exist: ${CONFIG_SOURCE}"
  fi
}

check_platform() {
  [[ -r /etc/os-release ]] || die "Cannot identify this operating system. Ubuntu 22.04 or 24.04 is required."
  # shellcheck disable=SC1091
  source /etc/os-release
  [[ "${ID:-}" == "ubuntu" ]] || die "Unsupported operating system: ${ID:-unknown}. Ubuntu is required."
  case "${VERSION_ID:-}" in
    22.04|24.04) ;;
    *) die "Unsupported Ubuntu version: ${VERSION_ID:-unknown}. Use Ubuntu 22.04 or 24.04." ;;
  esac

  local architecture
  architecture="$(dpkg --print-architecture)"
  case "$architecture" in
    amd64|arm64) ;;
    *) die "Unsupported CPU architecture: ${architecture}. Use amd64 or arm64." ;;
  esac
}

install_prerequisites() {
  log "Installing source build prerequisites (PostgreSQL and Redis are not installed)."
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends git curl ca-certificates build-essential

  if [[ "$SKIP_NGINX" == false ]] && ! command -v nginx >/dev/null 2>&1; then
    die "Nginx is not installed. Install/configure the existing Nginx service, or rerun with --skip-nginx."
  fi
  if [[ "$SKIP_NGINX" == false ]]; then
    systemctl cat nginx >/dev/null 2>&1 || \
      die "Nginx is installed but nginx.service is unavailable. Use --skip-nginx or fix the service first."
    nginx -t
  fi
}

prepare_inputs() {
  WORK_DIR="$(mktemp -d /tmp/sub2api-deploy.XXXXXX)"

  if [[ -n "$CONFIG_SOURCE" ]]; then
    CONFIG_SOURCE="$(readlink -f -- "$CONFIG_SOURCE")"
    [[ -f "$CONFIG_SOURCE" ]] || die "Cannot resolve --config path."
  fi

  if [[ -n "$SOURCE_DIR" ]]; then
    SOURCE_DIR="$(readlink -f -- "$SOURCE_DIR")"
    log "Using the existing source tree: ${SOURCE_DIR}"
  else
    SOURCE_DIR="${WORK_DIR}/source"
    log "Cloning branch/tag '${BRANCH}' from the configured repository."
    git clone --depth 1 --single-branch --branch "$BRANCH" -- "$REPO_URL" "$SOURCE_DIR"
  fi

  [[ -f "$SOURCE_DIR/frontend/package.json" ]] || die "Missing frontend/package.json in ${SOURCE_DIR}."
  [[ -f "$SOURCE_DIR/frontend/pnpm-lock.yaml" ]] || die "Missing frontend/pnpm-lock.yaml in ${SOURCE_DIR}."
  [[ -f "$SOURCE_DIR/backend/go.mod" ]] || die "Missing backend/go.mod in ${SOURCE_DIR}."
  [[ -f "$SOURCE_DIR/backend/scripts/resolve-version.sh" ]] || die "Missing backend/scripts/resolve-version.sh."
}

install_go() {
  local go_version go_arch go_root go_archive go_checksum expected_checksum installed_version
  go_version="$(awk '$1 == "go" { print $2; exit }' "$SOURCE_DIR/backend/go.mod")"
  [[ "$go_version" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || die "Cannot read a valid Go version from backend/go.mod."

  go_arch="$(dpkg --print-architecture)"
  go_root="/opt/go/${go_version}"
  if [[ -x "$go_root/bin/go" ]]; then
    installed_version="$($go_root/bin/go version)"
    [[ "$installed_version" == *" go${go_version} "* ]] || \
      die "${go_root} exists but does not contain Go ${go_version}. Resolve it manually before retrying."
    log "Go ${go_version} is already installed."
  elif [[ -e "$go_root" ]]; then
    die "${go_root} exists but is incomplete. Resolve it manually before retrying."
  else
    log "Installing Go ${go_version} for ${go_arch}."
    go_archive="${WORK_DIR}/go${go_version}.linux-${go_arch}.tar.gz"
    go_checksum="${go_archive}.sha256"
    curl --fail --silent --show-error --location \
      "https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz" \
      --output "$go_archive"
    curl --fail --silent --show-error --location \
      "https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz.sha256" \
      --output "$go_checksum"
    expected_checksum="$(tr -d '[:space:]' < "$go_checksum")"
    [[ "$expected_checksum" =~ ^[a-fA-F0-9]{64}$ ]] || die "Go download returned an invalid SHA-256 checksum."
    printf '%s  %s\n' "$expected_checksum" "$go_archive" | sha256sum --check --status -

    install -d -o root -g root -m 0755 /opt/go
    GO_STAGE_DIR="/opt/go/.sub2api-go-${go_version}-$$"
    install -d -o root -g root -m 0755 "$GO_STAGE_DIR"
    tar -C "$GO_STAGE_DIR" --strip-components=1 -xzf "$go_archive"
    mv -- "$GO_STAGE_DIR" "$go_root"
    GO_STAGE_DIR=""
  fi

  ln -sfn "$go_root/bin/go" /usr/local/bin/go
  ln -sfn "$go_root/bin/gofmt" /usr/local/bin/gofmt
  export PATH="$go_root/bin:$PATH"
  export GOTOOLCHAIN=local
  go version
}

install_node_and_pnpm() {
  local installed_node_major="" installed_pnpm_version=""
  if command -v node >/dev/null 2>&1; then
    installed_node_major="$(node -p 'process.versions.node.split(".")[0]')"
  fi

  if [[ "$installed_node_major" != "$NODE_MAJOR" ]]; then
    log "Installing Node.js ${NODE_MAJOR}.x."
    curl --fail --silent --show-error --location \
      "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" \
      --output "${WORK_DIR}/nodesource-setup.sh"
    bash "${WORK_DIR}/nodesource-setup.sh"
    apt-get install -y --no-install-recommends nodejs
  else
    log "Node.js ${NODE_MAJOR}.x is already installed."
  fi

  if command -v pnpm >/dev/null 2>&1; then
    installed_pnpm_version="$(pnpm --version 2>/dev/null || true)"
  fi
  if [[ "$installed_pnpm_version" != "$PNPM_VERSION" || ! -x "$PNPM_HOME/pnpm" ]]; then
    log "Installing pnpm ${PNPM_VERSION} with the official pnpm installer."
    curl --fail --silent --show-error --location \
      "https://get.pnpm.io/install.sh" \
      --output "${WORK_DIR}/install-pnpm.sh"
    install -d -o root -g root -m 0755 "$PNPM_HOME"
    PNPM_VERSION="$PNPM_VERSION" PNPM_HOME="$PNPM_HOME" SHELL=/bin/bash \
      sh "${WORK_DIR}/install-pnpm.sh"
  else
    log "pnpm ${PNPM_VERSION} is already installed."
  fi

  [[ -x "$PNPM_HOME/pnpm" ]] || die "The pnpm installer did not create ${PNPM_HOME}/pnpm."
  ln -sfn "$PNPM_HOME/pnpm" /usr/local/bin/pnpm
  export PATH="$PNPM_HOME:$PATH"
  [[ "$(node -p 'process.versions.node.split(".")[0]')" == "$NODE_MAJOR" ]] || die "Node.js ${NODE_MAJOR}.x installation failed."
  [[ "$(pnpm --version)" == "$PNPM_VERSION" ]] || die "pnpm ${PNPM_VERSION} installation failed."
  node --version
  pnpm --version
}

build_application() {
  local version
  log "Building the embedded frontend."
  (
    cd "$SOURCE_DIR/frontend"
    CI=true pnpm install --frozen-lockfile --ignore-scripts=false
    pnpm run build
  )

  version="$(cd "$SOURCE_DIR/backend" && sh ./scripts/resolve-version.sh)"
  [[ "$version" =~ ^[0-9A-Za-z._+-]+$ ]] || die "Resolved application version is invalid: ${version}"

  log "Building the Go server (version ${version})."
  (
    cd "$SOURCE_DIR/backend"
    go build -trimpath -tags embed -ldflags "-X main.Version=${version}" -o sub2api ./cmd/server
  )
  [[ -x "$SOURCE_DIR/backend/sub2api" ]] || die "Go build did not produce backend/sub2api."
}

render_systemd_unit() {
  cat > "${WORK_DIR}/sub2api.service" <<'EOF'
[Unit]
Description=Sub2API API Gateway
Documentation=https://github.com/JasonWangJie/sub2api
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=sub2api
Group=sub2api
WorkingDirectory=/opt/sub2api/current
ExecStart=/opt/sub2api/current/sub2api

Environment=GIN_MODE=release
Environment=SERVER_HOST=127.0.0.1
Environment=SERVER_PORT=8080
Environment=DATA_DIR=/opt/sub2api/data
Environment="SERVER_TRUSTED_PROXIES=127.0.0.1/32,::1/128"

Restart=always
RestartSec=5
KillSignal=SIGTERM
TimeoutStopSec=20
LimitNOFILE=1048576
UMask=0077

StandardOutput=journal
StandardError=journal
SyslogIdentifier=sub2api

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/sub2api/data

[Install]
WantedBy=multi-user.target
EOF
}

install_release() {
  local release_id release_dir previous_release="" config_target config_target_resolved=""
  release_id="$(date +%Y%m%d-%H%M%S)"
  release_dir="${RELEASES_DIR}/${release_id}"
  if [[ -e "$release_dir" ]]; then
    release_id="${release_id}-$$"
    release_dir="${RELEASES_DIR}/${release_id}"
  fi

  if ! id sub2api >/dev/null 2>&1; then
    useradd --system --home-dir "$APP_ROOT" --shell /usr/sbin/nologin sub2api
  fi
  install -d -o root -g root -m 0755 "$APP_ROOT" "$RELEASES_DIR"
  install -d -o sub2api -g sub2api -m 0750 "$DATA_DIR"
  install -d -o root -g root -m 0755 "$release_dir"
  install -o root -g root -m 0755 "$SOURCE_DIR/backend/sub2api" "$release_dir/sub2api"

  if [[ -L "$APP_ROOT/current" ]]; then
    previous_release="$(readlink -f "$APP_ROOT/current" || true)"
  elif [[ -e "$APP_ROOT/current" ]]; then
    die "${APP_ROOT}/current exists but is not a symbolic link. Resolve it manually before retrying."
  fi

  if [[ -e "$DATA_DIR/.installed" && ! -f "$DATA_DIR/config.yaml" && -z "$CONFIG_SOURCE" ]]; then
    die "${DATA_DIR}/.installed exists but config.yaml is missing. Restore the original config before deployment."
  fi
  if [[ -f "$APP_ROOT/config.yaml" && ! -f "$DATA_DIR/config.yaml" && -z "$CONFIG_SOURCE" ]]; then
    die "A legacy ${APP_ROOT}/config.yaml was found. Rerun with --config ${APP_ROOT}/config.yaml to migrate it safely."
  fi

  if systemctl is-active --quiet sub2api 2>/dev/null; then
    log "Stopping the existing Sub2API service for the release switch."
    systemctl stop sub2api
  fi

  if [[ -n "$CONFIG_SOURCE" ]]; then
    config_target="$DATA_DIR/config.yaml"
    if [[ -f "$config_target" ]]; then
      config_target_resolved="$(readlink -f -- "$config_target")"
    fi
    if [[ "$CONFIG_SOURCE" == "$config_target_resolved" ]]; then
      chown sub2api:sub2api "$config_target"
      chmod 0600 "$config_target"
      log "The supplied config is already installed; ownership and mode were verified."
    else
      if [[ -f "$config_target" ]] && ! cmp --silent "$CONFIG_SOURCE" "$config_target"; then
        install -o sub2api -g sub2api -m 0600 \
          "$config_target" "$config_target.backup-${release_id}"
        log "Backed up the previous config.yaml inside the protected data directory."
      fi
      install -o sub2api -g sub2api -m 0600 \
        "$CONFIG_SOURCE" "$config_target"
      log "Installed the supplied production config.yaml with mode 0600."
    fi
  fi

  ln -sfn "$release_dir" "$APP_ROOT/current"
  render_systemd_unit
  install -o root -g root -m 0644 "${WORK_DIR}/sub2api.service" "$SERVICE_FILE"
  systemctl daemon-reload
  systemctl enable sub2api >/dev/null
  systemctl restart sub2api

  log "Activated release ${release_id}."
  if [[ -n "$previous_release" ]]; then
    log "Previous release: ${previous_release}"
  fi
}

wait_for_backend() {
  local deadline response_url
  response_url="http://127.0.0.1:8080/setup/status"
  deadline=$((SECONDS + HEALTH_TIMEOUT_SECONDS))
  log "Waiting for Sub2API to answer on 127.0.0.1:8080."

  while (( SECONDS < deadline )); do
    if curl --fail --silent --show-error --max-time 3 "$response_url" >/dev/null 2>&1; then
      log "Backend health check passed."
      return 0
    fi
    sleep 2
  done

  warn "Backend did not become ready within ${HEALTH_TIMEOUT_SECONDS} seconds."
  systemctl status sub2api --no-pager || true
  journalctl -u sub2api -n 100 --no-pager || true
  return 1
}

render_nginx_files() {
  cat > "${WORK_DIR}/sub2api-websocket-map.conf" <<'EOF'
map $http_upgrade $sub2api_connection_upgrade {
    default upgrade;
    ''      close;
}
EOF

  cat > "${WORK_DIR}/sub2api.nginx" <<EOF
# Managed initially by deploy/ubuntu-native-deploy.sh.
# Existing files are preserved on later runs so Certbot and local edits are not lost.
server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};

    underscores_in_headers on;
    client_max_body_size 256m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header X-Forwarded-Port \$server_port;

        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$sub2api_connection_upgrade;

        proxy_buffering off;
        proxy_request_buffering off;
        proxy_cache off;

        proxy_connect_timeout 10s;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_socket_keepalive on;
    }
}
EOF
}

configure_nginx() {
  local map_existed=false map_backup="${WORK_DIR}/nginx-map.backup"
  local site_created=false enabled_created=false enabled_target=""

  [[ "$SKIP_NGINX" == false ]] || return 0
  render_nginx_files
  install -d -o root -g root -m 0755 /etc/nginx/conf.d /etc/nginx/sites-available /etc/nginx/sites-enabled

  if [[ ! -f "$NGINX_SITE" ]] && nginx -T 2>/dev/null | awk -v domain="$DOMAIN" '
    $1 == "server_name" {
      for (i = 2; i <= NF; i++) {
        candidate = $i
        sub(/;$/, "", candidate)
        if (candidate == domain) {
          found = 1
        }
      }
    }
    END { exit(found ? 0 : 1) }
  '; then
    die "Nginx already has another enabled site for ${DOMAIN}. Merge the Sub2API proxy settings manually."
  fi
  if [[ -f "$NGINX_SITE" ]]; then
    grep -Fq "server_name ${DOMAIN};" "$NGINX_SITE" || \
      die "${NGINX_SITE} already exists for another domain. Merge or rename it before retrying."
    grep -Fq "proxy_pass http://127.0.0.1:8080;" "$NGINX_SITE" || \
      die "${NGINX_SITE} does not proxy to 127.0.0.1:8080. Merge the expected proxy settings before retrying."
    grep -Fq "proxy_buffering off;" "$NGINX_SITE" || \
      die "${NGINX_SITE} must contain 'proxy_buffering off;' for streaming responses."
    grep -Fq "proxy_request_buffering off;" "$NGINX_SITE" || \
      die "${NGINX_SITE} must contain 'proxy_request_buffering off;'."
  fi
  if [[ -e "$NGINX_ENABLED" || -L "$NGINX_ENABLED" ]]; then
    enabled_target="$(readlink -f "$NGINX_ENABLED" 2>/dev/null || true)"
    [[ "$enabled_target" == "$NGINX_SITE" ]] || \
      die "${NGINX_ENABLED} exists but does not point to ${NGINX_SITE}."
  fi

  if [[ -f "$NGINX_MAP" ]]; then
    cp --preserve=mode,ownership,timestamps "$NGINX_MAP" "$map_backup"
    map_existed=true
  fi
  install -o root -g root -m 0644 "${WORK_DIR}/sub2api-websocket-map.conf" "$NGINX_MAP"

  if [[ -f "$NGINX_SITE" ]]; then
    log "Preserving the existing Nginx site, including any Certbot or local changes."
  else
    install -o root -g root -m 0644 "${WORK_DIR}/sub2api.nginx" "$NGINX_SITE"
    site_created=true
  fi

  if [[ -e "$NGINX_ENABLED" || -L "$NGINX_ENABLED" ]]; then
    :
  else
    ln -s "$NGINX_SITE" "$NGINX_ENABLED"
    enabled_created=true
  fi

  if ! nginx -t; then
    warn "Nginx validation failed; restoring the files changed by this run."
    if [[ "$enabled_created" == true ]]; then
      rm -f -- "$NGINX_ENABLED"
    fi
    if [[ "$site_created" == true ]]; then
      rm -f -- "$NGINX_SITE"
    fi
    if [[ "$map_existed" == true ]]; then
      cp --preserve=mode,ownership,timestamps "$map_backup" "$NGINX_MAP"
    else
      rm -f -- "$NGINX_MAP"
    fi
    nginx -t || true
    die "Nginx configuration was not activated."
  fi

  systemctl enable nginx >/dev/null
  if systemctl is-active --quiet nginx; then
    systemctl reload nginx
  else
    systemctl start nginx
  fi
  log "Nginx is proxying ${DOMAIN} to 127.0.0.1:8080 without response buffering."
}

configure_certbot() {
  [[ "$ENABLE_CERTBOT" == true ]] || return 0
  log "Installing Certbot and requesting an HTTPS certificate for ${DOMAIN}."
  apt-get install -y --no-install-recommends certbot python3-certbot-nginx
  certbot --nginx \
    --non-interactive \
    --agree-tos \
    --redirect \
    --email "$CERTBOT_EMAIL" \
    --domains "$DOMAIN"
  nginx -t
  systemctl reload nginx
}

print_result() {
  local current_release
  current_release="$(readlink -f "$APP_ROOT/current")"
  printf '\n'
  log "Deployment completed."
  log "Release: ${current_release}"
  log "Service logs: journalctl -u sub2api -f"

  if [[ -f "$DATA_DIR/config.yaml" || -e "$DATA_DIR/.installed" ]]; then
    log "Existing configuration detected; normal startup and automatic migrations were used."
    if [[ "$SKIP_NGINX" == false ]]; then
      log "Application URL: http://${DOMAIN}"
    fi
  else
    cat <<'EOF'

[sub2api-deploy] A fresh installation was detected. PostgreSQL and Redis must
[sub2api-deploy] already be reachable, and the PostgreSQL database must exist.
[sub2api-deploy] Open an SSH tunnel from your computer:

  ssh -L 8080:127.0.0.1:8080 USER@SERVER_IP

[sub2api-deploy] Then open http://127.0.0.1:8080 and complete the setup wizard.
[sub2api-deploy] Do not expose port 8080 publicly.
EOF
  fi

  if [[ "$SKIP_NGINX" == false && "$ENABLE_CERTBOT" == false ]]; then
    log "HTTPS was not requested. Rerun with --enable-certbot --certbot-email EMAIL after DNS is ready."
  fi
}

main() {
  parse_arguments "$@"
  validate_arguments
  check_platform
  install_prerequisites
  prepare_inputs
  install_go
  install_node_and_pnpm
  build_application
  install_release
  wait_for_backend
  configure_nginx
  configure_certbot
  print_result
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
