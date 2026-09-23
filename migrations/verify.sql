-- Eyves VM 迁移 0001 数据校验脚本。
-- 用法：sqlite3 /var/lib/eyves/eyves.db < verify.sql
-- 期望：每一行输出均为 PASS；任一行输出 FAIL 则必须回滚。

.mode column
.headers on

-- 1) 行数一致性：orders 中 recharge 类订单数 == 旧表 recharge_orders 行数。
SELECT '1.订单回填行数' AS check_name,
       CASE WHEN (SELECT COUNT(*) FROM orders WHERE kind='recharge')
               = (SELECT COUNT(*) FROM recharge_orders)
            THEN 'PASS' ELSE 'FAIL' END AS result,
       (SELECT COUNT(*) FROM orders WHERE kind='recharge') AS orders_recharge,
       (SELECT COUNT(*) FROM recharge_orders)              AS legacy_orders;

-- 2) 金额一致性：回填订单总额 == 旧表总额。
SELECT '2.订单金额合计' AS check_name,
       CASE WHEN (SELECT COALESCE(SUM(amount_cents),0) FROM orders WHERE kind='recharge')
               = (SELECT COALESCE(SUM(amount_cents),0) FROM recharge_orders)
            THEN 'PASS' ELSE 'FAIL' END AS result,
       (SELECT COALESCE(SUM(amount_cents),0) FROM orders WHERE kind='recharge') AS orders_sum,
       (SELECT COALESCE(SUM(amount_cents),0) FROM recharge_orders)              AS legacy_sum;

-- 3) 已支付金额一致性：已入账充值金额合计一致。
SELECT '3.已支付金额合计' AS check_name,
       CASE WHEN (SELECT COALESCE(SUM(amount_cents),0) FROM orders WHERE kind='recharge' AND status='paid')
               = (SELECT COALESCE(SUM(amount_cents),0) FROM recharge_orders WHERE status='paid')
            THEN 'PASS' ELSE 'FAIL' END AS result;

-- 4) 账本守恒：每位用户的 transactions 合计 == users.balance_cents。
--    这是资金正确性的核心不变量，必须为 0 条不一致记录。
SELECT '4.账本与余额守恒' AS check_name,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       COUNT(*) AS mismatched_users
FROM (
  SELECT u.id
  FROM users u
  LEFT JOIN transactions t ON t.user_id = u.id
  GROUP BY u.id, u.balance_cents
  HAVING COALESCE(SUM(t.amount_cents), 0) != u.balance_cents
);

-- 5) 幂等键唯一性：orders.ref 非空时不得重复。
SELECT '5.幂等键唯一' AS check_name,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       COUNT(*) AS duplicate_refs
FROM (SELECT ref FROM orders WHERE ref != '' GROUP BY ref HAVING COUNT(*) > 1);

-- 6) 退款额不得为负且不得超过订单金额。
SELECT '6.退款额合法' AS check_name,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       COUNT(*) AS bad_refunds
FROM orders
WHERE refunded_cents < 0 OR refunded_cents > amount_cents;

-- 7) 状态合法性：status 必须落在状态机定义的枚举内。
SELECT '7.状态枚举合法' AS check_name,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       COUNT(*) AS bad_status
FROM orders
WHERE status NOT IN ('pending','paid','provisioned','active','failed','cancelled',
                     'expired','refunding','refunded','partially_refunded');

-- 8) 外键完整性：orders.user_id 必须存在于 users。
SELECT '8.订单用户外键' AS check_name,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       COUNT(*) AS orphan_orders
FROM orders o LEFT JOIN users u ON u.id = o.user_id WHERE u.id IS NULL;

-- 9) 对账凭据不丢失：旧表非空 txid 必须逐条回填到 external_txid。
--    FAIL 意味着网关/链上流水号被丢弃，用户投诉时无法举证。
SELECT '9.对账凭据txid' AS check_name,
       CASE WHEN (SELECT COUNT(*) FROM orders WHERE kind='recharge' AND external_txid != '')
               = (SELECT COUNT(*) FROM recharge_orders WHERE txid != '')
            THEN 'PASS' ELSE 'FAIL' END AS result,
       (SELECT COUNT(*) FROM orders WHERE kind='recharge' AND external_txid != '') AS new_txid,
       (SELECT COUNT(*) FROM recharge_orders WHERE txid != '')                     AS legacy_txid;

-- 10) 幂等键前缀一致：历史充值订单的 ref 必须恰为 'order:' ‖ order_no。
--    若前缀格式与面板代码生成的不一致，同一笔网关回调会二次入账（资金风险）。
SELECT '10.幂等键前缀' AS check_name,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result,
       COUNT(*) AS bad_ref_prefix
FROM orders
WHERE kind = 'recharge' AND ref != 'order:' || order_no;