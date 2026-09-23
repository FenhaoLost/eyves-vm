# 第八部分：部署文档

> **读前必知**：本仓库（Eyves VM）**当前无法部署** —— 没有 `cmd/` 入口、没有 HTTP 路由、没有 Web 层。
> 本文的部署流程适用于**现状系统 cub-panel**（已构建、已实测运行），外加 Eyves VM 迁移脚本的使用方式。
> Eyves VM 开始产出可执行文件后，本文会扩展为双系统部署指南。

---

## 8.0 部署对象与前提

| 部署对象 | 可部署？ | 位置 |
|---|---|---|
| `cub-panel` 主控 | ✅ | cub-panel 仓库 `cmd/panel` |
| `cub-agent` 被控 | ✅ | cub-panel 仓库 `cmd/agent` |
| Eyves VM 主控 | ❌ 无入口 | — |
| Eyves VM 数据库迁移脚本 | ✅ 可执行 | 本仓库 [migrations/](../migrations) |

**硬性路径约束**（由 cub-panel 的 `deploy/install-panel.sh` 等脚本强制）：

```
生产部署根目录 仅允许 /opt/cub-panel
禁止：/usr/local、/box/env、源码树自身、其他任意路径
```

---

## 8.1 架构与端口

```
        ┌──────────────────────────────────────────────┐
        │  主控机 (Panel)                              │
        │  /opt/cub-panel/bin/cub-panel                │
        │  监听 :8080  ← 浏览器 / WHMCS / API 客户端    │
        │  SQLite: /opt/cub-panel/data/panel.db        │
        └───────────────┬──────────────────────────────┘
                        │ HTTPS + HMAC 签名
                        │ 端口 8788（自签证书 + 指纹钉扎）
        ┌───────────────▼──────────────────────────────┐
        │  宿主机 (Node)                               │
        │  /opt/cub-panel/bin/cub-agent                │
        │      │ unix socket                           │
        │      ▼                                       │
        │  /var/lib/incus/unix.socket  →  Incus 守护进程 │
        └──────────────────────────────────────────────┘
```

| 端口 | 组件 | 默认值 | 可配置项 |
|---|---|---|---|
| 8080 | cub-panel | `0.0.0.0:8080` | `CUB_PANEL_LISTEN` |
| 8788 | cub-agent | `0.0.0.0:8788` | `CUB_AGENT_LISTEN` |

> ⚠️ **被控端口 8788 不要暴露公网**。主控与被控之间应走内网或 WireGuard。

---

## 8.2 构建

### 8.2.1 通用版（一个二进制同时支持 AMD 与 Intel）

x86-64 二进制本身即同时支持两家 CPU，真正的兼容性风险来自 `GOAMD64` 微架构等级：

| 等级 | 指令集要求 | 兼容性 |
|---|---|---|
| **v1（推荐，通用版）** | 纯基线 x86-64 | ✅ 2003 年后所有 AMD/Intel CPU，含 Atom、早期至强、虚拟机默认 CPU 模型 |
| v2 | SSE4.2 / POPCNT | ⚠️ 部分老 Atom 与虚拟机 CPU 模型会 `SIGILL` |
| v3 | AVX2 | ❌ AMD 推土机、Intel 四代酷睿之前全部崩溃 |

**通用版构建命令**：

```bash
cd src

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 \
  go build -trimpath \
  -ldflags="-s -w -X cubpanel/internal/shared.Version=v0.1.0-generic" \
  -o ../bin/cub-panel ./cmd/panel

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 \
  go build -trimpath \
  -ldflags="-s -w -X cubpanel/internal/shared.Version=v0.1.0-generic" \
  -o ../bin/cub-agent ./cmd/agent
```

或用仓库自带脚本（`build.sh` 默认继承宿主 `GOAMD64`，跨架构时自动加后缀）：

```bash
VERSION=v0.1.0-generic ./deploy/build.sh
ls -lh bin/          # bin/cub-panel  bin/cub-agent
```

### 8.2.2 验证产物（发布前必做）

