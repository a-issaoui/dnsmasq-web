#!/usr/bin/env bash

set -e

BACKUP_FILE="/etc/resolv.conf.dnsmasq.backup"

detect_manager() {
    if systemctl is-active --quiet NetworkManager; then
        echo "NetworkManager"
    elif systemctl is-active --quiet systemd-resolved; then
        echo "systemd-resolved"
    else
        echo "manual"
    fi
}

get_active_connection() {
    nmcli -t -f NAME,DEVICE connection show --active 2>/dev/null | grep -v lo | head -n1
}

get_connection_name() {
    echo "$1" | cut -d: -f1
}

get_connection_device() {
    echo "$1" | cut -d: -f2
}

backup_resolv() {
    if [ ! -f "$BACKUP_FILE" ]; then
        cp /etc/resolv.conf "$BACKUP_FILE"
    fi
}

restore_resolv() {
    if [ -f "$BACKUP_FILE" ]; then
        cp "$BACKUP_FILE" /etc/resolv.conf
        rm -f "$BACKUP_FILE"
    fi
}

start_dnsmasq() {
    echo "🚀 Starting dnsmasq..."

    systemctl enable --now dnsmasq

    MANAGER=$(detect_manager)
    echo "Detected DNS manager: $MANAGER"

    backup_resolv

    if [ "$MANAGER" = "NetworkManager" ]; then
        ACTIVE=$(get_active_connection)
        CONN=$(get_connection_name "$ACTIVE")
        DEV=$(get_connection_device "$ACTIVE")
        if [ -n "$CONN" ]; then
            nmcli connection modify "$CONN" ipv4.dns "127.0.0.1"
            nmcli connection modify "$CONN" ipv4.ignore-auto-dns yes
            nmcli connection modify "$CONN" ipv6.ignore-auto-dns yes
            # Use device reapply to avoid dropping the network connection
            if [ -n "$DEV" ]; then
                nmcli device reapply "$DEV" 2>/dev/null || nmcli connection up "$CONN"
            else
                nmcli connection up "$CONN"
            fi
        fi

    elif [ "$MANAGER" = "systemd-resolved" ]; then
        resolvectl dns lo 127.0.0.1 || true
        systemctl restart systemd-resolved

    else
        echo "⚙️ Using manual resolv.conf mode"
        echo "nameserver 127.0.0.1" | tee /etc/resolv.conf > /dev/null
    fi

    echo "✅ dnsmasq interception enabled"
}

stop_dnsmasq() {
    echo "🛑 Stopping dnsmasq..."

    MANAGER=$(detect_manager)
    echo "Detected DNS manager: $MANAGER"

    # Disable and stop dnsmasq first
    systemctl disable --now dnsmasq 2>/dev/null || true

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

    elif [ "$MANAGER" = "systemd-resolved" ]; then
        systemctl restart systemd-resolved

    else
        restore_resolv
    fi

    restore_resolv

    echo "✅ DNS restored to original state"
}

reload_dnsmasq() {
    echo "🔄 Reloading dnsmasq configuration..."
    if systemctl is-active --quiet dnsmasq; then
        systemctl reload dnsmasq || systemctl restart dnsmasq
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
    dig +short google.com || true
}

case "$1" in
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
