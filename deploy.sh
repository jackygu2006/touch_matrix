#!/bin/bash
# NanoFusion Matrix One-click Deploy
# Suggested: configure SSH passwordless login first: ssh-copy-id root@114.55.132.92
#
# Server config is read from .env on the server; no passwords are uploaded or hardcoded locally.

SERVER="root@114.55.132.92"
REMOTE_DIR="/root/opt/xiaonai-matrix"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== 1. Build Linux Binary ==="
cd "$SCRIPT_DIR"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o nftouch-server . || exit 1
echo "Build complete: $(ls -lh nftouch-server | awk '{print $5}')"

echo ""
echo "=== 2. Stop Old Service ==="
ssh "$SERVER" "pkill -9 -f nftouch-server; fuser -k 8443/tcp" 2>/dev/null || true
sleep 2
echo "Stopped"

echo ""
echo "=== 3. Upload Files ==="
ssh "$SERVER" "mkdir -p $REMOTE_DIR/static"
scp nftouch-server "$SERVER:$REMOTE_DIR/" || exit 1
scp VERSION "$SERVER:$REMOTE_DIR/" || exit 1
scp static/*.html static/*.css static/*.js "$SERVER:$REMOTE_DIR/static/" || exit 1
echo "Upload complete"

echo ""
echo "=== 4. Start Service ==="
# Start script reads config from .env on the server; no passwords passed locally
ssh "$SERVER" "cat > /tmp/start-nftouch.sh << SCRIPT
#!/bin/bash
cd $REMOTE_DIR
pkill -9 -f nftouch-server 2>/dev/null
sleep 1
set -a; source $REMOTE_DIR/.env; set +a
SKIP_BUMP=true nohup ./nftouch-server </dev/null > nftouch.log 2>&1 &
echo \$! > /tmp/nftouch.pid
SCRIPT
bash /tmp/start-nftouch.sh"
echo "Start command sent"
sleep 3

echo ""
echo "=== 5. Verify ==="
if ssh "$SERVER" "curl -s http://localhost:8443/health" | grep -q ok; then
    echo "✅ Service started successfully"
else
    echo "❌ Startup failed, check logs:"
    ssh "$SERVER" "tail -20 $REMOTE_DIR/nftouch.log"
    exit 1
fi

echo ""
echo "=== 6. Cleanup ==="
rm -f "$SCRIPT_DIR/nftouch-server"
echo "=== Deployment Complete ==="
echo "Access: http://114.55.132.92:8443/"
