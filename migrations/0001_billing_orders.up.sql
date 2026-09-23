-- Eyves VM 迁移 0001：统一订单表 + 订单状态事件表。
-- 目的：旧库仅有 recharge_orders（充值），购买/升级/续费/退款没有订单凭证，
-- 无法退款、无法对账。本迁移引入统一 orders 表并回填历史充值数据。
--
-- 方言：本文件以 SQLite 为准。PostgreSQL / MySQL 差异见文件末尾注释。

BEGIN;

CREATE TABLE IF NOT EXISTS orders (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  order_no         TEXT    NOT NULL UNIQUE,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind             TEXT    NOT NULL,                    -- recharge|purchase|renew|upgrade|refund
  plan_id          INTEGER NOT NULL DEFAULT 0,
  instance_id      INTEGER NOT NULL DEFAULT 0,
  amount_cents     INTEGER NOT NULL,                    -- 正数：订单总额（最小单位）
  refunded_cents   INTEGER NOT NULL DEFAULT 0,          -- 已退金额
  currency         TEXT    NOT NULL DEFAULT 'CNY',
  status           TEXT    NOT NULL DEFAULT 'pending',  -- 见 internal/billing/order.go 状态机
  ref              TEXT    NOT NULL DEFAULT '',         -- 外部幂等键
  external_txid    TEXT    NOT NULL DEFAULT '',         -- 第三方/链上流水号（对账凭据，来源 recharge_orders.txid）
  note             TEXT    NOT NULL DEFAULT '',
  refundable_until INTEGER NOT NULL DEFAULT 0,          -- Unix 秒，0 = 不限期
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL DEFAULT 0,
  paid_at          INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_orders_user    ON orders(user_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_status  ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_kind    ON orders(kind, status);
CREATE INDEX IF NOT EXISTS idx_orders_inst    ON orders(instance_id);
-- 幂等：同一外部键只允许一张订单。SQLite/PG 用部分唯一索引；
-- MySQL 无部分索引，见文件末尾。
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_ref ON orders(ref) WHERE ref != '';

CREATE TABLE IF NOT EXISTS order_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id   INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  from_status TEXT   NOT NULL DEFAULT '',
  to_status  TEXT    NOT NULL,
  actor      TEXT    NOT NULL DEFAULT '',               -- system|admin:<email>|user:<id>
  reason     TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_order_events_order ON order_events(order_id, id);

-- 回填历史充值订单，保持与旧表完全一致的金额与状态语义。
-- external_txid 必须回填：第三方/链上流水号是对账举证凭据，丢失后无法与网关核对。
INSERT INTO orders (order_no, user_id, kind, amount_cents, refunded_cents, currency,
                    status, ref, external_txid, note, created_at, updated_at, paid_at)
SELECT o.order_no, o.user_id, 'recharge', o.amount_cents, 0, 'CNY',
       CASE o.status
         WHEN 'paid'    THEN 'paid'
         WHEN 'expired' THEN 'expired'
         ELSE 'pending'
       END,
       CASE WHEN o.order_no = '' THEN '' ELSE 'order:' || o.order_no END,
       o.txid,
       o.method || ' 充值', o.created_at,
       COALESCE(NULLIF(o.paid_at, 0), o.created_at), o.paid_at
FROM recharge_orders o
WHERE NOT EXISTS (SELECT 1 FROM orders n WHERE n.order_no = o.order_no);

-- 迁移自检：回填行数必须等于旧表行数，否则回滚。
-- （由 verify.sql 独立执行，此处仅留断言注释，避免依赖 SQLite 断言能力。）

COMMIT;

-- PostgreSQL 差异：
--   1) id 改为 BIGSERIAL PRIMARY KEY；
--   2) 时间列类型为 BIGINT，语义不变（Unix 秒）；
--   3) 部分唯一索引语法与 SQLite 相同，无需改动。
-- MySQL 差异：
--   1) id 改为 BIGINT AUTO_INCREMENT PRIMARY KEY；
--   2) TEXT NOT NULL DEFAULT '' 需改为 VARCHAR(255) NOT NULL DEFAULT ''；
--   3) 不支持部分唯一索引：orders.ref 空值改用 NULL（UNIQUE 忽略 NULL），
--      即 ref 列改为 VARCHAR(255) NULL，并在写入层把空串转为 NULL。