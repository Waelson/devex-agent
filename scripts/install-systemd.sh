#!/usr/bin/env bash
set -euo pipefail

AGENT_BIN="${AGENT_BIN:-./devex-agent}"
INSTALL_PATH="/usr/local/bin/devex-agent"
CONFIG_DIR="/etc/devex-agent"
STATE_DIR="/var/lib/devex-agent"
SERVICE_FILE="/etc/systemd/system/devex-agent.service"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$(id -u)" -ne 0 ]; then
  echo "ERROR: this script must be run as root (use sudo)" >&2
  exit 1
fi

if [ ! -f "$AGENT_BIN" ]; then
  echo "ERROR: agent binary not found: $AGENT_BIN" >&2
  echo "Set AGENT_BIN=/path/to/devex-agent or build it first with: go build -o devex-agent ./cmd/devex-agent" >&2
  exit 1
fi

echo "Creating directories..."
mkdir -p "$CONFIG_DIR"
mkdir -p "$STATE_DIR/locks"
mkdir -p "$STATE_DIR/gateway"
chmod 700 "$CONFIG_DIR"
chmod 700 "$STATE_DIR"

echo "Installing binary..."
cp "$AGENT_BIN" "$INSTALL_PATH"
chmod 755 "$INSTALL_PATH"

echo "Installing systemd unit..."
cp "$SCRIPT_DIR/devex-agent.service" "$SERVICE_FILE"

systemctl daemon-reload
systemctl enable devex-agent

echo ""
echo "Installation complete."
echo ""
echo "Next steps:"
echo "  1. Copy a config file:"
echo "     cp $SCRIPT_DIR/config-runtime.yaml $CONFIG_DIR/config.yaml   # for Runtime Agent"
echo "     cp $SCRIPT_DIR/config-gateway.yaml $CONFIG_DIR/config.yaml   # for Gateway Agent"
echo "  2. Set the agent token:"
echo "     echo 'YOUR_AGENT_TOKEN' | tee $CONFIG_DIR/token > /dev/null"
echo "     chmod 600 $CONFIG_DIR/config.yaml $CONFIG_DIR/token"
echo "  3. Edit $CONFIG_DIR/config.yaml to match your environment."
echo "  4. Start the service:"
echo "     systemctl start devex-agent"
echo "     systemctl status devex-agent"
echo "  5. Follow logs:"
echo "     journalctl -u devex-agent -f"
