#!/usr/bin/env bash
# dnsmasq-manager — service control + local DNS interception for dnsmasq-web.
#
#   start    start dnsmasq + route this machine's DNS through it
#   stop     stop dnsmasq + restore the machine's original DNS
#   restart  restart the dnsmasq service (DNS routing untouched)
#   reload   SIGHUP dnsmasq (re-reads hosts/leases, NOT dnsmasq.conf)
#   status   show service + resolver state

set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "✗ run as root:  sudo $0 ${1:-}" >&2
    exit 1
fi

BACKUP_FILE="/etc/resolv.conf.dnsmasq.backup"
RESOLVED_DROPIN="/etc/systemd/resolved.conf.d/dnsmasq-web.conf"

detect_manager() {
    if systemctl is-active --quiet NetworkManager; then
        echo "NetworkManager"
    elif systemctl is-active --quiet systemd-resolved; then
        echo "systemd-resolved"
    else
        echo "manual"
    fi
}

# First active connection whose device is a real interface (field-exact match,
# so devices like wlo1/eno1 that merely contain "lo" are not dropped).
get_active_connection() {
    nmcli -t -f NAME,DEVICE connection show --active 2>/dev/null \
        | awk -F: '$2 != "lo" && $2 != ""' | head -n1
}

get_connection_name()   { echo "$1" | cut -d: -f1; }
get_connection_device() { echo "$1" | cut -d: -f2; }

backup_resolv() {
    if [ ! -f "$BACKUP_FILE" ]; then
        cp /etc/resolv.conf "$BACKUP_FILE"
    fi
}

restore_resolv() {
    if [ -f "$BACKUP_FILE" ]; then
        # --remove-destination: /etc/resolv.conf may be a symlink (resolved);
        # replace the link itself instead of writing through it
        cp --remove-destination "$BACKUP_FILE" /etc/resolv.conf
        rm -f "$BACKUP_FILE"
    fi
}

start_dnsmasq() {
    echo "🚀 Starting dnsmasq..."

    systemctl enable --now dnsmasq

    MANAGER=$(detect_manager)
    echo "Detected DNS manager: $MANAGER"

    if [ "$MANAGER" = "NetworkManager" ]; then
        ACTIVE=$(get_active_connection)
        CONN=$(get_connection_name "$ACTIVE")
        DEV=$(get_connection_device "$ACTIVE")
        if [ -z "$CONN" ]; then
            echo "✗ no active NetworkManager connection found — nothing changed" >&2
            exit 1
        fi
        backup_resolv
        nmcli connection modify "$CONN" ipv4.dns "127.0.0.1"
        nmcli connection modify "$CONN" ipv4.ignore-auto-dns yes
        nmcli connection modify "$CONN" ipv6.ignore-auto-dns yes
        # Use device reapply to avoid dropping the network connection
        if [ -n "$DEV" ]; then
            nmcli device reapply "$DEV" 2>/dev/null || nmcli connection up "$CONN"
        else
            nmcli connection up "$CONN"
        fi

    elif [ "$MANAGER" = "systemd-resolved" ]; then
        # Persistent drop-in — runtime `resolvectl dns` does not survive a
        # resolved restart. Domains=~. routes ALL queries to dnsmasq.
        mkdir -p "$(dirname "$RESOLVED_DROPIN")"
        printf '[Resolve]\nDNS=127.0.0.1\nDomains=~.\n' > "$RESOLVED_DROPIN"
        systemctl restart systemd-resolved

    else
        echo "⚙️ Using manual resolv.conf mode"
        backup_resolv
        echo "nameserver 127.0.0.1" | tee /etc/resolv.conf > /dev/null
    fi

    echo "✅ dnsmasq interception enabled"
}

stop_dnsmasq() {
    echo "🛑 Stopping dnsmasq..."

    MANAGER=$(detect_manager)
    echo "Detected DNS manager: $MANAGER"

    # Restore the machine's resolver first, then stop the daemon it pointed at.
    if [ "$MANAGER" = "NetworkManager" ]; then
        ACTIVE=$(get_active_connection)
        CONN=$(get_connection_name "$ACTIVE")
        DEV=$(get_connection_device "$ACTIVE")
        if [ -n "$CONN" ]; then
            nmcli connection modify "$CONN" ipv4.ignore-auto-dns no
            nmcli connection modify "$CONN" ipv6.ignore-auto-dns no
            nmcli connection modify "$CONN" ipv4.dns ""
            # Use device reapply to avoid dropping the network connection
            if [ -n "$DEV" ]; then
                nmcli device reapply "$DEV" 2>/dev/null || nmcli connection up "$CONN"
            else
                nmcli connection up "$CONN"
            fi
        fi
        rm -f "$BACKUP_FILE"   # NM regenerated resolv.conf; stale backup is now wrong

    elif [ "$MANAGER" = "systemd-resolved" ]; then
        rm -f "$RESOLVED_DROPIN"
        systemctl restart systemd-resolved

    else
        restore_resolv
    fi

    systemctl stop dnsmasq

    echo "✅ dnsmasq stopped and DNS restored (still enabled at boot — use 'systemctl disable dnsmasq' to change that)"
}

reload_dnsmasq() {
    echo "🔄 Reloading dnsmasq (SIGHUP)..."
    if systemctl is-active --quiet dnsmasq; then
        # `systemctl reload` needs an ExecReload= the stock unit doesn't have,
        # and its fallback would be a full restart. kill -s HUP is the real
        # thing: re-reads hosts/leases without dropping the daemon.
        systemctl kill -s HUP dnsmasq
    else
        echo "dnsmasq is not running, starting it..."
        start_dnsmasq
    fi
    echo "✅ Configuration reloaded"
}

restart_dnsmasq() {
    echo "🔄 Restarting dnsmasq..."
    # Only restart the service, don't cycle DNS config since it's already set
    if systemctl is-active --quiet dnsmasq; then
        systemctl restart dnsmasq
    else
        start_dnsmasq
    fi
    echo "✅ dnsmasq restarted"
}

status_dns() {
    echo "------ DNS STATUS ------"
    echo "dnsmasq service:"
    systemctl status dnsmasq --no-pager | grep Active || true
    echo ""
    echo "/etc/resolv.conf:"
    cat /etc/resolv.conf
    echo ""
    echo "Test query:"
    if command -v dig >/dev/null; then
        dig +short google.com || true
    else
        getent hosts google.com || true
    fi
}

case "${1:-}" in
    start)
        start_dnsmasq
        ;;
    stop)
        stop_dnsmasq
        ;;
    restart)
        restart_dnsmasq
        ;;
    reload)
        reload_dnsmasq
        ;;
    status)
        status_dns
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|reload|status}"
        exit 1
        ;;
esac
