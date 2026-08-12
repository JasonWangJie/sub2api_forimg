#!/bin/bash
#
# Sub2API Upgrade Script
# Sub2API 升级脚本
# FORK_IDENTITY: JasonWangJie/sub2api — 合并 upstream 时禁止删除本文件或改回 Wei-Shaw
# Usage: curl -sSL https://raw.githubusercontent.com/JasonWangJie/sub2api/main/deploy/upgrade.sh | sudo bash
#
# Thin wrapper around install.sh upgrade: downloads the latest release binary,
# backs up the binary and config.yaml, replaces the program, and restarts systemd.
#

set -e

GITHUB_REPO="${SUB2API_GITHUB_REPO:-JasonWangJie/sub2api}"
BRANCH="${SUB2API_BRANCH:-main}"
INSTALL_SCRIPT_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/${BRANCH}/deploy/install.sh"

if [ -z "${BASH_VERSION:-}" ]; then
    echo "Error: This upgrader must be run with Bash 4.0 or later." >&2
    exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
    echo "Error: Please run as root (use sudo)." >&2
    exit 1
fi

# Prefer a local install.sh when this script is executed from a checked-out repo.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/install.sh" ]; then
    exec bash "$SCRIPT_DIR/install.sh" upgrade "$@"
fi

# curl | bash path: fetch install.sh and run upgrade
TMP_SCRIPT="$(mktemp)"
trap 'rm -f "$TMP_SCRIPT"' EXIT

if ! curl -fsSL "$INSTALL_SCRIPT_URL" -o "$TMP_SCRIPT"; then
    echo "Error: Failed to download install.sh from ${INSTALL_SCRIPT_URL}" >&2
    exit 1
fi

exec bash "$TMP_SCRIPT" upgrade "$@"
