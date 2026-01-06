#!/bin/bash

#############################################
# BloxOs VM Setup Script
# Run this on a fresh Ubuntu 24.04 LTS VM
# 
# Usage: chmod +x setup-vm.sh && ./setup-vm.sh
#############################################

set -e  # Exit on error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${BLUE}[*]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[+]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[!]${NC} $1"
}

print_error() {
    echo -e "${RED}[-]${NC} $1"
}

#############################################
# Check if running as root
#############################################
if [ "$EUID" -eq 0 ]; then
    print_error "Please do not run as root. Run as your normal user."
    exit 1
fi

#############################################
# System Update
#############################################
print_status "Updating system packages..."
sudo apt update && sudo apt upgrade -y

#############################################
# Install Essential Tools
#############################################
print_status "Installing essential tools..."
sudo apt install -y \
    curl \
    wget \
    git \
    vim \
    nano \
    htop \
    tmux \
    screen \
    build-essential \
    software-properties-common \
    apt-transport-https \
    ca-certificates \
    gnupg \
    lsb-release \
    unzip \
    jq \
    tree

print_success "Essential tools installed"

#############################################
# Install Node.js 22 LTS
#############################################
print_status "Installing Node.js 22 LTS..."

# Remove old Node.js if exists
sudo apt remove -y nodejs npm 2>/dev/null || true

# Install via NodeSource
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs

# Verify
node_version=$(node --version)
print_success "Node.js installed: $node_version"

#############################################
# Install pnpm
#############################################
print_status "Installing pnpm..."
curl -fsSL https://get.pnpm.io/install.sh | sh -

# Add to current shell
export PNPM_HOME="$HOME/.local/share/pnpm"
export PATH="$PNPM_HOME:$PATH"

# Add to .bashrc
if ! grep -q "PNPM_HOME" ~/.bashrc; then
    echo '' >> ~/.bashrc
    echo '# pnpm' >> ~/.bashrc
    echo 'export PNPM_HOME="$HOME/.local/share/pnpm"' >> ~/.bashrc
    echo 'export PATH="$PNPM_HOME:$PATH"' >> ~/.bashrc
fi

print_success "pnpm installed"

#############################################
# Install Go 1.22
#############################################
print_status "Installing Go 1.22..."

GO_VERSION="1.22.5"
wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz

# Add to PATH
if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
    echo '' >> ~/.bashrc
    echo '# Go' >> ~/.bashrc
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    echo 'export GOPATH=$HOME/go' >> ~/.bashrc
    echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc
fi

export PATH=$PATH:/usr/local/go/bin
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin

go_version=$(/usr/local/go/bin/go version)
print_success "Go installed: $go_version"

#############################################
# Install Docker
#############################################
print_status "Installing Docker..."

# Remove old versions
sudo apt remove -y docker docker-engine docker.io containerd runc 2>/dev/null || true

# Add Docker's official GPG key
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

# Add repository
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# Install Docker
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# Add user to docker group (no sudo needed for docker commands)
sudo usermod -aG docker $USER

print_success "Docker installed"
print_warning "You may need to log out and back in for docker group permissions"

#############################################
# Install PostgreSQL 16
#############################################
print_status "Installing PostgreSQL 16..."

# Add PostgreSQL repository
sudo sh -c 'echo "deb http://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list'
wget --quiet -O - https://www.postgresql.org/media/keys/ACCC4CF8.asc | sudo apt-key add -

sudo apt update
sudo apt install -y postgresql-16 postgresql-contrib-16

# Start and enable
sudo systemctl start postgresql
sudo systemctl enable postgresql

# Create bloxos user and database
sudo -u postgres psql -c "CREATE USER bloxos WITH PASSWORD 'bloxos_dev_password';" 2>/dev/null || true
sudo -u postgres psql -c "CREATE DATABASE bloxos OWNER bloxos;" 2>/dev/null || true
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE bloxos TO bloxos;" 2>/dev/null || true

