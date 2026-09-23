# 第三部分：API 文档

> 机器可读规范：[`api/openapi.yaml`](../api/openapi.yaml)（OpenAPI 3.0.3，26 路径 / 36 操作 / 21 schema）
> 本文是人读版：解释约定、给示例、标注坑点。
>
> **实现状态警告**：本规范是**设计定稿**，`/v2/` 路由与 handler **尚未实现**。下面所有 cURL 示例描述的是**契约行为**，不要期望在当前代码上跑通。

---

## 3.1 概览

| 项 | 值 |
|---|---|
| 规范版本 | OpenAPI 3.0.3 |
| API 版本 | `2.0.0` |
| Base URL | `https://{host}/v2`，变量 `host` 默认 `panel.example.com` |
| 传输 | 仅 HTTPS（生产）；被控端自签证书需指纹钉扎 |
| 内容类型 | 请求与响应统一 `application/json` |
| 时间戳 | 一律 Unix 秒（`int64`），**非** ISO8601，与 SQLite 存量字段一致 |
| 金额 | 一律最小货币单位 `int64`（CNY 分 / USD cents），**禁止浮点** |
| 分组标签 | `instances` / `snapshots` / `backups` / `networks` / `billing` / `nodes` |

### 版本策略

```
/app/*    /api/*    /api/v1/*    /admin/*     ← 旧接口，原样保留，不改路径/请求体/响应体
/v2/*                                        ← 全部新接口
```

- 旧接口在同一进程内继续服务，通过适配器复用 `internal/billing` 等领域层。
- 兼容性以**请求/响应字节级不变**为准，允许内部实现替换。
- 下线窗口需显式公告，规范中未定义因此**不做承诺**。

---

## 3.2 认证

两种方案，可同时启用（`security` 为 OR 关系）：

| 方案 | 类型 | 载体 | 使用场景 |
|---|---|---|---|
| `sessionCookie` | `apiKey` in `cookie` | Cookie 名 `cub_session` | 浏览器会话，旧前端与 `/v2` 的 UI 调用 |
| `apiKey` | `http` `bearer` | `Authorization: Bearer <key>` | 服务端到服务端：计费系统回调、WHMCS 对接、运维脚本 |

```bash
# 方式一：API Key
curl -H "Authorization: Bearer eyv_live_xxxxxxxx" \
     https://panel.example.com/v2/billing/plans

# 方式二：会话（先登录拿 Cookie）
curl -c jar.txt -X POST https://panel.example.com/login \
     -d 'email=admin@example.com&password=***'
curl -b jar.txt https://panel.example.com/v2/billing/plans
```

未认证返回 `401` + `EYVES-203`。**注意**：`EYVES-203` 的语义是"账户不存在"，被复用为未认证标识，见 §3.13。

---

## 3.3 错误信封

所有非 2xx 响应体统一为：

