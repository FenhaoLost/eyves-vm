# 界面截图

## 重要说明

**这些截图不是 Eyves VM 的界面。**

截图取自**当前可运行的 cub-panel 代码库**（`https://github.com/wd780h/cub-panel`），启动时传入了 `-site "Eyves VM"`，因此页面标题栏显示 "Eyves VM"。它代表的是：

- ✅ Eyves VM 重构所依据的**演进基线**
- ✅ 已被识别的问题（见 [../01-bad-smells.md](../01-bad-smells.md)）的现场
- ❌ **不是** Eyves VM 新架构的产物 —— Eyves VM 的 Web UI 尚未实现

---

## 采集信息

| 项 | 值 |
|---|---|
| 采集时间 | 2026-09-23 |
| 采集方式 | 真实 HTTP 访问 + 登录态截图（管理员会话） |
| 站点版本 | cub-panel v0.1.0-generic |
| 登录账号 | `admin@eyves.local`（首个注册账号自动成为管理员） |
| 视口尺寸 | 1280 × 633（沙箱环境固定，无法调整） |
| 写操作 | **未执行任何写操作**（仅登录 + 导航 + 截图） |

---

## 文件清单

### 公开页面

| 文件 | URL | 页面标题 |
|---|---|---|
| [01-login.png](01-login.png) | `/login` | 登录 · Eyves VM |
| [02-register.png](02-register.png) | `/register` | 注册 · Eyves VM |

### 用户侧（登录态）

| 文件 | URL | 页面标题 |
|---|---|---|
| [03-user-dashboard.png](03-user-dashboard.png) | `/app` | 控制台 · Eyves VM |
| [15-user-recharge.png](15-user-recharge.png) | `/app/recharge` | 充值 · Eyves VM |

> `/app/orders` 与 `/app/instances` 返回 **HTTP 404**，原因为 cub-panel 未实现该路径（用户侧订单与实例列表分别位于其他路径）。对应截图内容为服务器纯文本 404 页，已删除，不纳入本文档。

### 管理后台（登录态）

| 文件 | URL | 页面标题 | 核心能力 |
|---|---|---|---|
| [04-admin-dashboard.png](04-admin-dashboard.png) | `/admin` | 管理后台 · Eyves VM | 节点/用户/实例统计、容量水位、审计日志 |
| [05-admin-nodes.png](05-admin-nodes.png) | `/admin/nodes` | 节点管理 · Eyves VM | 节点增删、探针、被控更新、HMAC 密钥 |
| [06-admin-plans.png](06-admin-plans.png) | `/admin/plans` | 套餐管理 · Eyves VM | CPU/内存/磁盘/流量/价格 |
| [07-admin-instances.png](07-admin-instances.png) | `/admin/instances` | 实例管理 · Eyves VM | 创建、删除、改 IP、端口转发、批量、延期、扩容、流量、迁移 |
| [08-admin-users.png](08-admin-users.png) | `/admin/users` | 用户管理 · Eyves VM | 状态、升降权、余额调整、重置密码 |
| [09-admin-orders.png](09-admin-orders.png) | `/admin/orders` | 充值订单 · Eyves VM | 订单查看、USDT 人工确认 |
| [10-admin-images.png](10-admin-images.png) | `/admin/images` | 镜像管理 · Eyves VM | 拉取/删除镜像、上传 ISO |
| [11-admin-codes.png](11-admin-codes.png) | `/admin/codes` | 激活码 · Eyves VM | 批量生成/删除兑换码 |
| [12-admin-settings.png](12-admin-settings.png) | `/admin/settings` | 网站设置 · Eyves VM | 站点名、公告、SMTP、支付、API Key |
| [13-admin-storage.png](13-admin-storage.png) | `/admin/storage` | 存储管理 · Eyves VM | 池容量查看与扩容 |

**合计 14 张**，全部为有效 PNG（1280×633），文件大小 31–81 KB。

---

## 功能状态对照

截图所示功能与 Eyves VM 规划状态的对应关系：

| 截图中的能力 | cub-panel | Eyves VM 目标 |
|---|---|---|
| 节点管理 | 手工登记 | 📐 集群模块：注册 + 健康检查 + 自动调度 + 维护模式 |
| 套餐管理 | 单周期定价 | 📐 catalog 模块：多周期定价 |
| 订单 | 仅充值订单 | 📐 统一 `orders`：购买/续费/升级/退款 |
| 实例管理 | Incus 单后端 | 📐 多 Hypervisor 抽象（Incus / KVM / OpenVZ） |
| 充值 | 支付宝/微信/USDT | ✅ 计费域已重构（幂等 + 部分退款） |
| 快照 | ✅ 用户自助（套餐配额） | 📐 快照链 + 容量计量 |
| VNC / 串口控制台 | ✅ VM 走 VNC，容器走串口 | 沿用 |
| 备份 | ⚠️ 仅迁移用 | 📐 定时 + 异地 + 保留策略 |
| IPv6 | ⚠️ 单地址分配 | 📐 子网（/64·/112·/120）+ 网关 |
| 反向 DNS | ❌ 无 | 📐 网络模块 |

详见 [../06-features.md](../06-features.md)。

---

## 重新采集方法

```bash
# 1) 启动面板
/opt/cub-panel/bin/cub-panel -listen 0.0.0.0:8080 \
  -db ./data/panel.db -site "Eyves VM"

# 2) 注册首个账号（自动成为管理员）
curl -X POST http://localhost:8080/register \
  -d 'email=admin@eyves.local&password=<强密码>&password2=<强密码>'

# 3) 打开浏览器登录后逐页截图
#    /login /register /app /app/recharge
#    /admin /admin/nodes /admin/plans /admin/instances /admin/users
#    /admin/orders /admin/images /admin/codes /admin/settings /admin/storage
```

> 采集时勿执行删除、停用、保存等写操作，以免污染演示环境数据。