```bash
for f in bin/cub-panel bin/cub-agent; do
  echo "== $f =="
  readelf -h   "$f" | grep -E 'Class|Machine'          # 期望：ELF64 / Advanced Micro Devices X86-64
  readelf -d   "$f" | grep -c NEEDED                   # 期望：0（无动态依赖）
  go version -m "$f" | grep -E 'GOARCH|GOAMD64|CGO'    # 期望：amd64 / v1 / CGO_ENABLED=0
done
```

实测结果（v0.1.0-generic）：

| 校验项 | cub-panel | cub-agent |
|---|---|---|
| ELF 类型 | `ELF64` / `EM_X86_64` | `ELF64` / `EM_X86_64` |
| 构建参数 | `GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0` | 同左 |
| 动态依赖 `NEEDED` 数量 | **0** | **0** |
| ISA property 段 | 无 | 无 |
| 体积 | 18 MB | 7.4 MB |

> `NEEDED=0` + `statically linked` 意味着**不依赖 glibc**，可在 Alpine(musl)、Debian、Ubuntu、Rocky 之间自由迁移。

### 8.2.3 其他架构

```bash
GOARCH=arm64   ./deploy/build.sh    # → bin/cub-panel-arm64   作 host 直跑
GOARCH=riscv64 ./deploy/build.sh    # → bin/cub-panel-riscv64
```

`install-panel.sh` 会自动按 `uname -m` 挑选带后缀的二进制。

---

## 8.3 方式一：发布包 + 安装脚本（推荐）

### 8.3.1 发布包结构

```
cub-panel-v0.1.0-generic-linux-amd64/
├── SHA256SUMS
├── bin/
│   ├── cub-panel                    主控（18 MB）
│   └── cub-agent                    被控（7.4 MB）
├── deploy/
│   ├── install-panel.sh             主控安装（自动识别 systemd / OpenRC）
│   ├── install-agent.sh             被控安装（自动生成共享密钥）
│   ├── install.sh                   一体化安装
│   ├── setup-lxd-node.sh            宿主机初始化（装 Incus、存储池、NAT 桥、可选 IPv6）
│   ├── update-binaries.sh           升级二进制（自动备份 + 重启）
│   ├── enforce-paths.sh             部署路径合规检查
│   ├── build.sh                     从源码重建
│   ├── systemd/cub-{panel,agent}.service
│   ├── openrc/cub-{panel,agent}
│   └── docker/{Dockerfile,docker-compose.yml}
└── docs/
    ├── GUIDE.md                     使用指南
    ├── GUIDE.en.md
    └── OPS-PATHS.md                 路径规范
```

### 8.3.2 主控机部署

```bash
tar -xzf cub-panel-v0.1.0-generic-linux-amd64.tar.gz
cd cub-panel-v0.1.0-generic-linux-amd64

# 校验完整性
sha256sum -c SHA256SUMS

# 安装（默认装到 /opt/cub-panel，非交互时自动选 SQLite）
sudo sh deploy/install-panel.sh
```

交互式选择数据库（脚本**只连接、不安装**数据库服务）：

```
选择数据库 / choose the database backend:
  1) SQLite      默认，零依赖，单文件（中小规模够用）
  2) PostgreSQL  对接已有实例（大规模 / 多实例推荐）
  3) MySQL       对接已有实例
请选择 [1]:
```

非交互部署：

```bash
sudo CUB_PANEL_DB_DRIVER=sqlite sh deploy/install-panel.sh
# 或对接已有 PostgreSQL
sudo CUB_PANEL_DB_DRIVER=postgres \
     CUB_PANEL_DB_DSN='postgres://user:pass@127.0.0.1:5432/cub?sslmode=disable' \
     sh deploy/install-panel.sh
```

脚本行为：

| 步骤 | 说明 |
|---|---|
| 路径校验 | 拒绝 `/usr/local`、`/box/env`、源码树；只允许 `/opt/cub-panel` |
| 安装二进制 | `install -m 0755 bin/cub-panel → /opt/cub-panel/bin/` |
| 建数据目录 | `mkdir -p /opt/cub-panel/data && chmod 0750` |
| 写配置 | 首次生成 `/opt/cub-panel/cub-panel.env`（`chmod 0640`）；已存在则**保留不动** |
| 注册服务 | 有 OpenRC 用 OpenRC，否则用 systemd，并立即启动 |