```json
{
  "code": "EYVES-201",
  "message": "insufficient balance",
  "trace_id": "8f3c1a2e9b7d4f60",
  "details": { "required_cents": 1250, "balance_cents": 300 }
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `code` | ✅ | 稳定错误码，**唯一可用于程序判定的字段** |
| `message` | ✅ | 面向运维的英文描述，**禁止**用于字符串比较 |
| `trace_id` | ⬜ | 关联服务端日志；排障时先要这个 |
| `details` | ⬜ | 结构化补充信息，键随 `code` 变化 |

### 错误码全表

分段约定：`1xx` 入参/金额 · `2xx` 余额与账本 · `3xx` 订单与状态机 · `4xx` 外部依赖编排。
定义位置：[`internal/billing/errors.go`](../internal/billing/errors.go)。

| 错误码 | 常量名 | 含义 | 建议 HTTP |
|---|---|---|---|
| `EYVES-101` | `CodeInvalidAmount` | 金额格式非法 | 400 |
| `EYVES-102` | `CodeAmountOutOfRange` | 金额超出允许区间 | 400 / 422 |
| `EYVES-103` | `CodeNegativeAmount` | 需正数处传入 `<= 0` | 400 / 422 |
| `EYVES-104` | `CodeCurrencyMismatch` | 币种不一致 | 422 |
| `EYVES-105` | `CodeUnknownCurrency` | 未登记的币种 | 422 |
| `EYVES-201` | `CodeInsufficientBalance` | 余额不足 | 402 |
| `EYVES-202` | `CodeDuplicateRef` | 幂等 ref 重复（视为成功重放） | 409 |
| `EYVES-203` | `CodeAccountNotFound` | 账户不存在 / 未认证 | 401 / 404 |
| `EYVES-301` | `CodeOrderNotFound` | 订单不存在 | 404 |
| `EYVES-302` | `CodeIllegalTransition` | 非法状态流转 | 409 |
| `EYVES-303` | `CodeOrderNotRefundable` | 订单当前状态不可退款 | 409 |
| `EYVES-304` | `CodeRefundWindowExceeded` | 超出可退款时限 | 409 / 422 |
| `EYVES-401` | `CodeProvisionFailed` | 实例编排失败（已自动退款） | 502 |
| `EYVES-402` | `CodeInvalidRequest` | 领域层入参缺失 | 400 / 409 |

> `EYVES-104` / `105` 已在代码中登记，但**尚未**被 openapi.yaml 的 responses 引用，见 §3.13。

---

## 3.4 幂等约定

所有**产生资金变动或资源占用**的写操作都接受 `ref` 字段作为幂等键：

| 端点 | 幂等键字段 |
|---|---|
| `POST /instances` | `ref` |
| `POST /billing/recharges` | `ref`（必填） |
| `POST /billing/orders/{orderNo}/renew` | `ref` |
| `POST /networks/ipv6/subnets` | `ref` |

行为契约：

1. 首次请求：正常执行，返回资源。
2. 重复 `ref`：**不重复执行**，返回既有资源或 `409` + `EYVES-202`。
3. 充值端点为重放友好：返回 `200` 且 `duplicated: true`，**不重复入账**。

```json
// POST /v2/billing/recharges 的重复请求响应
{ "ok": true, "user_id": 42, "balance_cents": 5000, "duplicated": true }
```

> 上游计费系统（WHMCS）应把**自己的账单号**作为 `ref` 传入，保证跨系统可对账。

---

## 3.5 分页约定

列表型端点统一使用 `page` / `page_size`，响应统一信封：

| 参数 | 类型 | 默认 | 约束 |
|---|---|---|---|
| `page` | integer | `1` | `>= 1` |
| `page_size` | integer | `20` | `1..200` |

```json
{ "items": [ /* ... */ ], "total": 137 }
```

例外：`/instances/{id}/snapshots`、`/instances/{id}/backups`、`/instances/{id}/backups/schedules`、`/networks/ipv6/subnets`、`/billing/plans`、`/nodes` 直接返回数组（天然有界）。

---

## 3.6 端点速查

### 实例生命周期（instances）

| 方法 | 路径 | 说明 | 成功码 |
|---|---|---|---|
| GET | `/instances` | 列出实例（可按 `node_id` / `status` 过滤） | 200 |
| POST | `/instances` | **创建实例（下单 + 扣款 + 下发）** | 201 |
| GET | `/instances/{id}` | 实例详情 | 200 |
| DELETE | `/instances/{id}` | 删除实例（`keep_data=true` 保留数据盘） | 204 |
| POST | `/instances/{id}/actions` | 电源操作 `start\|stop\|restart`，支持 `force` | 202 |
| POST | `/instances/{id}/reinstall` | 重装系统（可指定 `root_password`） | 202 |
| POST | `/instances/{id}/rescue` | 进入救援模式（挂载 ISO，**仅 KVM**） | 202 |
| DELETE | `/instances/{id}/rescue` | 退出救援模式 | 204 |

### 快照与备份（snapshots / backups）

| 方法 | 路径 | 说明 | 成功码 |
|---|---|---|---|
| GET | `/instances/{id}/snapshots` | 列出快照 | 200 |
| POST | `/instances/{id}/snapshots` | 创建快照（`name` 需匹配 `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,31}$`） | 201 |
| DELETE | `/instances/{id}/snapshots/{name}` | 删除快照 | 204 |
| POST | `/instances/{id}/snapshots/{name}/restore` | 从快照还原 | 202 |
| GET | `/instances/{id}/backups` | 列出备份 | 200 |
| POST | `/instances/{id}/backups` | 创建备份（`note` / `compress`） | 202 |
| GET | `/instances/{id}/backups/schedules` | 列出定时备份任务 | 200 |
| POST | `/instances/{id}/backups/schedules` | 创建定时任务（五段 cron + `keep`） | 201 |

### 网络（networks）

| 方法 | 路径 | 说明 | 成功码 |
|---|---|---|---|
| GET | `/networks/ipv6/subnets` | 列出已分配 IPv6 子网 | 200 |
| POST | `/networks/ipv6/subnets` | 从节点池分配子网（`prefix_length ∈ {64, 112, 120}`） | 201 |
| DELETE | `/networks/ipv6/subnets/{id}` | 释放子网 | 204 |
| GET | `/networks/rdns?address=` | 查询 PTR 记录 | 200 |
| PUT | `/networks/rdns` | 设置 PTR 记录 | 200 |
| DELETE | `/networks/rdns?address=` | 删除 PTR 记录 | 204 |

### 计费（billing）

| 方法 | 路径 | 说明 | 成功码 |
|---|---|---|---|
| GET | `/billing/plans` | 列出套餐（含定价，`purchasable_only` 默认 true） | 200 |
| GET | `/billing/accounts/{userId}/balance` | 查询余额 | 200 |
| GET | `/billing/accounts/{userId}/transactions` | 查询账户流水（分页） | 200 |
| POST | `/billing/recharges` | **充值入账（服务端到服务端，幂等）** | 200 |
| GET | `/billing/orders` | 查询订单（按 `user_id`/`kind`/`status` 过滤） | 200 |
| GET | `/billing/orders/{orderNo}` | 订单详情 | 200 |
| POST | `/billing/orders/{orderNo}/renew` | 续费（`days` 1..1095） | 200 |
| POST | `/billing/orders/{orderNo}/refund` | 退款（支持部分退款） | 200 |

### 节点（nodes）

| 方法 | 路径 | 说明 | 成功码 |
|---|---|---|---|
| GET | `/nodes` | 列出节点 | 200 |
| POST | `/nodes` | 添加节点 | 201 |
| DELETE | `/nodes/{id}` | 移除节点（仍有实例时 409） | 204 |
| GET | `/nodes/{id}/health` | 健康检查（转发被控 `/v1/health`） | 200 |
| PUT | `/nodes/{id}/maintenance` | 切换维护模式（不参与新建调度） | 200 |
| POST | `/nodes/{id}/migrations` | 发起跨节点迁移（`live=true` 仅 KVM） | 202 |

**合计：26 路径 / 36 操作。**

---

## 3.7 生命周期示例

### 创建实例（下单 + 扣款 + 下发）

```bash
curl -X POST https://panel.example.com/v2/instances \
  -H "Authorization: Bearer $EYVES_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "plan_id": 7,
    "image": "debian/13",
    "label": "web-01",
    "days": 30,
    "ref": "WHMCS-INV-20260924-001",
    "node_id": 0
  }'
