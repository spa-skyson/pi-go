#!/bin/bash
# exit on error
set -e

# pi-rate installer (fork: spa-skyson/pi-rate).
#
# Two install modes:
#   1. release — download the latest published GitHub release asset and
#      install `pirate` (used whenever the repo has a published release).
#   2. source  — fallback when no release is published yet or the asset
#      download fails: shallow-clone the repo, run `make build`, and install
#      both `pirate` and `pirate-sandbox`. Requires git and Go (>= the go.mod
#      minimum). Set DRY_RUN=1 to print the chosen path without installing.

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Configuration (REPO/BRANCH overridable via environment — useful for forks,
# mirrors and testing, e.g. REPO=owner/repo ./install.sh)
REPO="${REPO:-spa-skyson/pi-rate}"
BRANCH="${BRANCH:-main}"
BINARY_NAME="pirate"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Helper functions
log_info() {
    echo -e "${BLUE}ℹ️  ${NC}$1" >&2
}

log_success() {
    echo -e "${GREEN}✅ ${NC}$1" >&2
}

log_step() {
    echo -e "${CYAN}🔄 ${NC}$1" >&2
}

log_step_complete() {
    echo -e "\033[1A\033[2K${GREEN}✅ ${NC}$1" >&2
}

log_warn() {
    echo -e "${YELLOW}⚠️  ${NC}$1" >&2
}

log_error() {
    echo -e "${RED}❌ ${NC}$1" >&2
}

log_header() {
    echo -e "\n${BOLD}${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}" >&2
    echo -e "${BOLD}${CYAN}  🚀 pi-rate Installer${NC}" >&2
    echo -e "${BOLD}${CYAN}  repo: ${REPO}${NC}" >&2
    echo -e "${BOLD}${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n" >&2
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Detect OS and architecture
detect_platform() {
    local os
    local arch

    # Detect OS - use uname -s directly (returns Darwin/Linux/etc)
    case "$(uname -s)" in
        Darwin)
            os="darwin"
            ;;
        Linux)
            os="linux"
            ;;
        CYGWIN*|MINGW32*|MSYS*|MINGW*)
            os="windows"
            ;;
        *)
            log_error "Unsupported operating system: $(uname -s)"
            exit 1
            ;;
    esac

    # Detect architecture
    case "$(uname -m)" in
        x86_64|amd64)
            arch="amd64"
            ;;
        arm64|aarch64)
            arch="arm64"
            ;;
        *)
            log_error "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac

    echo "${os}-${arch}"
}

