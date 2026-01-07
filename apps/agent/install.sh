#!/bin/bash
#
# BloxOS Agent Installer
# 
# Usage:
#   curl -sSL https://your-server/install.sh | sudo bash -s -- --token YOUR_TOKEN --server http://server:3001
#
# Or download and run:
#   wget -O install.sh https://your-server/install.sh
#   chmod +x install.sh
#   sudo ./install.sh --token YOUR_TOKEN --server http://server:3001
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
SERVER_URL=""
TOKEN=""
INSTALL_DIR="/opt/bloxos"
BIN_DIR="/usr/local/bin"
CONFIG_DIR="/etc/bloxos"
SERVICE_NAME="bloxos-agent"
GITHUB_REPO="bokiko/bloxos"
AGENT_VERSION="latest"

# Print colored message
print_msg() {
    echo -e "${2:-$NC}$1${NC}"
}

print_header() {
    echo ""
    echo "=========================================="
    print_msg "  BloxOS Agent Installer" "$BLUE"
    echo "=========================================="
    echo ""
}

print_success() {
    print_msg "✓ $1" "$GREEN"
}

print_warning() {
    print_msg "⚠ $1" "$YELLOW"
}

print_error() {
    print_msg "✗ $1" "$RED"
}

# Show usage
usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -t, --token TOKEN     Rig authentication token (required)"
    echo "  -s, --server URL      BloxOS server URL (required)"
    echo "  -v, --version VER     Agent version to install (default: latest)"
    echo "  -h, --help            Show this help message"
    echo ""
    echo "Example:"
    echo "  curl -sSL https://bloxos.io/install.sh | sudo bash -s -- -t mytoken -s http://192.168.1.100:3001"
    echo ""
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -t|--token)
                TOKEN="$2"
                shift 2
                ;;
            -s|--server)
                SERVER_URL="$2"
                shift 2
                ;;
            -v|--version)
                AGENT_VERSION="$2"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
}

# Check if running as root
check_root() {
    if [ "$EUID" -ne 0 ]; then
        print_error "Please run as root (use sudo)"
        exit 1
    fi
}

# Detect system info
detect_system() {
    # Detect OS
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$NAME
        OS_VERSION=$VERSION_ID
    else
        OS=$(uname -s)
        OS_VERSION=$(uname -r)
    fi

    # Detect architecture
    ARCH=$(uname -m)
    case $ARCH in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        armv7l|armhf)
            ARCH="armv7"
            ;;
        *)
            print_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    # Detect GPU
    GPU_VENDOR="none"
    if command -v nvidia-smi &> /dev/null; then
        GPU_VENDOR="nvidia"
    elif command -v rocm-smi &> /dev/null || [ -d /sys/class/drm/card0/device ] && grep -q "0x1002" /sys/class/drm/card0/device/vendor 2>/dev/null; then
        GPU_VENDOR="amd"
    fi

    print_msg "System Information:" "$BLUE"
    echo "  OS: $OS $OS_VERSION"
    echo "  Architecture: $ARCH"
    echo "  GPU: $GPU_VENDOR"
    echo ""
}

# Install dependencies
install_dependencies() {
    print_msg "Installing dependencies..." "$BLUE"
    
    if command -v apt-get &> /dev/null; then
        apt-get update -qq
        apt-get install -y -qq curl wget jq > /dev/null
    elif command -v yum &> /dev/null; then
        yum install -y -q curl wget jq > /dev/null
    elif command -v dnf &> /dev/null; then
        dnf install -y -q curl wget jq > /dev/null
    elif command -v pacman &> /dev/null; then
        pacman -Sy --noconfirm curl wget jq > /dev/null
    else
        print_warning "Could not detect package manager. Please ensure curl, wget, and jq are installed."
    fi
    
    print_success "Dependencies installed"
}

# Download agent binary
download_agent() {
    print_msg "Downloading BloxOS Agent..." "$BLUE"

    # Create directories
    mkdir -p "$INSTALL_DIR"
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$INSTALL_DIR/miners"
    mkdir -p "$INSTALL_DIR/logs"

    # Get download URL
    if [ "$AGENT_VERSION" = "latest" ]; then
        # Get latest release from GitHub
        RELEASE_URL="https://api.github.com/repos/$GITHUB_REPO/releases/latest"
        DOWNLOAD_URL=$(curl -sL "$RELEASE_URL" | jq -r ".assets[] | select(.name | contains(\"linux-$ARCH\")) | .browser_download_url" | head -1)
        
        if [ -z "$DOWNLOAD_URL" ] || [ "$DOWNLOAD_URL" = "null" ]; then
            print_warning "No pre-built binary found. Will attempt to build from source."
            build_from_source
            return
        fi
    else
        DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/v$AGENT_VERSION/bloxos-agent-linux-$ARCH"
    fi

    # Download binary
    if [ -n "$DOWNLOAD_URL" ] && [ "$DOWNLOAD_URL" != "null" ]; then
        print_msg "Downloading from: $DOWNLOAD_URL"
        if curl -sL "$DOWNLOAD_URL" -o "$INSTALL_DIR/bloxos-agent"; then
            chmod +x "$INSTALL_DIR/bloxos-agent"
            ln -sf "$INSTALL_DIR/bloxos-agent" "$BIN_DIR/bloxos-agent"
            print_success "Agent downloaded successfully"
        else
            print_warning "Download failed. Will attempt to build from source."
            build_from_source
        fi
    else
        build_from_source
    fi
}

