# NFTouch 云控平台

## 目录结构

```
├── server/                  # Go 服务端（当前目录）
│   ├── main.go              # HTTP 路由 + 中间件
│   ├── db.go                # 数据库操作
│   ├── hub.go               # WebSocket 消息中心
│   ├── static/              # 前端静态文件
│   ├── test.sh              # 一键测试脚本
│   ├── *_test.go            # 单元测试
│   └── README.md
├── nf-touch/                # 安卓客户端
└── docker-compose.yml
```

---

## 1. 编译

```bash
# macOS 本地（arm64）
go build -o nftouch-server-macos .

# Linux 部署（x86-64）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o nftouch-server .
```

> `nftouch-server-macos` 和 `nftouch-server` 均已加入 `.gitignore`，不会入库。

---

## 2. 启动 / 停止

```bash
# === 本地开发（推荐）===
# 每次启动自动递增版本号（格式 2026.7.13.1），版本通过 GET /api/version 暴露
export $(grep -v '^#' .env | xargs) && go run .

# === 服务器部署 ===
# 1) 上传二进制和静态文件
# 2) 启动（服务器上的 .env 已配置好各项密钥）
set -a && source .env && set +a
LISTEN_ADDR=":8443" DB_PATH="./nftouch.db" \
  nohup ./nftouch-server > nftouch.log 2>&1 &

# 指定 TLS 证书（可选）
set -a && source .env && set +a
TLS_CERT="/path/to/cert.pem" TLS_KEY="/path/to/key.pem" \
  LISTEN_ADDR=":8443" DB_PATH="./nftouch.db" \
  nohup ./nftouch-server > nftouch.log 2>&1 &

# 验证
curl -s http://localhost:8443/health
# → {"status":"ok"}

curl -s http://localhost:8443/api/version
# → {"version":"2026.7.13.3"}

# 停止
pkill -f nftouch-server
lsof -i :8443 2>/dev/null || echo "端口已释放"
```

> **注意**：`source .env` 在 zsh 中不会自动 export 变量给子进程。请使用 `set -a; source .env; set +a` 或 `export $(grep -v '^#' .env | xargs)`。

---

## 3. 测试

```bash
# 一键运行（单元测试 + 静态分析 + 编译验证）
./test.sh

# 仅 Go 测试
go test -v ./...
```

> **规则**：新增 API 时**必须**同步在 `*_test.go` 中添加测试用例。提交前务必 `./test.sh` 通过。

### 当前覆盖率

| 文件 | 数量 | 覆盖的 API |
|-----|------|-----------|
| `auth_test.go` | 12 | 登录、JWT、中间件 |
| `device_test.go` | 10 | 设备列表/详情/删除/下发任务 |
| `user_test.go` | 10 | 用户 CRUD、toggle |
| `pairing_test.go` | 9 | 配对码生成/绑定 |
| `api_test.go` | 3 | Profile、Health |
| **合计** | **44** | REST API 全部覆盖 |

---

## 4. REST API 参考

### 公开接口

| 方法 | 路径 | 说明 | 认证 |
|------|------|------|------|
| `POST` | `/api/auth/login` | 登录 | 无 |
| `GET` | `/api/auth/check` | 检查登录状态 | JWT |
| `POST` | `/api/pairing-code` | 生成配对码 | 无 |
| `GET` | `/health` | 健康检查 | 无 |
| `GET` | `/api/version` | 服务端版本号 | 无 |

### 认证接口（需 JWT）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/profile` | 当前用户信息 |
| `POST` | `/api/bind` | 绑定设备 |
| `GET` | `/api/devices` | 设备列表（仅看到自己的） |
| `GET` | `/api/devices/{id}` | 设备详情 |
| `DELETE` | `/api/devices/{id}` | 删除设备 |
| `POST` | `/api/devices/{id}/task` | 下发 AI 任务 |

### 管理员接口（需 JWT + admin 角色）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/admin/users` | 用户列表 |
| `POST` | `/api/admin/users` | 创建用户 |
| `PUT` | `/api/admin/users/{id}` | 更新用户 |
| `DELETE` | `/api/admin/users/{id}` | 删除用户 |
| `PUT` | `/api/admin/users/{id}/toggle` | 启用/禁用用户 |

### 请求示例

```bash
# 登录
curl -X POST http://localhost:8443/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@nftouch.local","password":"nf123456"}'

# 获取设备列表
curl http://localhost:8443/api/devices \
  -H "Authorization: Bearer <token>"

# 获取配对码
curl -X POST http://localhost:8443/api/pairing-code \
  -H "Content-Type: application/json" \
  -d '{"device_id":"my-phone-001"}'

# 绑定设备
curl -X POST http://localhost:8443/api/bind \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"code":"123456"}'

# 下发任务
curl -X POST http://localhost:8443/api/devices/my-phone-001/task \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"打开微信"}'

# 管理员创建用户
curl -X POST http://localhost:8443/api/admin/users \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"email":"user@test.com","password":"123456","nickname":"Test","max_devices":5}'
```

### WebSocket 端点

| 路径 | 用途 | 认证 |
|------|------|------|
| `GET /ws/device` | 设备连接 | token |
| `GET /ws/dash` | 中控面板 | JWT (query `?token=`) |

---

## 5. 默认账户

| 角色 | 邮箱 | 密码 | 配额 |
|------|------|------|------|
| 管理员 | `admin@nftouch.local` | `nf123456` | 999 |
| 测试用户 | `test@nextflow.com` | `123456` | 5 |

---

## 6. 设备绑定流程

1. **手机**：安装 NF Touch 应用，授权无障碍服务
2. **手机**：设置 → 配置大模型（推荐 DeepSeek API）
3. **手机**：云控绑定 → 输入服务器地址 `ws://<IP>:8443` → 输入设备ID → **获取配对码**
4. **中控**：点击 "+ 绑定设备" → 输入 6 位配对码 → 复制 Token 发给手机
5. **手机**：输入 Token → 完成绑定

---

## 7. 环境变量

`.env` 文件：

> - `JWT_SECRET` **必须配置**，不能使用默认值，否则启动会 fatalf。
> - `.env` 文件**不要提交**到 Git，生产环境务必更换密钥。
> - 启动时 `export $(grep -v '^#' .env | xargs)` 可确保变量被正确 export。