### 8.3.3 宿主机（节点）部署

```bash
# 1) 先初始化宿主机：安装 Incus、创建存储池与 NAT 桥
sudo sh deploy/setup-lxd-node.sh

# 2) 再装被控
sudo sh deploy/install-agent.sh
```

`setup-lxd-node.sh` 可重复执行，关键环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `NAT_BRIDGE` | `lxdbr0` | NAT 桥名 |
| `NAT_SUBNET` | 交互询问 | 桥地址，默认 `10.180.0.1/24` |

`install-agent.sh` 会自动：

- 加载内核模块（`tun`、`fuse` 等）并确保 `/dev/net/tun` 存在
- 首次运行时**生成随机共享密钥**写入 `/opt/cub-panel/cub-agent.env`
- 注册 systemd/OpenRC 服务并启动

**把被控的密钥与地址填进主控后台**：

```
管理后台 → 节点管理 → 添加节点
  Endpoint: https://<节点内网IP>:8788
  Secret:   <被控 /opt/cub-panel/cub-agent.env 里的 CUB_AGENT_SECRET>
```

---

## 8.4 方式二：systemd 手工部署

适合已有配置管理体系、不跑安装脚本的场景。

```bash
sudo mkdir -p /opt/cub-panel/{bin,data}
sudo install -m 0755 bin/cub-panel /opt/cub-panel/bin/cub-panel
sudo chmod 0750 /opt/cub-panel/data

sudo tee /opt/cub-panel/cub-panel.env >/dev/null <<'EOF'
CUB_PANEL_LISTEN=127.0.0.1:8080
CUB_PANEL_DB=/opt/cub-panel/data/panel.db
CUB_PANEL_DB_DRIVER=sqlite
CUB_PANEL_SITE="Eyves VM"
CUB_PANEL_SECURE_COOKIES=1
CUB_PANEL_TRUST_PROXY=1
CUB_PANEL_ALLOW_SIGNUP=0
EOF
sudo chmod 0640 /opt/cub-panel/cub-panel.env

sudo install -m 0644 deploy/systemd/cub-panel.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now cub-panel
sudo systemctl status cub-panel
```

附带的 unit 已内置沙箱加固，无需额外调优：

```ini
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/cub-panel/data
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallArchitectures=native
```

> 注意 `ReadWritePaths=/opt/cub-panel/data` —— 数据库**必须**放在这个目录下，否则会被 `ProtectSystem=strict` 拦截。

---

## 8.5 方式三：OpenRC（Alpine）

安装脚本会自动识别，手工步骤如下：

```bash
doas install -m 0755 deploy/openrc/cub-panel /etc/init.d/cub-panel
doas rc-update add cub-panel default
doas rc-service cub-panel start
doas rc-service cub-panel status
```

被控同理替换为 `cub-agent`。

> 静态二进制（`NEEDED=0`）在 Alpine 的 musl 环境下无需额外兼容层。

---

## 8.6 方式四：Docker

仓库自带 `deploy/docker/docker-compose.yml`，使用 **host network** 模式：

```bash
cd deploy/docker
docker compose up -d --build
docker compose logs -f
```

设计要点：

- `network_mode: host` —— 主控可直接访问节点的 `8788` 端口，无需端口映射；client IP 与裸机部署行为一致
- 数据卷 `cub-data:/data`，对应 `CUB_PANEL_DB=/data/panel.db`
- 环境变量在 `docker-compose.yml` 的 `environment:` 段配置（改端口直接改 `CUB_PANEL_LISTEN`）

```yaml
environment:
  CUB_PANEL_LISTEN: "0.0.0.0:8080"
  CUB_PANEL_SITE: "Eyves VM"
  CUB_PANEL_ALLOW_SIGNUP: "1"
  CUB_PANEL_SECURE_COOKIES: "0"      # 前面挂了 TLS 反代时改 1
  CUB_PANEL_TRUST_PROXY: "0"         # 反代可信时改 1
  CUB_PANEL_DB_DRIVER: "sqlite"
  CUB_PANEL_DB: "/data/panel.db"
```

> 主控可容器化，但**被控不建议容器化** —— 它需要直接访问宿主机的 Incus Unix Socket 与内核模块。

