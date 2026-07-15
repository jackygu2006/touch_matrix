#!/bin/bash
# ============================================================
# Weak network simulator — test remote control under poor network conditions
#
# Usage:
#   sudo ./simulate_network.sh 3g        # Simulate 3G network
#   sudo ./simulate_network.sh edge      # Simulate EDGE/2G network
#   sudo ./simulate_network.sh lossy     # Simulate high packet loss
#   sudo ./simulate_network.sh off       # Restore normal network
#   sudo ./simulate_network.sh status    # Show current state
#
# How it works: macOS kernel dummynet pipes (dnctl + pfctl)
# ============================================================

set -e

ANCHOR_NAME="com.nftouch.throttle"

if [[ $EUID -ne 0 ]]; then
    echo "Error: root privileges required, run with sudo"
    exit 1
fi

reset_rules() {
    pfctl -a "$ANCHOR_NAME" -F all 2>/dev/null || true
    dnctl -q flush 2>/dev/null || true
    echo "All throttle rules cleared"
}

setup_pipe() {
    local name=$1 bw=$2 delay=$3 loss=$4

    dnctl -q flush 2>/dev/null || true
    pfctl -a "$ANCHOR_NAME" -F all 2>/dev/null || true

    # Create pipe: bandwidth(bps), latency(ms), packet loss(%)
    dnctl pipe 1 config bw "$bw" delay "$delay" plr "$loss"

    # Create dummynet rule anchor and load
    echo "dummynet in quick proto tcp from any to any pipe 1
dummynet out quick proto tcp from any to any pipe 1" | pfctl -a "$ANCHOR_NAME" -f -

    # Enable pf
    pfctl -e 2>/dev/null || true
}

case "${1:-}" in
    3g|3G)
        # 3G: ~1Mbps down, ~384Kbps up, 100ms latency, 0.5% loss
        BW="1200000"
        DELAY="100"
        LOSS="0.005"
        DESC="3G (1.2Mbps, 100ms, 0.5% loss)"
        ;;
    edge|EDGE|2g|2G)
        # EDGE/2G: 150Kbps, 400ms latency, 2% loss
        BW="150000"
        DELAY="400"
        LOSS="0.02"
        DESC="EDGE/2G (150Kbps, 400ms, 2% loss)"
        ;;
    lossy|highloss)
        # High loss: 5Mbps, 50ms latency, 10% loss
        BW="5000000"
        DELAY="50"
        LOSS="0.10"
        DESC="High Loss (5Mbps, 50ms, 10% loss)"
        ;;
    laggy|highlatency)
        # High latency: 5Mbps, 1000ms latency, 1% loss
        BW="5000000"
        DELAY="1000"
        LOSS="0.01"
        DESC="High Latency (5Mbps, 1000ms, 1% loss)"
        ;;
    slow|bad)
        # Very poor network: 50Kbps, 500ms latency, 5% loss
        BW="50000"
        DELAY="500"
        LOSS="0.05"
        DESC="Very Poor (50Kbps, 500ms, 5% loss)"
        ;;
    wifi)
        # Normal WiFi: 10Mbps, 10ms latency, 0% loss
        BW="10000000"
        DELAY="10"
        LOSS="0"
        DESC="Normal WiFi (10Mbps, 10ms)"
        ;;
    off|reset|none|disable)
        reset_rules
        echo "Network restored to normal"
        exit 0
        ;;
    status|show)
        echo "=== pf rules ==="
        pfctl -a "$ANCHOR_NAME" -s rules 2>/dev/null || echo "(no throttle rules)"
        echo ""
        echo "=== dnctl pipes ==="
        dnctl pipe list 2>/dev/null || echo "(no pipes)"
        exit 0
        ;;
    *)
        echo "Usage: sudo $0 <mode>"
        echo ""
        echo "Modes:"
        echo "  3g        Simulate 3G (1.2Mbps, 100ms, 0.5% loss)"
        echo "  edge/2g   Simulate EDGE/2G (150Kbps, 400ms, 2% loss)"
        echo "  lossy     Simulate high packet loss (5Mbps, 50ms, 10% loss)"
        echo "  laggy     Simulate high latency (5Mbps, 1000ms, 1% loss)"
        echo "  slow      Simulate very poor network (50Kbps, 500ms, 5% loss)"
        echo "  wifi      Simulate normal WiFi (10Mbps, 10ms)"
        echo "  off       Restore normal network"
        echo "  status    Show current status"
        exit 1
        ;;
esac

echo "Applying throttle: $DESC"
setup_pipe "1" "$BW" "$DELAY" "$LOSS"
echo "Active. Restore with: sudo $0 off"
