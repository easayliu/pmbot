#!/usr/bin/env bash
set -euo pipefail

APP_NAME="pmbot"
REPO="easayliu/pmbot"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/${APP_NAME}"
DATA_DIR="/var/lib/${APP_NAME}"
SERVICE_USER="${APP_NAME}"
SERVICE_GROUP="${APP_NAME}"

# Colors for output.
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# Must run as root.
[[ $EUID -eq 0 ]] || error "please run as root: sudo bash install.sh"

# --- Resolve binary: local build or GitHub release ---

detect_arch() {
    local arch
    arch="$(uname -m)"
    case "${arch}" in
        x86_64)  echo "amd64" ;;
        aarch64) echo "arm64" ;;
        arm64)   echo "arm64" ;;
        *)       error "unsupported architecture: ${arch}" ;;
    esac
}

detect_os() {
    local os
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "${os}" in
        linux)  echo "linux" ;;
        darwin) echo "darwin" ;;
        *)      error "unsupported OS: ${os}" ;;
    esac
}

download_release() {
    local version="${1:-latest}"
    local os arch asset_name url

    os="$(detect_os)"
    arch="$(detect_arch)"
    asset_name="${APP_NAME}-${os}-${arch}"

    if [[ "${version}" == "latest" ]]; then
        url="https://github.com/${REPO}/releases/latest/download/${asset_name}"
    else
        url="https://github.com/${REPO}/releases/download/${version}/${asset_name}"
    fi

    info "downloading ${asset_name} from ${url}"
    if command -v curl &>/dev/null; then
        curl -fsSL -o "/tmp/${APP_NAME}" "${url}"
    elif command -v wget &>/dev/null; then
        wget -qO "/tmp/${APP_NAME}" "${url}"
    else
        error "curl or wget is required"
    fi
    chmod +x "/tmp/${APP_NAME}"
    echo "/tmp/${APP_NAME}"
}

# Use local build if available, otherwise download from GitHub.
BINARY=""
if [[ -f "./build/${APP_NAME}" ]]; then
    BINARY="./build/${APP_NAME}"
    info "using local binary: ${BINARY}"
else
    BINARY="$(download_release "${VERSION:-latest}")"
fi

info "installing ${APP_NAME}..."

# Create system user.
if ! id -u "${SERVICE_USER}" &>/dev/null; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "${SERVICE_USER}"
    info "created system user: ${SERVICE_USER}"
else
    info "user ${SERVICE_USER} already exists"
fi

# Install binary.
install -m 0755 "${BINARY}" "${INSTALL_DIR}/${APP_NAME}"
INSTALLED_VERSION="$("${INSTALL_DIR}/${APP_NAME}" -version 2>/dev/null || echo "unknown")"
info "installed binary to ${INSTALL_DIR}/${APP_NAME} (${INSTALLED_VERSION})"

# Create config directory and install default config.
mkdir -p "${CONFIG_DIR}"
CONFIG_SRC=""
if [[ -f "./config.yaml.example" ]]; then
    CONFIG_SRC="./config.yaml.example"
elif [[ -f "./config.yaml.minimal" ]]; then
    CONFIG_SRC="./config.yaml.minimal"
fi
if [[ -n "${CONFIG_SRC}" && ! -f "${CONFIG_DIR}/config.yaml" ]]; then
    install -m 0640 "${CONFIG_SRC}" "${CONFIG_DIR}/config.yaml"
    info "installed default config to ${CONFIG_DIR}/config.yaml"
elif [[ -f "${CONFIG_DIR}/config.yaml" ]]; then
    warn "config already exists at ${CONFIG_DIR}/config.yaml, skipped"
fi
chown -R root:"${SERVICE_GROUP}" "${CONFIG_DIR}"

# Create data directory.
mkdir -p "${DATA_DIR}"
chown -R "${SERVICE_USER}":"${SERVICE_GROUP}" "${DATA_DIR}"
info "data directory: ${DATA_DIR}"

# Install environment file.
ENV_FILE="${CONFIG_DIR}/env"
if [[ ! -f "${ENV_FILE}" ]]; then
    cat > "${ENV_FILE}" <<'EOF'
# Polymarket private key (required).
POLYMARKET_PRIVATE_KEY=

# Polymarket wallet address (optional).
# POLYMARKET_WALLET_ADDRESS=
EOF
    chmod 0640 "${ENV_FILE}"
    chown root:"${SERVICE_GROUP}" "${ENV_FILE}"
    info "created env file: ${ENV_FILE} (edit to set your private key)"
else
    warn "env file already exists at ${ENV_FILE}, skipped"
fi

# Install systemd unit.
cat > /etc/systemd/system/${APP_NAME}.service <<EOF
[Unit]
Description=Polymarket Trading Bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_GROUP}
WorkingDirectory=${DATA_DIR}
EnvironmentFile=${CONFIG_DIR}/env
ExecStart=${INSTALL_DIR}/${APP_NAME} -config ${CONFIG_DIR}/config.yaml
Restart=on-failure
RestartSec=5

# Security hardening.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR}
ReadOnlyPaths=${CONFIG_DIR}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
info "installed systemd unit"

# Reload and enable.
systemctl daemon-reload
systemctl enable "${APP_NAME}.service"
info "service enabled"

echo ""
info "installation complete!"
echo ""
echo "  1. edit private key:  sudo vi ${CONFIG_DIR}/env"
echo "  2. edit config:       sudo vi ${CONFIG_DIR}/config.yaml"
echo "  3. start service:     sudo systemctl start ${APP_NAME}"
echo "  4. check status:      sudo systemctl status ${APP_NAME}"
echo "  5. view logs:         sudo journalctl -u ${APP_NAME} -f"
echo ""