---

## 8.7 反向代理 + TLS

生产必须让面板走 HTTPS，否则会话 Cookie 与 API Key 全程明文。

### Nginx

```nginx
server {
    listen 443 ssl http2;
    server_name panel.example.com;

    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    # WebSocket（终端 / 控制台功能需要）
    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Upgrade           $http_upgrade;
        proxy_set_header   Connection        "upgrade";
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 300s;
    }
}

server {
    listen 80;
    server_name panel.example.com;
    return 301 https://$host$request_uri;
}
```

### Caddy（自动证书）

```
panel.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

挂上反代后**必须**同步改配置：

```bash
CUB_PANEL_SECURE_COOKIES=1     # 否则 Cookie 无 Secure 标志
CUB_PANEL_TRUST_PROXY=1        # 否则限流与审计日志记到反代 IP
CUB_PANEL_LISTEN=127.0.0.1:8080  # 收回公网监听
```

---

## 8.8 配置项详解

### 8.8.1 主控 cub-panel

配置文件：`/opt/cub-panel/cub-panel.env`（也可用同名环境变量覆盖）

| 变量 | 默认 | 说明 |
|---|---|---|
| `CUB_PANEL_LISTEN` | `0.0.0.0:8080` | 监听地址。挂了反代请改 `127.0.0.1:8080` |
| `CUB_PANEL_DB` | `/opt/cub-panel/data/panel.db` | SQLite 文件路径 |
| `CUB_PANEL_DB_DRIVER` | `sqlite` | `sqlite` \| `postgres` \| `mysql` |
| `CUB_PANEL_DB_DSN` | 空 | 仅 postgres/mysql 使用 |
| `CUB_PANEL_SITE` | `Cub Panel` | 界面显示的站点名 |
| `CUB_PANEL_SECURE_COOKIES` | `0` | HTTPS 下**必须**置 `1`，否则登录异常 |
| `CUB_PANEL_TRUST_PROXY` | `0` | 仅在受信反代之后置 `1`。**误开会导致 IP 伪造** |
| `CUB_PANEL_ALLOW_SIGNUP` | `1` | 公开注册开关。管理员建好后建议置 `0` |

对应命令行参数：`-listen` `-db` `-db-driver` `-db-dsn` `-site` `-secure-cookies` `-trust-proxy` `-allow-signup`

### 8.8.2 被控 cub-agent

配置文件：`/opt/cub-panel/cub-agent.env`

| 变量 | 默认 | 说明 |
|---|---|---|
| `CUB_AGENT_LISTEN` | `0.0.0.0:8788` | 建议改内网地址 |
| `CUB_AGENT_SOCKET` | `/var/lib/incus/unix.socket` | Incus/LXD Unix Socket |
| `CUB_AGENT_POOL` | `cub` | 存储池名 |
| `CUB_AGENT_IMAGE_SERVER` | `https://images.linuxcontainers.org` | simplestreams 镜像源 |
| `CUB_AGENT_ISO_DIR` | `/var/lib/cub-panel/isos` | ISO 存放目录 |
| `CUB_AGENT_TLS` | `1` | 是否启用 HTTPS（自签证书） |
| `CUB_AGENT_TLS_CERT` | `agent-cert.pem` | 证书路径，缺失时自动生成 |
| `CUB_AGENT_TLS_KEY` | `agent-key.pem` | 私钥路径，缺失时自动生成 |
| `CUB_AGENT_SECRET` | 安装时自动生成 | HMAC 共享密钥。**不要手工改动**，改了要同步主控 |
| `CUB_AGENT_VERBOSE` | 空 | 非空即开启详细日志 |

### 8.8.3 Eyves VM 目标配置（📐 尚未生效）

本仓库提供 [config.example.yaml](../config.example.yaml) 作为**目标形态**的配置样例：

- 环境变量前缀 `EYVES_`，层级用下划线连接：`EYVES_PANEL_LISTEN`、`EYVES_AGENT_ENDPOINT`
- **禁止**将密钥写入文件，必须走环境变量注入：
  - `EYVES_NETWORK_RDNS_TSIG_SECRET`
  - `EYVES_PAYMENT_EPAY_KEY`
  - 节点 HMAC 密钥（存 `nodes.secret` 表）
