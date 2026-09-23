-- Eyves VM 迁移 0001 回滚：删除统一订单表与事件表。
-- 注意：down 会丢弃 orders / order_events 的全部数据。
-- 若 orders 中已存在 recharge 之外的数据（purchase/renew/upgrade/refund），
-- 直接 down 将造成业务数据丢失。请优先使用 ./rollback.sh 从备份恢复。
--
-- 方言：SQLite。PostgreSQL / MySQL 语法一致（DROP TABLE IF EXISTS 通用）。

BEGIN;

-- 安全护栏：若存在非充值订单，拒绝回滚（SQLite 下用 SELECT 触发错误需应用层判断，
-- 因此该断言由 rollback.sh 在调用前执行，见脚本 check_non_recharge_orders）。
DROP TABLE IF EXISTS order_events;
DROP TABLE IF EXISTS orders;

COMMIT;

-- 说明：users.balance_cents、transactions 未被本迁移修改，无需回滚。
-- 若需彻底恢复旧库，执行：./migrations/rollback.sh <db_path> <backup_path>