```

```json
{
  "id": 1042,
  "name": "vm-1042",
  "label": "web-01",
  "user_id": 42,
  "node_id": 3,
  "plan_id": 7,
  "hypervisor": "incus",
  "instance_type": "container",
  "image": "debian/13",
  "cpu": 2,
  "memory_mb": 2048,
  "disk_gb": 20,
  "status": "provisioning",
  "ipv4": null,
  "ipv6": "2001:db8:1:2::1042",
  "expires_at": 1761436800,
  "created_at": 1758672000
}
```

契约要点：

| 场景 | 行为 |
|---|---|
| 余额不足 | `402` + `EYVES-201`，`details` 含 `required_cents` / `balance_cents` |
| `ref` 重复 | `409` + `EYVES-202`，**不重复扣款** |
| 扣款成功但下发失败 | 自动全额退款，返回 `EYVES-401`；订单进入 `failed` 且 `refunded == amount` |
| `node_id=0` | 自动调度到可用节点（排除 `maintenance=true` 与已满节点） |

### 电源操作

```bash
curl -X POST https://panel.example.com/v2/instances/1042/actions \
  -H "Authorization: Bearer $EYVES_TOKEN" \
  -d '{"action":"stop","force":false}'
```

```json
{ "operation_id": "op_9f3c1a2e", "status": "queued" }
```

异步返回。轮询约定：`status ∈ {queued, running, done, failed}`，`failed` 时附带 `error` 对象。

### 重装 / 救援

```bash
# 重装
curl -X POST .../v2/instances/1042/reinstall \
  -d '{"image":"ubuntu/24.04","root_password":"'"$NEW_PW"'"}'

