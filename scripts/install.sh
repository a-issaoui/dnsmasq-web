#!/usr/bin/env bash
# dnsmasq-web installer — builds the binary, installs to /opt/dnsmasq-web,
# registers the systemd service and starts it at boot.
#
#   sudo ./scripts/install.sh                install (or update) + enable + start
#   sudo ./scripts/install.sh --intercept    also route this machine's DNS through dnsmasq
#   sudo ./scripts/install.sh --no-intercept never ask about DNS interception
#   sudo ./scripts/install.sh uninstall      stop + disable + remove
set -euo pipefail

INSTALL_DIR=/opt/dnsmasq-web
UNIT=/etc/systemd/system/dnsmasq-web.service
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ $EUID -ne 0 ]]; then
    echo "✗ run as root:  sudo $0 ${1:-}" >&2
    exit 1
fi

if [[ "${1:-}" == "uninstall" ]]; then
    echo "→ stopping and removing dnsmasq-web…"
    systemctl disable --now dnsmasq-web 2>/dev/null || true
    rm -f "$UNIT"
    systemctl daemon-reload
    rm -rf "$INSTALL_DIR"
    echo "✓ uninstalled (backups in /var/backups/dnsmasq-web were kept)"
    if grep -qE '^\s*nameserver\s+127\.0\.0\.1' /etc/resolv.conf 2>/dev/null; then
        echo "ℹ this machine still resolves through dnsmasq (the console is gone, dnsmasq is not)."
        echo "  To undo the interception too: sudo bash $REPO_DIR/scripts/dnsmasq-manager.sh stop"
    fi
    exit 0
fi

command -v dnsmasq >/dev/null || echo "⚠ dnsmasq not found — install it first (config validation and service control need it)"

# dnscrypt-proxy powers the optional encrypted-upstream (DoH) feature.
# Best-effort install; the console's DNS → Upstream toggle configures and
# enables it, so nothing is activated here.
if ! command -v dnscrypt-proxy >/dev/null; then
    echo "→ installing dnscrypt-proxy (for encrypted upstream DNS)…"
    if command -v dnf >/dev/null; then dnf install -y -q dnscrypt-proxy 2>/dev/null || true
    elif command -v apt-get >/dev/null; then apt-get install -y -q dnscrypt-proxy 2>/dev/null || true
    elif command -v pacman >/dev/null; then pacman -S --noconfirm dnscrypt-proxy 2>/dev/null || true
    fi
    command -v dnscrypt-proxy >/dev/null \
        && echo "  ✓ dnscrypt-proxy installed (enable it from DNS → Upstream in the console)" \
        || echo "  ⚠ could not install dnscrypt-proxy — the encrypted-upstream toggle will stay disabled"
fi

# ── Build ────────────────────────────────────────────────────────────
if command -v go >/dev/null; then
    echo "→ building…"
    (cd "$REPO_DIR" && go build -o dnsmasq-web .)
elif [[ ! -x "$REPO_DIR/dnsmasq-web" ]]; then
    echo "✗ Go toolchain not found and no prebuilt binary at $REPO_DIR/dnsmasq-web" >&2
    exit 1
else
    echo "→ Go not found, using prebuilt binary"
fi

# ── Install files ────────────────────────────────────────────────────
echo "→ installing to $INSTALL_DIR…"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$REPO_DIR/dnsmasq-web" "$INSTALL_DIR/dnsmasq-web.new"
mv -f "$INSTALL_DIR/dnsmasq-web.new" "$INSTALL_DIR/dnsmasq-web"   # atomic swap while running
cp -r  "$REPO_DIR/templates" "$REPO_DIR/static" "$INSTALL_DIR/"
mkdir -p "$INSTALL_DIR/scripts"
install -m 0755 "$REPO_DIR/scripts/dnsmasq-manager.sh" "$INSTALL_DIR/scripts/"
[[ -f "$REPO_DIR/README.md" ]] && cp "$REPO_DIR/README.md" "$INSTALL_DIR/"

# ── Service ──────────────────────────────────────────────────────────
echo "→ registering systemd service…"
cp "$REPO_DIR/scripts/dnsmasq-web.service" "$UNIT"
systemctl daemon-reload
systemctl enable --now dnsmasq-web

sleep 1
if systemctl is-active --quiet dnsmasq-web; then
    PORT=$(grep -oP '(?<=Environment=PORT=)\d+' "$UNIT" || echo 8053)
    echo ""
    echo "✓ dnsmasq-web is running and enabled at boot"
    echo "  → http://localhost:${PORT}"
    echo "  logs:   journalctl -u dnsmasq-web -f"
    echo "  update: git pull && sudo ./scripts/install.sh"
else
    echo "✗ service failed to start — check: journalctl -u dnsmasq-web -n 30" >&2
    exit 1
fi

# ── DNS interception (optional) ──────────────────────────────────────
# Without this, dnsmasq only serves clients that are explicitly pointed
# at it — this machine keeps using whatever resolver NetworkManager set,
# and the live query stream stays empty. Interception routes the local
# machine's DNS through dnsmasq (persists across reboots; reversible
# with: sudo bash /opt/dnsmasq-web/scripts/dnsmasq-manager.sh stop).
INTERCEPT="${1:-}"
if grep -qE '^\s*nameserver\s+127\.0\.0\.1' /etc/resolv.conf 2>/dev/null; then
    echo "✓ this machine already resolves through dnsmasq"
elif [[ "$INTERCEPT" == "--intercept" ]]; then
    bash "$INSTALL_DIR/scripts/dnsmasq-manager.sh" start
elif [[ "$INTERCEPT" != "--no-intercept" && -t 0 ]]; then
    echo ""
    read -r -p "Route this machine's DNS through dnsmasq so you get caching and the live query stream? [y/N] " ans
    if [[ "$ans" =~ ^[Yy]$ ]]; then
        bash "$INSTALL_DIR/scripts/dnsmasq-manager.sh" start
    else
        echo "  skipped — enable later with: sudo bash $INSTALL_DIR/scripts/dnsmasq-manager.sh start"
    fi
else
    echo "ℹ this machine does not resolve through dnsmasq — enable with:"
    echo "  sudo bash $INSTALL_DIR/scripts/dnsmasq-manager.sh start"
fi
