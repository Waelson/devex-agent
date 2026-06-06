#!/usr/bin/env bash
set -euo pipefail

INSTALL_PATH="/usr/local/bin/devex-agent"
SERVICE_FILE="/etc/systemd/system/devex-agent.service"

if [ "$(id -u)" -ne 0 ]; then
  echo "ERROR: this script must be run as root (use sudo)" >&2
  exit 1
fi

echo "Stopping and disabling devex-agent service..."
systemctl stop devex-agent  || true
systemctl disable devex-agent || true

echo "Removing systemd unit..."
rm -f "$SERVICE_FILE"
systemctl daemon-reload

echo "Removing binary..."
rm -f "$INSTALL_PATH"

echo ""
echo "Service devex-agent removed."
echo ""
echo "The following directories were NOT removed to prevent data loss:"
echo "  /etc/devex-agent    (config and token)"
echo "  /var/lib/devex-agent (local state)"
echo ""
echo "To remove them manually:"
echo "  sudo rm -rf /etc/devex-agent"
echo "  sudo rm -rf /var/lib/devex-agent"