# 进入救援（仅 KVM；Incus 容器返回 403 + EYVES-302）
curl -X POST .../v2/instances/1042/rescue \
  -d '{"iso":"systemrescue-11.00-amd64.iso","duration_minutes":60}'

# 退出救援
curl -X DELETE .../v2/instances/1042/rescue
```

---

## 3.8 快照与备份示例

```bash
# 创建快照
curl -X POST .../v2/instances/1042/snapshots \
  -d '{"name":"before-upgrade"}'

# 还原
curl -X POST .../v2/instances/1042/snapshots/before-upgrade/restore

# 创建定时备份：每天 03:00，保留 7 份
curl -X POST .../v2/instances/1042/backups/schedules \
  -d '{"cron":"0 3 * * *","keep":7,"enabled":true}'
```

> `keep` 上限 60。`cron` 为五段式表达式。超期备份的清理策略见 [05-risks.md](05-risks.md)。

---

## 3.9 网络示例

```bash
# 分配一个 /64 子网给实例 1042
curl -X POST .../v2/networks/ipv6/subnets \
  -d '{"node_id":3,"prefix_length":64,"instance_id":1042,"ref":"subnet-for-1042"}'
```

```json
{
  "id": 88,
  "node_id": 3,
  "instance_id": 1042,
  "cidr": "2001:db8:1:2::/64",
  "gateway": "2001:db8:1:2::1",
  "allocated_at": 1758672000
}
```

```bash
# 设置反向 DNS
curl -X PUT .../v2/networks/rdns \
  -d '{"address":"2001:db8:1:2::1042","hostname":"vm-1042.example.com","ttl":300}'
```

`prefix_length` 支持 `64` / `112` / `120`。释放子网时若 `instance_id` 仍绑定则返回 `409` + `EYVES-302`。

---

## 3.10 计费示例

```bash
# 查询余额
curl -H "Authorization: Bearer $EYVES_TOKEN" \
     .../v2/billing/accounts/42/balance
# => {"user_id":42,"email":"u@example.com","balance":{"amount_cents":5000,"currency":"CNY"}}

# 服务端到服务端充值（幂等）
curl -X POST .../v2/billing/recharges \
  -d '{"email":"u@example.com","amount_cents":5000,"ref":"EPAY-202609240001","note":"支付宝"}'
# => {"ok":true,"user_id":42,"balance_cents":5000,"duplicated":false}

# 续费 30 天
curl -X POST .../v2/billing/orders/ORD-20260924-001/renew \
  -d '{"days":30,"ref":"RENEW-20261024-001"}'

# 部分退款
curl -X POST .../v2/billing/orders/ORD-20260924-001/refund \
  -d '{"amount_cents":300,"reason":"服务中断补偿"}'
```

### 订单状态机

```
                    ┌──────────┐
                    │ pending  │  ← 订单创建，未支付
                    └────┬─────┘
          ┌──────────────┼──────────────┬──────────────┐
          ▼              ▼              ▼              ▼
     ┌────────┐    ┌───────────┐  ┌────────┐    ┌─────────┐
     │  paid  │    │ cancelled │  │ expired│    │ failed  │
     └────┬───┘    └───────────┘  └────────┘    └─────────┘
          ▼
   ┌──────────────┐
   │ provisioned  │
   └──────┬───────┘
          ▼
      ┌────────┐      ┌────────────┐      ┌───────────┐
      │ active │─────▶│ refunding  │─────▶│ refunded  │
      └────────┘      └────────────┘      └───────────┘
                             │
                             ▼
                   ┌────────────────────┐
                   │ partially_refunded │
                   └────────────────────┘