print_success "PostgreSQL 16 installed and configured"

#############################################
# Install Redis
#############################################
print_status "Installing Redis..."

sudo apt install -y redis-server

# Configure Redis to start on boot
sudo systemctl enable redis-server
sudo systemctl start redis-server

print_success "Redis installed"

#############################################
# Install Caddy (Reverse Proxy)
#############################################
print_status "Installing Caddy..."

sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list

sudo apt update
sudo apt install -y caddy

print_success "Caddy installed"

#############################################
# Install OpenCode (Claude Code CLI)
#############################################
print_status "Installing OpenCode..."

# Using npm to install opencode globally
npm install -g @anthropic-ai/claude-code 2>/dev/null || {
    print_warning "OpenCode installation via npm failed, trying alternative..."
    # Alternative: download directly if available
    print_warning "Please install OpenCode manually after setup"
}

print_success "OpenCode installation attempted"

#############################################
# Create Project Directory
#############################################
print_status "Creating project directory..."

mkdir -p ~/projects/bloxos
cd ~/projects/bloxos

print_success "Project directory created at ~/projects/bloxos"

#############################################
# Configure Git
#############################################
print_status "Configuring Git..."

# Check if git is configured
if [ -z "$(git config --global user.email)" ]; then
    print_warning "Git email not configured. Please run:"
    echo "  git config --global user.email 'your@email.com'"
    echo "  git config --global user.name 'Your Name'"
else
    print_success "Git already configured"
fi

# Set default branch to main
git config --global init.defaultBranch main

#############################################
# Create useful aliases
#############################################
print_status "Adding useful aliases..."

if ! grep -q "# BloxOs aliases" ~/.bashrc; then
    cat >> ~/.bashrc << 'EOF'

# BloxOs aliases
alias ll='ls -la'
alias dc='docker compose'
alias dcup='docker compose up -d'
alias dcdown='docker compose down'
alias dclogs='docker compose logs -f'
alias bloxos='cd ~/projects/bloxos'

# Quick edit
alias edit='nano'

# Git shortcuts
alias gs='git status'
alias gc='git commit'
alias gp='git push'
alias gl='git log --oneline -10'
EOF
fi

print_success "Aliases added"

#############################################
# Setup firewall (UFW)
#############################################
print_status "Configuring firewall..."

sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow ssh
sudo ufw allow 3000/tcp  # Dashboard
sudo ufw allow 3001/tcp  # API
sudo ufw allow 80/tcp    # HTTP
sudo ufw allow 443/tcp   # HTTPS

# Enable without prompt
echo "y" | sudo ufw enable

print_success "Firewall configured"

#############################################
# Summary
#############################################
echo ""
echo "============================================"
echo -e "${GREEN}BloxOs VM Setup Complete!${NC}"
echo "============================================"
echo ""
echo "Installed:"
echo "  - Node.js $(node --version 2>/dev/null || echo 'N/A')"
echo "  - pnpm"
echo "  - Go $(/usr/local/go/bin/go version 2>/dev/null | awk '{print $3}' || echo 'N/A')"
echo "  - Docker $(docker --version 2>/dev/null | awk '{print $3}' | tr -d ',' || echo 'N/A')"
echo "  - PostgreSQL 16"
echo "  - Redis"
echo "  - Caddy"
echo ""
echo "Database:"
echo "  - User: bloxos"
echo "  - Password: bloxos_dev_password"
echo "  - Database: bloxos"
echo "  - Connection: postgresql://bloxos:bloxos_dev_password@localhost:5432/bloxos"
echo ""
echo "Next steps:"
echo "  1. Log out and back in (for docker group)"
echo "  2. cd ~/projects/bloxos"
echo "  3. Run: ./init-project.sh"
echo ""
echo -e "${YELLOW}IMPORTANT: Change the database password in production!${NC}"
echo ""
