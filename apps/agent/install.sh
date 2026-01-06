#!/bin/bash
# BloxOs Agent Installation Script
# Usage: curl -sSL https://your-server/install.sh | bash -s -- -t YOUR_TOKEN -s http://server:3001

set -e

# Default values
SERVER_URL="http://localhost:3001"
TOKEN=""
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/bloxos"

# Parse arguments
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
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Check if token is provided
if [ -z "$TOKEN" ]; then
    echo "Error: Token is required. Use -t or --token"
    exit 1
fi

echo "BloxOs Agent Installer"
echo "======================"
echo "Server: $SERVER_URL"
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo "Please run as root (sudo)"
    exit 1
fi

# Detect architecture
ARCH=$(uname -m)
case $ARCH in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64)
        ARCH="arm64"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

echo "Architecture: $ARCH"

# Create config directory
mkdir -p "$CONFIG_DIR"

# Create environment file
cat > "$CONFIG_DIR/agent.env" << EOF
BLOXOS_SERVER=$SERVER_URL
BLOXOS_TOKEN=$TOKEN
EOF

chmod 600 "$CONFIG_DIR/agent.env"

# Download binary (TODO: Replace with actual download URL)
echo "Downloading agent..."
# For now, we'll just show instructions to build manually
cat << 'EOF'

To build the agent manually:

1. On the rig, install Go 1.22+:
   curl -LO https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
   tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
   export PATH=$PATH:/usr/local/go/bin

2. Build the agent:
   cd /path/to/bloxos/apps/agent
   go build -o bloxos-agent ./cmd/agent

3. Install:
   cp bloxos-agent /usr/local/bin/
   chmod +x /usr/local/bin/bloxos-agent

4. Install systemd service:
   cp bloxos-agent.service /etc/systemd/system/
   systemctl daemon-reload
   systemctl enable bloxos-agent
   systemctl start bloxos-agent

5. Check status:
   systemctl status bloxos-agent
   journalctl -u bloxos-agent -f

EOF

echo ""
echo "Config saved to $CONFIG_DIR/agent.env"
echo "Token: $TOKEN"