```

| 状态 | 含义 | 可退款 |
|---|---|---|
| `pending` | 已创建，等待支付 | ❌ |
| `paid` | 已扣款 | ✅ |
| `provisioned` | 资源已下发 | ✅ |
| `active` | 服务中 | ✅ |
| `failed` | 下发失败（已自动退款） | ❌ |
| `cancelled` / `expired` | 用户取消 / 超时未付 | ❌ |
| `refunding` | 退款处理中 | ❌ |
| `refunded` | 已全额退款 | ❌ |
| `partially_refunded` | 已部分退款，**仍可继续退** | ✅ |

非法流转返回 `409` + `EYVES-302`；超出 `refund_window_days` 返回 `EYVES-304`。

---

## 3.11 节点管理示例

```bash
# 添加节点
curl -X POST .../v2/nodes \
  -d '{"name":"hk-01","region":"HK","endpoint":"https://10.0.0.5:8788",
       "hypervisor":"incus","storage_pool":"default","max_instances":200}'

# 健康检查
curl -H "Authorization: Bearer $EYVES_TOKEN" .../v2/nodes/3/health
# => {"agent":"0.1.0","kvm_ready":true,"hypervisor_version":"6.0.2", ...}

# 进入维护模式
curl -X PUT .../v2/nodes/3/maintenance \
  -d '{"enabled":true,"reason":"内核升级"}'

# 跨节点迁移
curl -X POST .../v2/nodes/3/migrations \
  -d '{"instance_ids":[1042,1043],"target_node_id":5,"live":false}'
```

| 约束 | 说明 |
|---|---|
| `endpoint` | 形如 `https://host:port`，**不得硬编码**在代码中 |
| HMAC 密钥 | 存于 `nodes.secret`，**不通过 API 读写**，也不出现在响应里 |
| 移除节点 | 节点仍有实例时返回 `409` + `EYVES-402` |
| 维护模式 | 不参与新建调度，但已运行的实例不受影响 |
| `live=true` | 在线迁移，**仅 KVM** 支持；Incus 容器传 `true` 返回 `409` |

---

## 3.12 旧接口兼容矩阵

| 旧路径 | 新路径 | 兼容状态 | 说明 |
|---|---|---|---|
| `POST /app/deploy` | `POST /v2/instances` | ✅ 语义等价 | 新接口增加 `ref` 幂等键 |
| `POST /api/v1/recharge` | `POST /v2/billing/recharges` | ✅ 响应体逐字节相同 | `{"ok","user_id","balance_cents","duplicated"}` |
| `GET /app/*`（页面） | — | ✅ 保留 | 服务端渲染，不迁移 |
| `POST /admin/*`（后台） | 部分对应 `/v2/nodes` 等 | ✅ 保留 | 内部改走领域层，对外不变 |

**不变量**：旧接口的路径、请求体、响应体在本次重构中**零变更**；新增能力只出现在 `/v2/` 下。

---

## 3.13 已知问题（需在实现前裁决）

| # | 问题 | 影响 | 建议 |
|---|---|---|---|
| A1 | `EYVES-203` 语义为"账户不存在"，却被复用为 `401 未认证` | 调用方无法区分「未登录」与「账号被删」 | 新增 `EYVES-204` 专表未认证 |
| A2 | `EYVES-104` / `105` 已在代码登记，但 openapi 的 responses 未引用 | 文档与实现不完全对齐 | 在 `Unprocessable` 响应的 description 中补上 |
| A3 | 36 个操作**全部缺少 `operationId`** | 无法自动生成 SDK / 客户端代码 | 按 `listInstances` / `createInstance` 风格补齐 |
| A4 | 多个端点无 `security` 覆盖差异声明（如 `/billing/recharges` 应仅允许 `apiKey`） | 权限边界模糊 | 按端点显式声明 `security` |
| A5 | `Operation.status` 是字符串枚举，无对应轮询端点定义 | 异步操作无法查询进度 | 增加 `GET /v2/operations/{operationId}` |
| A6 | 未定义限流响应（`429`）与 `Retry-After` | 客户端无退避依据 | 增加 `429` 响应与限流头 |
| A7 | 未定义 Webhook / 事件回调契约 | WHMCS 只能轮询 | 规划 `/v2/webhooks`（P2） |