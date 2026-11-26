#!/usr/bin/env bash
#
# FastRAG 远端发布脚本。
#
#   ./deploy/deploy.sh              # 默认：bootstrap，首次/日常都可用
#   ./deploy/deploy.sh bootstrap    # 补齐基础组件与配置，然后发布全部代码
#   ./deploy/deploy.sh all          # 同 bootstrap
#   ./deploy/deploy.sh app          # 只构建前端 + Go 并重启 app
#   ./deploy/deploy.sh evaluator    # 只上传并重建 Ragas 评分服务
#   ./deploy/deploy.sh db           # 只应用 deploy/schema.sql
#   ./deploy/deploy.sh es           # 只创建/校验 ES 索引
#
# 默认目标：
#   host=home-old-pub  dir=/home/duomi/fastrag
# 用环境变量覆盖：
#   FASTRAG_DEPLOY_HOST / FASTRAG_DEPLOY_DIR
#   FASTRAG_DEPLOY_DB_CONTAINER / FASTRAG_DEPLOY_ES_URL
#   FASTRAG_DEPLOY_EVAL_IMAGE / FASTRAG_DEPLOY_BASE_SERVICES
#   FASTRAG_DEPLOY_HEALTH_URL
#
# 脚本不打印任何 token 或数据库密码；首次 bootstrap 会生成到远端
# .admin.env（权限 600），管理令牌请自行到该文件读取。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
HOST=${FASTRAG_DEPLOY_HOST:-home-old-pub}
DIR=${FASTRAG_DEPLOY_DIR:-/home/duomi/fastrag}
DB_CONTAINER=${FASTRAG_DEPLOY_DB_CONTAINER:-fastrag-mysql}
ES_URL=${FASTRAG_DEPLOY_ES_URL:-http://127.0.0.1:9200}
EVAL_IMAGE=${FASTRAG_DEPLOY_EVAL_IMAGE:-fastrag/ragas-evaluator:20261125}
BASE_SERVICES=${FASTRAG_DEPLOY_BASE_SERVICES:-mysql es}
HEALTH_URL=${FASTRAG_DEPLOY_HEALTH_URL:-http://192.168.50.159:8088/health}

remote_exists() {
	ssh "$HOST" "test -e '$1'"
}

usage() {
	cat <<'EOF'
用法: ./deploy/deploy.sh [bootstrap|all|db|es|app|evaluator]

  bootstrap  补齐目录/配置/token/基础组件，然后发布全部代码（默认）
  all        同 bootstrap
  db         应用 deploy/schema.sql 到远端 MySQL
  es         在远端创建/校验 ES 索引
  app        构建前端和 Linux 二进制，上传并重启 app
  evaluator  上传并重建 Ragas 评分服务
EOF
}

check_ssh() {
	ssh "$HOST" "command -v docker >/dev/null && docker compose version >/dev/null" || {
		echo "!! ${HOST} 缺少 docker 或 docker compose" >&2
		return 1
	}
}

ensure_assets() {
	ssh "$HOST" "mkdir -p '${DIR}/bin' '${DIR}/runtime' '${DIR}/logs' '${DIR}/ragas' '${DIR}/data/mysql' '${DIR}/data/es' '${DIR}/deploy-es'"

	if ! remote_exists "${DIR}/docker-compose.yml"; then
		scp -q "$ROOT/deploy/compose.yml" "${HOST}:${DIR}/docker-compose.yml"
	fi
	if ! remote_exists "${DIR}/Dockerfile.fastrag"; then
		scp -q "$ROOT/deploy/Dockerfile.fastrag" "${HOST}:${DIR}/Dockerfile.fastrag"
	fi

	rsync -a "$ROOT/deploy/es/" "${HOST}:${DIR}/deploy-es/"
	rsync -a --delete "$ROOT/evaluation/ragas-service/app/" "${HOST}:${DIR}/ragas/app/"
	scp -q "$ROOT/evaluation/ragas-service/Dockerfile" \
		"$ROOT/evaluation/ragas-service/requirements.lock" "${HOST}:${DIR}/ragas/"
	if ! remote_exists "${DIR}/ragas/compose.yml"; then
		scp -q "$ROOT/deploy/ragas/compose.remote.yml" "${HOST}:${DIR}/ragas/compose.yml"
	fi
}

ensure_env() {
	scp -q "$ROOT/deploy/bootstrap_env.py" "${HOST}:${DIR}/bootstrap_env.py"
	if ! remote_exists "${DIR}/config.json"; then
		scp -q "$ROOT/config.example.json" "${HOST}:${DIR}/config.json.bootstrap"
	fi
	ssh "$HOST" "cd '${DIR}' && python3 bootstrap_env.py admin && python3 bootstrap_env.py mysql && python3 bootstrap_env.py config && python3 bootstrap_env.py ragas && rm -f config.json.bootstrap"
}

build_es_image() {
	ssh "$HOST" "docker image inspect fastrag-es:9.5.5 >/dev/null 2>&1 || docker build -t fastrag-es:9.5.5 '${DIR}/deploy-es'"
}

start_base() {
	if ssh "$HOST" "docker ps --format '{{.Names}}' | grep -qx fastrag-mysql && docker ps --format '{{.Names}}' | grep -qx fastrag-es"; then
		echo "==> MySQL / ES 已在运行"
		return 0
	fi
	echo "==> 启动基础组件: ${BASE_SERVICES}"
	ssh "$HOST" "cd '${DIR}' && docker compose up -d ${BASE_SERVICES}"
	for _ in $(seq 1 40); do
		if ssh "$HOST" "docker exec ${DB_CONTAINER} mysqladmin ping -h127.0.0.1 -uroot -p\"\$MYSQL_ROOT_PASSWORD\" --silent >/dev/null 2>&1 && curl -fs '${ES_URL%/}/_cluster/health' >/dev/null 2>&1"; then
			echo "==> MySQL / ES 已就绪"
			return 0
		fi
		sleep 3
	done
	echo "!! MySQL / ES 未在预期时间内就绪" >&2
	return 1
}

deploy_db() {
	echo "==> 应用 MySQL schema 到 ${HOST}:${DB_CONTAINER}"
	ssh "$HOST" "docker exec -i ${DB_CONTAINER} sh -c 'mysql -h127.0.0.1 -uroot -p\"\$MYSQL_ROOT_PASSWORD\" --default-character-set=utf8mb4'" < "$ROOT/deploy/schema.sql"
}

deploy_es() {
	echo "==> 在 ${HOST} 创建/校验 ES 索引 ${ES_URL}"
	ssh "$HOST" "ES_URL='${ES_URL}' bash -s" < "$ROOT/deploy/create-es-index.sh"
}

build_app() {
	echo "==> 构建前端"
	if [ ! -d "$ROOT/frontend/node_modules" ]; then
		npm --prefix "$ROOT/frontend" ci --no-audit --no-fund
	fi
	npm --prefix "$ROOT/frontend" run build
	echo "==> 构建 Linux 二进制"
	mkdir -p "$ROOT/bin"
	(cd "$ROOT" && GOOS=linux GOARCH=amd64 go build -trimpath -o "$ROOT/bin/fastrag-linux" ./cmd/server)
}

deploy_app() {
	build_app

	echo "==> 上传并重启 ${HOST}:${DIR}"
	scp -q "$ROOT/bin/fastrag-linux" "${HOST}:${DIR}/bin/fastrag.new"
	ssh "$HOST" "cd '${DIR}' && cp -a bin/fastrag bin/fastrag-backup-\$(date +%H%M%S) && mv bin/fastrag.new bin/fastrag && docker compose build fastrag && docker compose up -d fastrag"
	rm -f "$ROOT/bin/fastrag-linux"

	echo "==> 等待 app 端口"
	for _ in $(seq 1 20); do
		if curl -fs -o /dev/null "${HEALTH_URL}"; then
			echo "==> app 已响应"
			return 0
		fi
		sleep 2
	done
	echo "!! app 未在预期时间内响应，请查看远端日志" >&2
	return 1
}

deploy_evaluator() {
	echo "==> 上传 Ragas 服务"
	rsync -a --delete "$ROOT/evaluation/ragas-service/app/" "${HOST}:${DIR}/ragas/app/"
	scp -q "$ROOT/evaluation/ragas-service/Dockerfile" \
		"$ROOT/evaluation/ragas-service/requirements.lock" "${HOST}:${DIR}/ragas/"
	ssh "$HOST" "test -f '${DIR}/ragas/compose.yml'" || {
		echo "!! 远端缺少 ${DIR}/ragas/compose.yml，无法启动 evaluator" >&2
		return 1
	}
	echo "==> 重建并重启 evaluator"
	ssh "$HOST" "cd '${DIR}/ragas' && docker build -q -t '${EVAL_IMAGE}' . && docker compose -f compose.yml up -d --force-recreate evaluator"
}

bootstrap() {
	check_ssh
	ensure_assets
	ensure_env
	build_es_image
	start_base
	deploy_db
	deploy_es
	deploy_evaluator
	deploy_app
	PAGE_URL=${HEALTH_URL%/health}
	echo "==> 完成。页面：${PAGE_URL}"
	echo "==> 管理令牌在 ${HOST}:${DIR}/.admin.env 的 FASTRAG_ADMIN_TOKEN（未打印）"
	echo "    查看：ssh ${HOST} \"grep FASTRAG_ADMIN_TOKEN ${DIR}/.admin.env\""
}

case "${1:-bootstrap}" in
bootstrap | all) bootstrap ;;
db) deploy_db ;;
es) deploy_es ;;
app) deploy_app ;;
evaluator) deploy_evaluator ;;
-h | --help | help) usage ;;
*)
	usage
	exit 1
	;;
esac
