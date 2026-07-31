#!/usr/bin/env bash
set -euo pipefail

# Installs the radar binary by downloading the appropriate release artifact
# from GitHub.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ankit-lilly/work-dashboard/main/scripts/install.sh | bash
#
# Options (environment variables):
#   VERSION      - release tag to install (default: latest)
#   INSTALL_DIR  - target directory (default: /usr/local/bin)
#   REPO         - GitHub repo (default: ankit-lilly/work-dashboard)

REPO="${REPO:-ankit-lilly/work-dashboard}"
BINARY_NAME="${BINARY_NAME:-radar}"
export BINARY_NAME
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-latest}"

log() {
  printf '==> %s\n' "$*"
}

err() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "required command '$1' not found"
  fi
}

detect_platform() {
  case "$(uname -s)" in
    Darwin) TARGET_OS=darwin ;;
    Linux) TARGET_OS=linux ;;
    MINGW* | MSYS* | CYGWIN*) TARGET_OS=windows ;;
    *)
      err "unsupported OS: $(uname -s)"
      ;;
  esac

  case "$(uname -m)" in
    x86_64 | amd64) TARGET_ARCH=amd64 ;;
    arm64 | aarch64) TARGET_ARCH=arm64 ;;
    *)
      err "unsupported architecture: $(uname -m)"
      ;;
  esac
  export TARGET_OS TARGET_ARCH
}

resolve_release_tag() {
  if [[ "${VERSION}" == "latest" ]]; then
    RELEASE_TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | sed -n 's/^[[:space:]]*"tag_name": *"\(.*\)",\{0,1\}$/\1/p' \
      | head -n1) || true

    if [[ -z "${RELEASE_TAG}" ]]; then
      log "GitHub API lookup failed; falling back to releases/latest redirect"
      RELEASE_TAG=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest") \
        || err "failed to resolve latest release tag for ${REPO}"
      RELEASE_TAG="${RELEASE_TAG##*/}"
      if [[ "${RELEASE_TAG}" == "releases" || "${RELEASE_TAG}" == "latest" ]]; then
        err "failed to resolve latest release tag for ${REPO} (redirect did not reach a tag)"
      fi
    fi
  else
    RELEASE_TAG="${VERSION}"
  fi

  if [[ -z "${RELEASE_TAG}" ]]; then
    err "could not determine release tag"
  fi

  export RELEASE_TAG
}

fetch_checksums() {
  local downloads_base candidate repo_name release_version

  downloads_base="https://github.com/${REPO}/releases/download/${RELEASE_TAG}"
  repo_name="${REPO##*/}"
  release_version="${RELEASE_TAG#v}"

  for candidate in \
    "checksums.txt" \
    "${repo_name}_${release_version}_checksums.txt"
  do
    checksums_url="${downloads_base}/${candidate}"
    log "Trying checksum manifest at ${checksums_url}"
    if checksum_content=$(curl -fsSL "${checksums_url}" 2>/dev/null); then
      if [[ -n "${checksum_content}" ]]; then
        log "Using checksum manifest ${candidate}"
        export checksums_url checksum_content
        return 0
      fi
    fi
  done

  err "failed to locate a checksum manifest for release ${RELEASE_TAG}"
}

select_asset_from_checksums() {
  local downloads_base checksum_pattern

  downloads_base="https://github.com/${REPO}/releases/download/${RELEASE_TAG}"
  fetch_checksums

  case "${TARGET_OS}" in
    darwin|linux)
      checksum_pattern="${TARGET_OS}_${TARGET_ARCH}\\.tar\\.gz"
      ;;
    windows)
      checksum_pattern="${TARGET_OS}_${TARGET_ARCH}\\.zip"
      ;;
    *)
      checksum_pattern="${TARGET_OS}_${TARGET_ARCH}"
      ;;
  esac

  ASSET_NAME=$(printf '%s\n' "${checksum_content}" | awk "/${checksum_pattern}/ {print \$2; exit}")
  if [[ -z "${ASSET_NAME}" ]]; then
    err "checksums.txt did not contain an asset matching ${TARGET_OS}/${TARGET_ARCH}"
  fi

  ASSET_URL="${downloads_base}/${ASSET_NAME}"
}

download_asset() {
  TMP_DIR=$(mktemp -d)
  trap 'rm -rf "$TMP_DIR"' EXIT

  DOWNLOAD_PATH="${TMP_DIR}/${ASSET_NAME:-download}"
  log "Downloading ${ASSET_URL}..."
  curl -fL --retry 3 --retry-delay 2 -o "${DOWNLOAD_PATH}" "${ASSET_URL}" || err "failed to download ${ASSET_URL}"
}

extract_binary() {
  case "${DOWNLOAD_PATH}" in
    *.tar.gz | *.tgz)
      require_cmd tar
      tar -xzf "${DOWNLOAD_PATH}" -C "${TMP_DIR}"
      ;;
    *.zip)
      require_cmd unzip
      unzip -qo "${DOWNLOAD_PATH}" -d "${TMP_DIR}"
      ;;
  esac

  # Find the binary
  local bin_ext=""
  if [[ "${TARGET_OS}" == "windows" ]]; then
    bin_ext=".exe"
  fi

  BINARY_PATH=$(find "${TMP_DIR}" -maxdepth 3 -type f \
    -name "${BINARY_NAME}${bin_ext}" \
    ! -name "*.tar" ! -name "*.gz" ! -name "*.zip" ! -name "*.tgz" \
    -print -quit)

  if [[ -z "${BINARY_PATH}" ]]; then
    err "could not locate ${BINARY_NAME} binary inside downloaded artifact"
  fi

  chmod +x "${BINARY_PATH}"
}

maybe_clear_quarantine() {
  if [[ "${TARGET_OS}" != "darwin" ]]; then
    return
  fi

  if command -v xattr >/dev/null 2>&1; then
    if xattr -p com.apple.quarantine "${BINARY_PATH}" >/dev/null 2>&1; then
      log "Removing macOS quarantine attribute from $(basename "${BINARY_PATH}")"
      xattr -d com.apple.quarantine "${BINARY_PATH}" || log "warning: failed to clear com.apple.quarantine; continuing"
    fi
  else
    log "warning: xattr command not available; skipping quarantine removal"
  fi
}

install_binary() {
  log "Installing ${BINARY_NAME} to ${INSTALL_DIR}"
  mkdir -p "${INSTALL_DIR}"

  local bin_name target
  bin_name=$(basename "${BINARY_PATH}")
  target="${INSTALL_DIR}/${bin_name}"

  if [[ -w "${INSTALL_DIR}" ]]; then
    install -m 0755 "${BINARY_PATH}" "${target}"
  else
    if command -v sudo >/dev/null 2>&1; then
      sudo install -m 0755 "${BINARY_PATH}" "${target}"
    else
      err "${INSTALL_DIR} is not writable; re-run with sudo or set INSTALL_DIR to a user-writable path"
    fi
  fi

  log "Installed ${bin_name} -> ${target}"
}

post_install() {
  log ""
  log "RADAR dashboard installed successfully!"
  log ""
  log "Quick start:"
  log "  1. Run 'radar setup' to configure your AWS profiles"
  log "  2. Run 'radar server' to start the dashboard"
  log ""
  log "Ensure ${INSTALL_DIR} is on your PATH."
}

main() {
  require_cmd curl
  require_cmd install
  detect_platform
  resolve_release_tag
  select_asset_from_checksums
  download_asset
  extract_binary
  maybe_clear_quarantine
  install_binary
  post_install
}

main "$@"
