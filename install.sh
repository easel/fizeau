#!/usr/bin/env bash
# Fizeau installer — downloads the latest release binary for your platform.
# Usage: curl -fsSL https://raw.githubusercontent.com/easel/fizeau/master/install.sh | bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
REPO="easel/fizeau"
BINARY_NAME="${BINARY_NAME:-fiz}"
INSTALL_DIR="${FIZEAU_INSTALL_DIR:-$HOME/.local/bin}"
SHELL_NAME=""
RC_FILE=""

# Logging functions (all to stderr to avoid polluting command substitution)
log() {
    echo -e "${BLUE}[${BINARY_NAME}]${NC} $1" >&2
}

success() {
    echo -e "${GREEN}[${BINARY_NAME}]${NC} $1" >&2
}

warn() {
    echo -e "${YELLOW}[${BINARY_NAME}]${NC} $1" >&2
}

error() {
    echo -e "${RED}[${BINARY_NAME}]${NC} $1" >&2
    exit 1
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    
    # Check for curl or wget
    if ! command -v curl &>/dev/null && ! command -v wget &>/dev/null; then
        error "curl or wget is required but neither is installed."
    fi
    
    success "Prerequisites check passed"
}

# Detect platform
detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) error "Unsupported architecture: $ARCH" ;;
    esac

    case "$OS" in
        linux|darwin) ;;
        *) error "Unsupported OS: $OS" ;;
    esac

    BINARY="${BINARY_NAME}-${OS}-${ARCH}"
}

# Get latest release tag
get_latest_release() {
    log "Fetching latest release..."
    
    if [ -n "${FIZEAU_VERSION:-}" ]; then
        TAG="${FIZEAU_VERSION}"
        # Normalize to tag format (add v prefix if missing)
        if [[ ! "$TAG" =~ ^v ]]; then
            TAG="v${TAG}"
        fi
        log "Using requested version: ${TAG}"
    else
        if command -v curl &>/dev/null; then
            TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
        elif command -v wget &>/dev/null; then
            TAG=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
        fi

        if [ -z "$TAG" ]; then
            error "Could not determine latest release. Set FIZEAU_VERSION to specify a version."
        fi
        
        log "Latest release: ${TAG}"
    fi
    
    echo "$TAG"
}

# Download and install binary
install_binary() {
    local TAG="$1"
    
    URL="https://github.com/${REPO}/releases/download/${TAG}/${BINARY}"
    
    log "Installing ${BINARY_NAME} ${TAG} (${OS}/${ARCH})..."
    
    # Create installation directory
    mkdir -p "$INSTALL_DIR"
    
    # Download binary
    if command -v curl &>/dev/null; then
        curl -fsSL "$URL" -o "${INSTALL_DIR}/${BINARY_NAME}"
    elif command -v wget &>/dev/null; then
        wget -q "$URL" -O "${INSTALL_DIR}/${BINARY_NAME}"
    fi
    
    # Make executable
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

    success "Installed ${BINARY_NAME} to ${INSTALL_DIR}/${BINARY_NAME}"
}

# Configure PATH in shell rc files
configure_path() {
    log "Checking PATH configuration..."
    
    # Check if already in PATH
    if [[ ":$PATH:" == *":${INSTALL_DIR}:"* ]]; then
        success "PATH is already configured (${INSTALL_DIR})"
        return
    fi
    
    # Detect shell and add to appropriate rc file
    SHELL_NAME=$(basename "$SHELL")
    RC_FILE=""
    
    case "$SHELL_NAME" in
        bash)
            RC_FILE="$HOME/.bashrc"
            ;;
        zsh)
            RC_FILE="$HOME/.zshrc"
            ;;
        fish)
            RC_FILE="$HOME/.config/fish/config.fish"
            ;;
        *)
            RC_FILE="$HOME/.profile"
            ;;
    esac
    
    if [ -f "$RC_FILE" ]; then
        # Check if already added
        if ! grep -q "${INSTALL_DIR}" "$RC_FILE" 2>/dev/null; then
            echo "" >> "$RC_FILE"
            echo "# Fizeau CLI PATH" >> "$RC_FILE"
            
            case "$SHELL_NAME" in
                fish)
                    echo "fish_add_path ${INSTALL_DIR}" >> "$RC_FILE"
                    ;;
                *)
                    printf 'export PATH="${PATH}:%s"\n' "${INSTALL_DIR}" >> "$RC_FILE"
                    ;;
            esac
            
            success "Added ${BINARY_NAME} to PATH in $RC_FILE"
        else
            success "Fizeau is already configured in $RC_FILE"
        fi
    else
        warn "Could not find shell config file. Please add ${INSTALL_DIR} to your PATH manually."
        echo ""
        echo "Add this to your shell rc file:"
        case "$SHELL_NAME" in
            fish)
                echo "  fish_add_path ${INSTALL_DIR}"
                ;;
            *)
                echo "  export PATH=\"\${PATH}:${INSTALL_DIR}\""
                ;;
        esac
    fi
}

# Verify installation
verify_installation() {
    log "Verifying installation..."

    # Check if binary exists and is executable
    if [ ! -f "${INSTALL_DIR}/${BINARY_NAME}" ] || [ ! -x "${INSTALL_DIR}/${BINARY_NAME}" ]; then
        error "Installation failed: ${BINARY_NAME} binary not found or not executable at ${INSTALL_DIR}/${BINARY_NAME}"
    fi

    # Test binary execution
    if ! "${INSTALL_DIR}/${BINARY_NAME}" --version &>/dev/null; then
        error "Installation failed: '${INSTALL_DIR}/${BINARY_NAME} --version' did not run successfully."
    fi
    success "Installation verification passed"
}

# Show getting started information
show_getting_started() {
    echo ""
    echo -e "${GREEN}🎉 Fizeau installed successfully!${NC}"
    echo ""
    echo -e "${BLUE}📚 Next Steps:${NC}"
    echo "   ${BINARY_NAME} version     Check your installation"
    echo "   ${BINARY_NAME} update      Check for and install updates"
    echo "   ${BINARY_NAME} providers   List configured LLM providers"
    echo "   ${BINARY_NAME} import pi   Import configuration from Pi"
    echo ""
    echo -e "${BLUE}📖 Documentation:${NC}"
    echo "   https://github.com/${REPO}"
    echo ""
    echo -e "${BLUE}🔧 Binary Location:${NC}"
    echo "   ${INSTALL_DIR}/${BINARY_NAME}"
    echo ""
    echo -e "${BLUE}⚡ Quick Start:${NC}"
    echo "   ${BINARY_NAME} --help              Show all commands and options"
    echo "   ${BINARY_NAME} -p \"Your task\"      Run a quick task with default provider"
    echo ""
    
    if command -v "${BINARY_NAME}" &>/dev/null; then
        success "Fizeau is ready to use! Run '${BINARY_NAME} --version' to verify."
    else
        warn "Please restart your shell or run the following to use ${BINARY_NAME} immediately:"
        echo ""
        case "$SHELL_NAME" in
            fish)
                echo "  source ${RC_FILE}"
                ;;
            *)
                echo "  source $RC_FILE"
                ;;
        esac
    fi
}

# Main installation flow
main() {
    echo -e "${BLUE}🚀 Installing Fizeau — Embeddable Go Agent Runtime${NC}"
    echo ""
    
    check_prerequisites
    detect_platform
    
    TAG=$(get_latest_release)
    install_binary "$TAG"
    configure_path
    verify_installation
    show_getting_started
}

# Run installation
main "$@"