# Get latest release version from GitHub
get_latest_version() {
    log_step "Fetching latest version from GitHub..."

    local response
    if command_exists curl; then
        response=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest")
    elif command_exists wget; then
        response=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest")
    else
        log_error "Neither curl nor wget is available. Please install one of them."
        exit 1
    fi

    local version=$(echo "$response" | grep '"tag_name"' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')

    if [ -z "$version" ]; then
        # No published release (404 / empty tag) or API hiccup — the caller
        # falls back to a source build instead of failing hard.
        log_warn "No published release found for ${BOLD}${REPO}${NC}"
        return 0
    fi

    echo "$version"
}

# URL of the release archive for a version/platform pair (GoReleaser naming:
# tag v0.0.13 -> asset pi-rate_0.0.13_darwin_arm64.tar.gz).
asset_url() {
    local version="$1"
    local platform="$2"

    # GoReleaser strips 'v' from version in asset names
    local version_nov="${version#v}"
    local ext="tar.gz"
    if [[ "$platform" == windows-* ]]; then
        ext="zip"
    fi
    # Assets use underscore: pi-rate_0.0.13_darwin_arm64.tar.gz
    echo "https://github.com/${REPO}/releases/download/${version}/pi-rate_${version_nov}_${platform//-/_}.${ext}"
}

# Download binary. Returns 1 on failure so the caller can fall back to a
# source build instead of exiting hard.
download_binary() {
    local version="$1"
    local platform="$2"

    local download_url
    download_url=$(asset_url "$version" "$platform")
    local temp_file="/tmp/pirate-install.$$.archive"

    log_step "Downloading pi-rate ${BOLD}${version}${NC} for ${BOLD}${platform}${NC}..."

    if command_exists curl; then
        curl -fsSL -o "$temp_file" "$download_url" || { rm -f "$temp_file"; return 1; }
    elif command_exists wget; then
        wget -q -O "$temp_file" "$download_url" || { rm -f "$temp_file"; return 1; }
    fi

    if [ ! -s "$temp_file" ]; then
        echo "" >&2
        log_error "Failed to download binary from $download_url"
        return 1
    fi

    echo "$temp_file"
}

# Install binary
install_binary() {
    local temp_file="$1"
    local install_path="${INSTALL_DIR}/${BINARY_NAME}"

    log_step "Installing to ${BOLD}${install_path}${NC}..."

    # Create install directory if needed
    mkdir -p "$INSTALL_DIR"

    # Extract archive
    if [[ "$temp_file" == *.zip ]]; then
        unzip -o "$temp_file" -d "/tmp/pirate-install.$$"
        mv "/tmp/pirate-install.$$/pirate.exe" "$install_path" 2>/dev/null || mv "/tmp/pirate-install.$$/pirate" "$install_path"
        rm -rf "/tmp/pirate-install.$$"
    else
        tar -xzf "$temp_file" -C /tmp
        local extracted_file=$(tar -tzf "$temp_file" | grep -E '^pirate(-exe)?$' | head -1)
        if [ -z "$extracted_file" ]; then
            extracted_file="pirate"
        fi
        mv "/tmp/${extracted_file}" "$install_path"
    fi

    chmod +x "$install_path"
    rm -f "$temp_file"

    echo "$install_path"
}

# Verify installation
verify_installation() {
    local install_path="$1"

    log_step "Verifying installation..."

    if [[ ":$PATH:" == *":${INSTALL_DIR}:"* ]]; then
        log_step_complete "Installation verified! ${BOLD}pirate${NC} is in PATH"
        echo -e "${BLUE}ℹ️  ${NC}Run ${BOLD}pirate${NC} to start" >&2
    else
        echo "" >&2
        log_warn "${BINARY_NAME} installed but ${BOLD}${INSTALL_DIR}${NC} is not in PATH"
        log_info "Add this to your shell profile (~/.bashrc, ~/.zshrc):"
        echo -e "${CYAN}   export PATH=\"${INSTALL_DIR}:\$PATH\"${NC}" >&2
    fi
}

# Ensure the installed Go toolchain satisfies a "major.minor[.patch]"
# requirement (the minimum comes from the clone's go.mod). Compares
# major.minor only. Exits with an install hint when the toolchain is older.
check_go_version() {
    local required="$1"
    local have
    have=$(go version 2>/dev/null | awk '{print $3}')
    have="${have#go}"

    if ! [[ "$have" =~ ^[0-9]+\. ]]; then
        log_warn "Could not parse go version ('${have:-none}') — assuming it satisfies ${required}"
        return 0
    fi

    local have_major have_minor want_major want_minor
    have_major="${have%%.*}"
    have_minor="${have#*.}"
    have_minor="${have_minor%%.*}"
    want_major="${required%%.*}"
    want_minor="${required#*.}"
    want_minor="${want_minor%%.*}"

    if [ "$have_major" -lt "$want_major" ] ||
        { [ "$have_major" -eq "$want_major" ] && [ "$have_minor" -lt "$want_minor" ]; }; then
        log_error "Go ${want_major}.${want_minor}+ is required (found: ${have})."
        log_info "Install a current toolchain from https://go.dev/dl/ and re-run this script."
        exit 1
    fi
    log_step_complete "Go ${BOLD}${have}${NC} satisfies the required ${want_major}.${want_minor}"
}

# Build and install from source — fallback when no release exists or the
# release asset cannot be downloaded. Installs BOTH `pirate` and `pirate-sandbox`.
build_and_install_from_source() {
    if [ -n "${DRY_RUN:-}" ]; then
        log_info "DRY_RUN: source path selected (nothing will be cloned, built or installed):"
        log_info "DRY_RUN:   git clone --depth 1 --branch ${BRANCH} https://github.com/${REPO}.git /tmp/pirate-src.<pid>"
        log_info "DRY_RUN:   go version check: >= minimum from go.mod (otherwise error + https://go.dev/dl/)"
        log_info "DRY_RUN:   make build  (in the clone)"
        log_info "DRY_RUN:   install pirate and pirate-sandbox into ${INSTALL_DIR}"
        return 0
    fi

    if ! command_exists git; then
        log_error "git is required for the source build. Install git and re-run."
        exit 1
    fi

    local src_dir="/tmp/pirate-src.$$"
    # shellcheck disable=SC2064  — $$ is stable in the parent shell; expands to
    # the same PID at trap-fire time and sidesteps local-var scoping in traps.
    trap 'rm -rf /tmp/pirate-src.$$' EXIT

    log_step "Cloning ${BOLD}${REPO}${NC} (branch ${BOLD}${BRANCH}${NC}, shallow)..."
    if ! git clone --depth 1 --branch "$BRANCH" "https://github.com/${REPO}.git" "$src_dir"; then
        log_error "Failed to clone https://github.com/${REPO}.git (branch: ${BRANCH})"
        exit 1
    fi

    if ! command_exists go; then
        log_error "Go is required to build from source but was not found in PATH."
        log_info "Install Go from https://go.dev/dl/ and re-run this script."
        exit 1
    fi
    local required
    required=$(awk '$1 == "go" {print $2; exit}' "$src_dir/go.mod")
    required="${required:-1.27}"
    check_go_version "$required"

    log_step "Building from source (make build)..."
    if ! (cd "$src_dir" && make build); then
        log_error "make build failed in ${src_dir}"
        exit 1
    fi

    log_step "Installing binaries to ${BOLD}${INSTALL_DIR}${NC}..."
    mkdir -p "$INSTALL_DIR"
    install -m 0755 "$src_dir/pirate" "${INSTALL_DIR}/pirate"
    install -m 0755 "$src_dir/pirate-sandbox" "${INSTALL_DIR}/pirate-sandbox"

    log_success "Installed ${INSTALL_DIR}/pirate and ${INSTALL_DIR}/pirate-sandbox (source build, branch ${BRANCH})"
    verify_installation "${INSTALL_DIR}/pirate"
}

# Release path: download the published archive and install `pirate`.
# Returns 1 on any failure so the caller can fall back to a source build.
install_from_release() {
    local version="$1"
    local platform="$2"

    if [ -n "${DRY_RUN:-}" ]; then
        log_info "DRY_RUN: release path selected (nothing will be downloaded or installed):"
        log_info "DRY_RUN:   curl/wget $(asset_url "$version" "$platform")"
        log_info "DRY_RUN:   extract and install to ${INSTALL_DIR}/pirate"
        return 0
    fi

    local temp_file
    temp_file=$(download_binary "$version" "$platform") || return 1
    log_step_complete "Download complete"

    local install_path
    install_path=$(install_binary "$temp_file") || return 1
    log_step_complete "Binary installed"

    verify_installation "$install_path"
}

finish() {
    echo -e "\n${BOLD}${GREEN}🎉 Installation complete!${NC}" >&2
    echo -e "${GREEN}   Run ${BOLD}pirate${NC}${GREEN} to start.${NC}\n" >&2
}

# Main installation function
main() {
    log_header

    # Check prerequisites
    log_step "Checking prerequisites..."
    if ! command_exists curl && ! command_exists wget; then
        echo "" >&2
        log_error "This script requires either curl or wget."
        exit 1
    fi
    log_step_complete "Prerequisites check passed"

    # Detect platform
    log_step "Detecting platform..."
    local platform=$(detect_platform)
    log_step_complete "Platform detected: ${BOLD}${platform}${NC}"

    # Get latest version — empty when the repo has no published release
    local version
    version=$(get_latest_version)

    if [ -n "$version" ]; then
        log_step_complete "Found latest version: ${BOLD}${version}${NC}"
        if install_from_release "$version" "$platform"; then
            finish
            return 0
        fi
        log_warn "Release install failed — falling back to source build"
    else
        log_info "no published release yet — building from source"
    fi

    build_and_install_from_source
    finish
}

# Handle command line arguments
case "${1:-}" in
    -h|--help)
        echo -e "${BOLD}${CYAN}🚀 pi-rate Installer${NC}"
        echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo -e "\n${BOLD}USAGE:${NC}"
        echo -e "  $0 [OPTIONS]"
        echo -e "\n${BOLD}OPTIONS:${NC}"
        echo -e "  -h, --help     Show this help message"
        echo -e "\n${BOLD}MODES:${NC}"
        echo -e "  release   Install 'pirate' from the latest GitHub release (default when one exists)"
        echo -e "  source    Fallback: shallow-clone the repo, make build, install 'pirate' + 'pirate-sandbox'"
        echo -e "\n${BOLD}ENVIRONMENT VARIABLES:${NC}"
        echo -e "  INSTALL_DIR    Installation directory (default: \$HOME/.local/bin)"
        echo -e "  REPO           GitHub repository to install from (default: ${REPO})"
        echo -e "  BRANCH         Branch used by the source-build fallback (default: main)"
        echo -e "  DRY_RUN        Non-empty: print the chosen path and commands, install nothing"
        echo ""
        exit 0
        ;;
    *)
        main "$@"
        ;;
esac