- 与现状的映射关系：

| 现状 cub-panel | Eyves VM 目标 |
|---|---|
| `CUB_PANEL_LISTEN` | `panel.listen` / `EYVES_PANEL_LISTEN` |
| `CUB_PANEL_DB` | `database.dsn` |
| `CUB_PANEL_DB_DRIVER` | `database.driver` |
| `CUB_PANEL_SECURE_COOKIES` | `panel.secure_cookies` |
| `CUB_PANEL_TRUST_PROXY` | `panel.trust_proxy` |
| `CUB_PANEL_ALLOW_SIGNUP` | `panel.allow_signup` |
| `CUB_PANEL_SITE` | `panel.site_name` |
| `CUB_AGENT_ENDPOINT` | 存库 `nodes.endpoint`（不在配置文件里） |

---

## 8.9 升级与回滚

### 8.9.1 二进制升级（自动备份）

```bash
cd cub-panel-v0.1.0-generic-linux-amd64
sudo sh deploy/update-binaries.sh
```

脚本行为：源码树内构建 → 备份现有二进制为 `*.bak` → 安装到 `/opt/cub-panel` → 重启服务 → 打印 `sha256`。

```bash
sudo sh deploy/update-binaries.sh --no-restart   # 只装不重启
```

### 8.9.2 手工回滚

```bash
sudo systemctl stop cub-panel
sudo cp /opt/cub-panel/bin/cub-panel.bak /opt/cub-panel/bin/cub-panel
sudo systemctl start cub-panel
sudo systemctl status cub-panel
```

> 数据库结构若已随升级变更，**必须先执行 8.10 的 down 迁移**再回滚二进制。

---

## 8.10 数据库迁移（Eyves VM）

> 迁移脚本位于本仓库 [migrations/](../migrations)，作用于 `0001`：`recharge_orders → orders`。
> 详细字段映射见 [04-migration.md](04-migration.md)。

### 8.10.1 执行迁移

```bash
cd migrations

# 建议先停主控，消除并发写（避免撕裂备份）
sudo systemctl stop cub-panel

# 一条命令：备份 → 迁移 → 10 项校验
sh migrate-up.sh /opt/cub-panel/data/panel.db
```

脚本内部：

1. 自动 `cp` 出备份：`<db>.pre-0001.<时间戳>.bak`
2. 执行 `0001_billing_orders.up.sql`
3. 执行 `verify.sql` 做 10 项一致性校验
4. **校验非 0 退出即视为失败**（实测：篡改 `ref` 前缀 → `exit 5`）

预期输出（实测 10/10 通过）：

```
[1/10] PASS  orders 行数 == recharge_orders 行数
[2/10] PASS  order_no 集合完全一致
...
[10/10] PASS  幂等键 ref 前缀全部为 'order:'
```

### 8.10.2 回滚（一条命令）

```bash
sh rollback.sh /opt/cub-panel/data/panel.db
```

行为：优先从最近的 `.pre-0001.<ts>.bak` 恢复；无备份时执行 `0001_billing_orders.down.sql` 做结构回退。

### 8.10.3 迁移前检查清单

```bash
# 1) WAL 收敛，确保拷到的是完整库
sqlite3 /opt/cub-panel/data/panel.db 'PRAGMA wal_checkpoint(TRUNCATE);'

# 2) 完整性自检
sqlite3 /opt/cub-panel/data/panel.db 'PRAGMA integrity_check;'   # 期望 ok

# 3) 记录迁移前基线（回滚后用于比对）
sqlite3 /opt/cub-panel/data/panel.db \
  "SELECT 'users', COUNT(*) FROM users
   UNION ALL SELECT 'recharge_orders', COUNT(*) FROM recharge_orders;"

# 4) 磁盘余量（备份 ≈ 原库大小）
df -h /opt/cub-panel/data
```

---

## 8.11 排障