# Build from source (if no pre-built binary available)
build_from_source() {
    print_msg "Building from source..." "$BLUE"
    
    # Check if Go is installed
    if ! command -v go &> /dev/null; then
        print_msg "Installing Go..." "$BLUE"
        
        GO_VERSION="1.22.0"
        GO_ARCH="$ARCH"
        if [ "$GO_ARCH" = "amd64" ]; then
            GO_ARCH="amd64"
        elif [ "$GO_ARCH" = "arm64" ]; then
            GO_ARCH="arm64"
        else
            print_error "Cannot install Go for architecture: $ARCH"
            exit 1
        fi
        
        curl -sL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -o /tmp/go.tar.gz
        tar -C /usr/local -xzf /tmp/go.tar.gz
        rm /tmp/go.tar.gz
        export PATH=$PATH:/usr/local/go/bin
        
        print_success "Go installed"
    fi
    
    # Clone and build
    BUILD_DIR=$(mktemp -d)
    cd "$BUILD_DIR"
    
    print_msg "Cloning repository..."
    git clone --depth 1 "https://github.com/$GITHUB_REPO.git" bloxos
    cd bloxos/apps/agent
    
    print_msg "Building agent..."
    go build -o "$INSTALL_DIR/bloxos-agent" ./cmd/agent
    
    chmod +x "$INSTALL_DIR/bloxos-agent"
    ln -sf "$INSTALL_DIR/bloxos-agent" "$BIN_DIR/bloxos-agent"
    
    # Cleanup
    rm -rf "$BUILD_DIR"
    
    print_success "Agent built successfully"
}

# Create configuration
create_config() {
    print_msg "Creating configuration..." "$BLUE"

    # Create environment file
    cat > "$CONFIG_DIR/agent.env" << EOF
# BloxOS Agent Configuration
BLOXOS_SERVER=$SERVER_URL
BLOXOS_TOKEN=$TOKEN
BLOXOS_DEBUG=false
BLOXOS_GPU=true
BLOXOS_CPU=true
BLOXOS_POLL_INTERVAL=30
EOF

    chmod 600 "$CONFIG_DIR/agent.env"
    print_success "Configuration created at $CONFIG_DIR/agent.env"
}

# Create systemd service
create_service() {
    print_msg "Creating systemd service..." "$BLUE"

    cat > "/etc/systemd/system/$SERVICE_NAME.service" << EOF
[Unit]
Description=BloxOS Mining Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
EnvironmentFile=$CONFIG_DIR/agent.env
ExecStart=$INSTALL_DIR/bloxos-agent --server \${BLOXOS_SERVER} --token \${BLOXOS_TOKEN}
Restart=always
RestartSec=10
StandardOutput=append:$INSTALL_DIR/logs/agent.log
StandardError=append:$INSTALL_DIR/logs/agent.log

# Security hardening
NoNewPrivileges=false
ProtectSystem=false
ProtectHome=false

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    print_success "Systemd service created"
}

# Start the agent
start_agent() {
    print_msg "Starting BloxOS Agent..." "$BLUE"

    systemctl enable "$SERVICE_NAME"
    systemctl start "$SERVICE_NAME"
    
    # Wait a moment and check status
    sleep 3
    
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        print_success "Agent started successfully"
    else
        print_error "Agent failed to start. Check logs with: journalctl -u $SERVICE_NAME -f"
        exit 1
    fi
}

# Show completion message
show_complete() {
    echo ""
    echo "=========================================="
    print_msg "  Installation Complete!" "$GREEN"
    echo "=========================================="
    echo ""
    echo "Configuration: $CONFIG_DIR/agent.env"
    echo "Installation:  $INSTALL_DIR"
    echo "Logs:          $INSTALL_DIR/logs/agent.log"
    echo ""
    echo "Useful commands:"
    echo "  Status:  systemctl status $SERVICE_NAME"
    echo "  Logs:    journalctl -u $SERVICE_NAME -f"
    echo "  Restart: systemctl restart $SERVICE_NAME"
    echo "  Stop:    systemctl stop $SERVICE_NAME"
    echo ""
    print_msg "Your rig should now appear in your BloxOS dashboard!" "$GREEN"
    echo ""
}

# Uninstall function
uninstall() {
    print_msg "Uninstalling BloxOS Agent..." "$YELLOW"
    
    # Stop and disable service
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
    rm -f "/etc/systemd/system/$SERVICE_NAME.service"
    systemctl daemon-reload
    
    # Remove files
    rm -f "$BIN_DIR/bloxos-agent"
    rm -rf "$INSTALL_DIR"
    rm -rf "$CONFIG_DIR"
    
    print_success "BloxOS Agent uninstalled"
}

# Main installation flow
main() {
    print_header
    parse_args "$@"
    
    # Check for uninstall flag
    if [ "$1" = "uninstall" ] || [ "$1" = "--uninstall" ]; then
        check_root
        uninstall
        exit 0
    fi
    
    # Validate required arguments
    if [ -z "$TOKEN" ]; then
        print_error "Token is required. Use -t or --token"
        usage
        exit 1
    fi
    
    if [ -z "$SERVER_URL" ]; then
        print_error "Server URL is required. Use -s or --server"
        usage
        exit 1
    fi
    
    check_root
    detect_system
    install_dependencies
    download_agent
    create_config
    create_service
    start_agent
    show_complete
}

# Run main function
main "$@"
