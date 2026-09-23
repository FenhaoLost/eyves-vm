# 第四部分：数据迁移方案

迁移引擎：`migrations/` 下的 `NNNN_*.up.sql` / `.down.sql` + `migrate-up.sh` / `rollback.sh` / `verify.sql`。
版本记录表 `schema_migrations(version, applied_at)`，重复执行自动跳过（幂等）。

## 4.1 迁移清单与状态

| 版本 | 范围 | up | down | 校验 | 状态 |
|---|---|---|---|---|---|
| `0001` | 充值订单 → 统一订单表 + 订单事件表 | ✅ 76 行 | ✅ 17 行 | ✅ 10 项 | **已完成并实测通过** |
| `0002` | 套餐定价多周期多币种 | ❌ | ❌ | ❌ | 待开发（R1-1 门禁） |
| `0003` | 单地址字段 → `instance_nics` + 地址表 | ❌ | ❌ | ❌ | 待开发（R1-5 门禁） |
| `0004` | 兑换码 → 订单化 | ❌ | ❌ | ❌ | 待开发（R1-2 门禁） |
| `0005` | 账本双分录（`accounts` + `ledger_entries`） | ❌ | ❌ | ❌ | 待开发（阻塞于 F-3） |

> 本文件对 `0001` 给出完整映射；`0002`~`0005` 只给出映射设计，**不提供可执行脚本**，因为上游代码（`catalog` / `network` / 账本适配器）尚未实现，现在写 SQL 属于无消费方的臆测。

## 4.2 字段映射表（`0001`：`recharge_orders` → `orders`）

| 旧列（`recharge_orders`，见 `src/internal/store/schema.sql:184-193`） | 新列（`orders`） | 转换规则 | 备注 |
|---|---|---|---|
| `id` | — | **不迁移** | 新表自增主键独立；旧 ID 通过 `order_no` 可回查 |
| `order_no` | `order_no` | 原值 | `UNIQUE`，作为站内单号 |
| `user_id` | `user_id` | 原值 | 外键 `REFERENCES users(id) ON DELETE CASCADE` |
| — | `kind` | 常量 `'recharge'` | 旧表只有充值，故整表映射为 `recharge` |
| — | `plan_id` | `0` | 充值无套餐 |
| — | `instance_id` | `0` | 充值无实例 |
| `amount_cents` | `amount_cents` | 原值 | 正数，最小单位 |
| — | `refunded_cents` | `0` | 历史充值未退过款（旧系统无退款单） |
| — | `currency` | 常量 `'CNY'` | 旧库无币种字段，全部按人民币 |
| `status` | `status` | `paid→paid`；`expired→expired`；其余→`pending` | 旧枚举只有 3 值，映射为闭集子集 |
| — | `ref` | `'order:' ‖ order_no` | **幂等键，格式必须与面板代码逐字节一致**（见 F-2 / 校验项 10） |
| `txid` | `external_txid` | 原值 | **对账凭据，不可丢**（见 F-1 / 校验项 9） |
| — | `note` | `method ‖ ' 充值'` | 保留原支付方式便于人工核对 |
| `created_at` | `created_at` | 原值 | Unix 秒 |
| `paid_at` | `paid_at` | 原值 | 未支付为 `0` |
| — | `updated_at` | `COALESCE(NULLIF(paid_at,0), created_at)` | 旧库无更新时刻，用最后已知变更时刻近似 |
| — | `refundable_until` | `0` | 历史订单不限期（但 `IsRefundable` 对终态仍返回 false） |

新增表：`order_events(id, order_id, from_status, to_status, actor, reason, created_at)`，记录状态流转轨迹（旧库无此能力，迁移后由应用写入，不回填）。

## 4.3 未迁移字段（显式声明，非遗漏）

| 旧列 | 处置 | 原因 |
|---|---|---|
| `recharge_orders.method`（`alipay\|wxpay\|usdt`） | 并入 `orders.note`，**未建独立列** | 支付方式属于渠道细节，新模型由 `external_txid` + 网关回调判定；如需统计应建 `payment_channels` 而非在 orders 上堆列 |
| `users.balance_cents` | **本迁移不动** | 迁移 `0005` 才做账本化；`0001` 保持旧列不变，避免同一次迁移既改结构又改语义 |
| `transactions.*` | **本迁移不动** | 同上；`verify.sql` 检查项 4 只做**守恒断言**（`SUM(transactions.amount_cents) == users.balance_cents`），不改数据 |
| `codes.*` | 留待 `0004` | 兑换码语义与订单不同（预付凭证 vs 资金流水） |

