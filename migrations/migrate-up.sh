#!/usr/bin/env bash
# Eyves VM 迁移执行：先备份，再按版本号顺序应用 up.sql，最后跑 verify.sql。
#
# 用法：
#   ./migrations/migrate-up.sh <db_path> [--to 0001]
#
# 幂等：已应用的版本记录在 schema_migrations，重复执行自动跳过。

set -euo pipefail

DB_PATH="${1:-}"
TO_VERSION=""

if [[ -z "${DB_PATH}" ]]; then
  echo "用法: $0 <db_path> [--to <version>]" >&2
  exit 2
fi
shift || true
while [[ $# -gt 0 ]]; do
  case "$1" in
    --to) TO_VERSION="${2:-}"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

MIGRATION_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TS="$(date +%Y%m%d%H%M%S)"

# 1) 迁移前备份：命名与被 rollback.sh 自动识别的前缀一致。
BACKUP="${DB_PATH}.pre-0001.${TS}.bak"
echo "[migrate] 备份: ${DB_PATH} -> ${BACKUP}"
cp -f "${DB_PATH}" "${BACKUP}"

# 2) 迁移记录表。
sqlite3 "${DB_PATH}" <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    TEXT PRIMARY KEY,
  applied_at INTEGER NOT NULL
);
SQL

# 3) 按序应用。
for UP_FILE in $(ls -1 "${MIGRATION_DIR}"/*.up.sql | sort); do
  VERSION="$(basename "${UP_FILE}" | cut -d_ -f1)"
  if [[ -n "${TO_VERSION}" && "${VERSION}" > "${TO_VERSION}" ]]; then
    echo "[migrate] 跳过 ${VERSION}（超出 --to ${TO_VERSION}）"
    continue
  fi
  APPLIED="$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM schema_migrations WHERE version='${VERSION}';")"
  if [[ "${APPLIED}" != "0" ]]; then
    echo "[migrate] 已应用，跳过 ${VERSION}"
    continue
  fi
  echo "[migrate] 应用 ${VERSION}: $(basename "${UP_FILE}")"
  sqlite3 "${DB_PATH}" < "${UP_FILE}"
  sqlite3 "${DB_PATH}" "INSERT INTO schema_migrations(version, applied_at) VALUES('${VERSION}', strftime('%s','now'));"
done

# 4) 数据校验：sqlite3 自身报错（语法/缺列）与校验项 FAIL 都必须终止并提示回滚。
#    注意：不可写成 `if ! sqlite3 ... | grep -q FAIL` —— 配合 `set -o pipefail` 时，
#    sqlite3 报错会让管道返回非 0，`!` 反而把它判成"校验通过"（反向逻辑）。
echo "[migrate] 执行校验..."
set +e
VERIFY_OUT="$(sqlite3 "${DB_PATH}" < "${MIGRATION_DIR}/verify.sql" 2>&1)"
VERIFY_RC=$?
set -e
echo "${VERIFY_OUT}"

if [[ "${VERIFY_RC}" -eq 0 ]] && ! grep -q 'FAIL' <<<"${VERIFY_OUT}"; then
  echo "[migrate] 校验全部 PASS。"
  echo "[migrate] 如需回滚: ${MIGRATION_DIR}/rollback.sh ${DB_PATH} ${BACKUP}"
  exit 0
fi
if [[ "${VERIFY_RC}" -ne 0 ]]; then
  echo "[migrate] 校验脚本执行失败（rc=${VERIFY_RC}）：无法判定数据一致性，请立即回滚。" >&2
else
  echo "[migrate] 校验存在 FAIL，请立即回滚。" >&2
fi
echo "[migrate] 回滚命令: ${MIGRATION_DIR}/rollback.sh ${DB_PATH} ${BACKUP}" >&2
exit 5