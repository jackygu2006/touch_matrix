#!/bin/bash
# 小奈Matrix 一键部署
# 建议先配 SSH 免密: ssh-copy-id root@114.55.132.92

SERVER="root@114.55.132.92"
REMOTE_DIR="/root/opt/xiaonai-matrix"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== 1. 编译 Linux 版本 ==="
cd "$SCRIPT_DIR"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o nftouch-server . || exit 1
echo "编译完成: $(ls -lh nftouch-server | awk '{print $5}')"

echo ""
echo "=== 2. 停止旧服务 ==="
ssh "$SERVER" "pkill -9 -f nftouch-server; fuser -k 8443/tcp" 2>/dev/null || true
sleep 2
echo "已停止"

echo ""
echo "=== 3. 上传文件 ==="
ssh "$SERVER" "mkdir -p $REMOTE_DIR/static"
scp nftouch-server "$SERVER:$REMOTE_DIR/" || exit 1
scp static/*.html static/*.css static/*.js "$SERVER:$REMOTE_DIR/static/" || exit 1
scp .env "$SERVER:$REMOTE_DIR/" || exit 1
echo "上传完成"

echo ""
echo "=== 4. 启动服务 ==="
# 拿到本地的密码配置，写入服务器启动脚本
source .env 2>/dev/null || true
E="${ADMIN_EMAIL:-admin@nftouch.local}"
P="${ADMIN_PASSWORD:-nf123456}"
J="${JWT_SECRET:-change-me}"

# 在服务器上写启动脚本并后台执行
ssh "$SERVER" "cat > /tmp/start-nftouch.sh << SCRIPT
#!/bin/bash
cd $REMOTE_DIR
pkill -9 -f nftouch-server 2>/dev/null
sleep 1
export ADMIN_EMAIL='$E'
export ADMIN_PASSWORD='$P'
export JWT_SECRET='$J'
nohup ./nftouch-server </dev/null > nftouch.log 2>&1 &
echo \$! > /tmp/nftouch.pid
SCRIPT
bash /tmp/start-nftouch.sh"
echo "已发送启动命令"
sleep 3

echo ""
echo "=== 5. 验证 ==="
if ssh "$SERVER" "curl -s http://localhost:8443/health" | grep -q ok; then
    echo "✅ 服务启动成功"
else
    echo "❌ 启动失败，查看日志:"
    ssh "$SERVER" "tail -20 $REMOTE_DIR/nftouch.log"
    exit 1
fi

echo ""
echo "=== 6. 清理 ==="
rm -f "$SCRIPT_DIR/nftouch-server"
echo "=== 部署完成 ==="
echo "访问: http://114.55.132.92:8443/"