## 4.4 执行流程（含停机窗口 SOP）

```bash
# 0) 停机（消除 R0-3 的并发写导致撕裂备份）
systemctl stop eyves-panel cub-panel        # 或 install.sh --stop

# 1) WAL 收敛，确保拷到的是完整库
sqlite3 /var/lib/cub-panel/panel.db "PRAGMA wal_checkpoint(TRUNCATE);"

# 2) 迁移（脚本内部会自动 cp 出 .pre-0001.<ts>.bak 并跑 10 项校验）
./migrations/migrate-up.sh /var/lib/cub-panel/panel.db

# 3) 校验非 0 退出即为失败（已实测：篡改 ref 前缀 → exit 5）
echo "exit=$?"

# 4) 确认无误后启动新面板
systemctl start eyves-panel
```

### 实测输出（正常路径，10/10 PASS）

```text
[1.订单回填行数]   PASS   orders=4   legacy=4
[2.订单金额合计]   PASS   26999      26999
[3.已支付金额合计] PASS
[4.账本与余额守恒] PASS   mismatched=0
[5.幂等键唯一]     PASS   duplicate_refs=0
[6.退款额合法]     PASS
[7.状态枚举合法]   PASS
[8.订单用户外键]   PASS
[9.对账凭据txid]   PASS   new=2  legacy=2
[10.幂等键前缀]    PASS   bad_ref_prefix=0
[migrate] 校验全部 PASS。
```

### 实测输出（失败路径，必须 exit 5）

```text
[10.幂等键前缀]    FAIL   bad_ref_prefix=1
[migrate] 校验存在 FAIL，请立即回滚。
[migrate] 回滚命令: ./migrations/rollback.sh <db> <db>.pre-0001.<ts>.bak
exit_code=5
```

## 4.5 一条命令回滚

```bash
./migrations/rollback.sh /var/lib/cub-panel/panel.db
```

行为（[rollback.sh](file:///workspace/eyves-vm/migrations/rollback.sh)）：

1. 未给备份路径时自动取最新 `${DB}.pre-0001.*.bak`；
2. 回滚前先把**当前**库另存为 `${DB}.rollback-<ts>.bak`（回滚本身也可逆）；
3. 用备份覆盖 `${DB}`，**不执行 `down.sql`**（down 只删表，无法恢复数据）；
4. 若既无备份又要 down，且存在 `kind != 'recharge'` 的订单 → **拒绝执行并 exit 3**（防止业务数据丢失）。

## 4.6 校验项清单（`verify.sql`，10 项）

| # | 检查 | 不变量 | 关联风险 |
|---|---|---|---|
| 1 | 订单回填行数 | `COUNT(orders WHERE kind='recharge') == COUNT(recharge_orders)` | R0-3 |
| 2 | 订单金额合计 | 两边 `SUM(amount_cents)` 相等 | R0-2 |
| 3 | 已支付金额合计 | 两边 `status='paid'` 的金额和相等 | R0-2 |
| 4 | 账本与余额守恒 | 每个用户 `SUM(transactions.amount_cents) == users.balance_cents` | R0-4 |
| 5 | 幂等键唯一 | `orders.ref` 非空时全局唯一 | R0-1 |
| 6 | 退款额合法 | `0 ≤ refunded_cents ≤ amount_cents` | R0-4 |
| 7 | 状态枚举合法 | `status` 落在状态机闭集内 | R1-3 |
| 8 | 订单用户外键 | 无孤儿订单 | R0-2 |
| 9 | 对账凭据 txid | 旧表非空 `txid` 数 == 新表非空 `external_txid` 数 | **R0-2（F-1）** |
| 10 | 幂等键前缀 | recharge 订单 `ref == 'order:' ‖ order_no` | **R0-1（F-2）** |

## 4.7 门禁状态

| ID | 内容 | 状态 |
|---|---|---|
| F-1 | `txid` → `external_txid` 回填 | ✅ **本次已修复**（up.sql 增加列 + 回填；verify 检查项 9 实测通过） |
| F-2 | 幂等键格式统一为 `'order:' ‖ order_no` | ✅ **SQL 侧已固化**（verify 检查项 10 实测通过）；⚠️ Go 侧仍需把该格式抽为 `billing.OrderRef()` 单一函数，供适配器与网关回调共用（阻塞于 F-3） |
| F-3 | 实现 `Ledger` / `OrderRepo` 的 SQLite 适配器 | ❌ 待开发 |
| F-4 | 停机窗口 SOP 写入运维文档 | ✅ 见 4.4 |