| 现象 | 可能原因 | 处理 |
|---|---|---|
| 登录成功但立刻跳回登录页 | `CUB_PANEL_SECURE_COOKIES=1` 但站点跑在 HTTP | 改 `0`，或给站点配 HTTPS |
| 限流误判、审计日志全是同一个 IP | 反代后未开 `CUB_PANEL_TRUST_PROXY=1` | 置 `1`（确认反代可信） |
| 节点探针失败 | 被控未启动 / 密钥不匹配 / 防火墙挡 8788 | `curl -k https://<node>:8788/v1/health` 逐层排查；比对两侧 `CUB_AGENT_SECRET` |
| 面板启动即退出（systemd 部署） | 数据库路径不在 `ReadWritePaths` 内 | 数据库放在 `/opt/cub-panel/data` 下 |
| `database is locked` | SQLite 单写入者被并发压满 | 换 PostgreSQL：`CUB_PANEL_DB_DRIVER=postgres` |
| 安装脚本拒绝路径 | 部署根目录不是 `/opt/cub-panel` | 用默认路径，或改脚本前先读 `docs/OPS-PATHS.md` |
| 二进制在目标机 `Illegal instruction` | 用了 `GOAMD64=v2/v3` 构建 | 改用 `GOAMD64=v1` 重新构建（见 §8.2.1） |
| 抓不到日志现场 | 未开详细日志 | 被控设 `CUB_AGENT_VERBOSE=1`，重启服务 |

常用排查命令：

```bash
# 服务状态与日志
sudo systemctl status cub-panel
sudo journalctl -u cub-panel -n 200 --no-pager
sudo journalctl -u cub-agent  -n 200 --no-pager

# 端口与进程
ss -lntp | grep -E '8080|8788'
ps -eo pid,etimes,args | grep -E 'cub-panel|cub-agent' | grep -v grep

# 健康检查
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/login    # 期望 200
curl -sk https://<node>:8788/v1/health | head -c 400

# 数据库直查
sqlite3 /opt/cub-panel/data/panel.db 'SELECT id,email,is_admin FROM users;'
sqlite3 /opt/cub-panel/data/panel.db 'PRAGMA integrity_check;'
```

---

## 8.12 上线前安全加固清单

- [ ] 站点已走 HTTPS（Nginx / Caddy / 其他），且已设 `CUB_PANEL_SECURE_COOKIES=1`
- [ ] `CUB_PANEL_ALLOW_SIGNUP=0`（管理员账号已建好）
- [ ] `CUB_PANEL_LISTEN=127.0.0.1:8080`（回环，公网只暴露反代）
- [ ] 被控 8788 端口未对公网开放（内网 / WireGuard）
- [ ] `CUB_PANEL_TRUST_PROXY` 仅在反代可信时为 `1`，否则必须为 `0`
- [ ] `/opt/cub-panel/cub-panel.env` 与 `cub-agent.env` 权限为 `0640` 且属主为服务用户
- [ ] `/opt/cub-panel/data` 权限 `0750`，且已纳入备份计划
- [ ] 已配置异地备份（SQLite 用 `sqlite3 .backup`，不要直接 `cp` 正在写入的库）
- [ ] 已在 `/admin/settings` 配置好支付网关，且密钥未出现在日志中
- [ ] 已确认无默认口令：管理员密码非弱口令，`admin@eyves.local` 之类的临时账号已停用

**备份命令（在线安全备份）**：

```bash
sqlite3 /opt/cub-panel/data/panel.db \
  ".backup '/backup/panel-$(date +%Y%m%d-%H%M%S).db'"
```

---

## 8.13 Eyves VM 从设计态转为可部署的缺口

按依赖顺序，缺以下任一环节都无法部署 Eyves VM 本体：

| # | 缺口 | 影响 |
|---|---|---|
| 1 | `cmd/eyves-panel` / `cmd/eyves-agent` 入口 | 无进程可启动 |
| 2 | `internal/app` 依赖装配 | 无法组装 domain 与适配器 |
| 3 | SQLite 适配器（`Ledger` / `OrderRepo`） | 领域层无持久化 |
| 4 | `/v2` HTTP 路由与错误映射 | 26 路径 / 36 操作不可访问 |
| 5 | Hypervisor 抽象层实现 | 无法创建实例 |
| 6 | Web UI（模板或 SPA） | 无界面 |

补齐顺序与风险见 [05-risks.md](05-risks.md) 与 [06-features.md §6.6](06-features.md)。