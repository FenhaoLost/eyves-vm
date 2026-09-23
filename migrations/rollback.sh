#!/usr/bin/env bash
# Eyves VM 迁移回滚：一条命令恢复到迁移前状态。
#
# 用法：
#   ./migrations/rollback.sh <db_path> [backup_path]
#
# 行为：
#   1. 若提供 backup_path，直接用备份覆盖 db_path（最彻底、最推荐）；
#   2. 否则执行 0001_billing_orders.down.sql（仅删表，数据不可恢复）；
#   3. 若 orders 中存在 recharge 以外的订单，拒绝无备份回滚并退出。
#
# 约定：migrate-up.sh 每次执行前都会生成 <db_path>.pre-0001.<ts>.bak，
#       回滚脚本默认自动选用最新的备份。

set -euo pipefail

DB_PATH="${1:-}"
BACKUP_PATH="${2:-}"

if [[ -z "${DB_PATH}" ]]; then
  echo "用法: $0 <db_path> [backup_path]" >&2
  exit 2
fi
if [[ ! -f "${DB_PATH}" ]]; then
  echo "数据库不存在: ${DB_PATH}" >&2
  exit 2
fi

MIGRATION_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 未显式给出备份时，自动选用最新的迁移前备份。
if [[ -z "${BACKUP_PATH}" ]]; then
  BACKUP_PATH="$(ls -1t "${DB_PATH}".pre-0001.*.bak 2>/dev/null | head -n1 || true)"
fi

if [[ -n "${BACKUP_PATH}" && -f "${BACKUP_PATH}" ]]; then
  echo "[rollback] 从备份恢复: ${BACKUP_PATH} -> ${DB_PATH}"
  # 先备份当前状态，避免回滚本身不可逆。
  cp -f "${DB_PATH}" "${DB_PATH}.rollback-$(date +%s).bak"
  cp -f "${BACKUP_PATH}" "${DB_PATH}"
  echo "[rollback] 完成。已使用备份覆盖，无需执行 down.sql。"
  exit 0
fi

# 无备份：检查是否存在非充值订单，存在则拒绝（防业务数据丢失）。
if command -v sqlite3 >/dev/null 2>&1; then
  NON_RECHARGE="$(sqlite3 "${DB_PATH}" \
    "SELECT COUNT(*) FROM orders WHERE kind <> 'recharge';" 2>/dev/null || echo 0)"
  if [[ "${NON_RECHARGE}" != "0" ]]; then
    echo "[rollback] 拒绝执行：存在 ${NON_RECHARGE} 张非充值订单，无备份回滚会丢失业务数据。" >&2
    echo "[rollback] 请提供 backup_path，或先导出 orders 表。" >&2
    exit 3
  fi
  echo "[rollback] 未找到备份，执行 down.sql（仅含充值订单，风险可控）"
  sqlite3 "${DB_PATH}" < "${MIGRATION_DIR}/0001_billing_orders.down.sql"
  echo "[rollback] 完成。"
  exit 0
fi

echo "[rollback] 未找到 sqlite3 客户端且无备份，无法安全回滚。" >&2
exit 4