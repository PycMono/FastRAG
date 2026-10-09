#!/bin/bash
# 连 MySQL —— 排查/手工改数据用
#
# 用法:
#   bash scripts/mysql.sh                      # 交互式进 mysql
#   bash scripts/mysql.sh -e "SELECT 1"        # 直接跑一句
#   bash scripts/mysql.sh < some.sql           # 从 stdin 灌
#   MYSQL_CONTAINER=其他容器名 bash scripts/mysql.sh
#
# 存在的理由（**别直接用 `docker exec mysql mysql`**）:
#   mysql:latest 容器里的客户端默认 `character_set_client = latin1`（服务端是 utf8mb4，
#   但客户端这一侧是 latin1）。于是走这条连接手工 INSERT 中文时：
#     正确的 UTF-8 字节 → 被当成 15 个 cp1252 字符 → 再按 utf8mb4 存进去
#     演示知识库 (E6BC94E7A4BAE79FA5E8AF86E5BA93)
#       → æ¼”ç¤ºçŸ¥è¯†åº“ (C3A6C2BCE2809D...)
#   看着像"中文乱码"，其实是**双向编码错**，且列和连接字符串都查不出问题。
#
#   2026-10-09 的 demo-kb 就是这么坏的。本脚本强制带上 --default-character-set=utf8mb4。
#
#   万一已经坏了一批，用这个表达式逆回去（先 SELECT 验证再 UPDATE）:
#     CONVERT(BINARY(CONVERT(列名 USING latin1)) USING utf8mb4)
#
#   相关：设计文档 §4.2 的字符集说明。

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="$ROOT/config.json"

if [[ ! -f "$CONFIG" ]]; then
  echo "找不到 $CONFIG" >&2
  exit 1
fi

read -r HOST PORT DB USER PASS < <(python3 - "$CONFIG" <<'PY'
import json, sys
c = json.load(open(sys.argv[1]))["mysql"]
print(c["host"], c["port"], c["database"], c["user"], c["password"])
PY
)

# 默认走 docker 容器（本机开发环境的常规姿势）。
# 若宿主机装了 mysql 客户端，设 MYSQL_CONTAINER= 置空即可直连。
CONTAINER="${MYSQL_CONTAINER-mysql}"

ARGS=(
  "mysql"
  "-h$HOST" "-P$PORT" "-u$USER" "-p$PASS"
  "--default-character-set=utf8mb4"
  "$DB"
)

if [[ -n "$CONTAINER" ]]; then
  # -i 让 stdin 能灌 SQL；交互模式没有 TTY 会退化，所以 TTY 时加 -t
  TTY_FLAG="-i"
  [[ -t 0 && -t 1 ]] && TTY_FLAG="-it"
  exec docker exec $TTY_FLAG "$CONTAINER" "${ARGS[@]}" "$@"
fi

exec "${ARGS[@]}" "$@"
