# FastRAG 本地开发环境

## 起停

```bash
docker compose up -d --build              # 构建并启动 ES（默认不带 Kibana）
docker compose logs -f es                 # 看 ES 日志
docker compose ps                         # 看健康状态
docker compose down                       # 停，保留数据
docker compose down -v                    # 停，连数据一起删（重建索引时用）

docker compose --profile kibana up -d     # 需要 Kibana 时（额外 ~1.2GB 下载）
```

启动后：

| 服务 | 地址 | 说明 |
|---|---|---|
| Elasticsearch | http://localhost:9200 | 必装 |
| Kibana | http://localhost:5601 | **默认不启动**，镜像 ~1.2GB。调 mapping / RRF 查询体时再用 |

> Kibana 放进了 `kibana` profile——网络慢的时候，少拉 1.2GB 能让 ES 先跑起来。

MySQL / Redis 本机已在跑（`3306` / `6379`），直接用，不用另起。
需要完全独立的一套时，取消 `docker-compose.yml` 末尾注释块（端口故意错开成 `3307` / `6380`）。

初始化业务库与索引：

```bash
make init-db     # 建 MySQL 库表，可重复执行（DDL 全是 IF NOT EXISTS）
make es-index    # 建 ES 索引（已存在则只校验 mapping，不改动）
```

建表语句的**单一事实源**是 `deploy/schema.sql`，索引定义的单一事实源是
`deploy/create-es-index.sh`。**两者都由人先建好，服务不建**（设计文档 §9.4）——
运行期的连接账号因此也不需要 DDL 权限。改结构只改 `deploy/` 下这两处。

> ⚠️ 别直接 `mysql -uroot -p < deploy/schema.sql`：容器里的 mysql 客户端
> 默认字符集是 latin1，中文会被**双向编码错**存进库（`租户` 存成 `ç§Ÿæˆ·`），
> 而且列和连接字符串都查不出问题。`make init-db` 带上了
> `--default-character-set=utf8mb4`，用它。

## 远端发布

统一入口是 `deploy/deploy.sh`，直接执行就发布全部代码和基础组件：

```bash
./deploy/deploy.sh              # bootstrap：基础组件 + MySQL + ES + 前端 + Go + Python
./deploy/deploy.sh bootstrap    # 同上
./deploy/deploy.sh db           # 只应用 deploy/schema.sql
./deploy/deploy.sh es           # 只创建/校验 ES 索引
./deploy/deploy.sh app          # 只构建前端 + Go 并重启 app
./deploy/deploy.sh evaluator    # 只上传并重建 Ragas 评分服务
```

`bootstrap` 在远端缺什么补什么：

- 建目录、上传 compose / Dockerfile / Ragas 服务；
- 首次生成 `.admin.env`（MySQL 密码、管理 token、评测 token）；
- 首次从示例生成 `config.json`，默认 bge-m3、关闭精排、关闭评测；
- 构建 ES IK 镜像，启动 MySQL / ES；
- 建表、建索引、重建 Ragas、构建并重启 app。

管理 token 只写入远端 `.admin.env`，脚本不打印；查看方式：

```bash
ssh home-old-pub "grep FASTRAG_ADMIN_TOKEN /home/duomi/fastrag/.admin.env"
```

前提是远端有 SSH 和 Docker Compose。页面能直接打开；检索要真正可用，还需要
远端有可访问的 embedding/rerank 模型服务（默认配置指向本机 Ollama 的 bge-m3）。

默认目标是 `home-old-pub:/home/duomi/fastrag`，可用环境变量覆盖：

```bash
FASTRAG_DEPLOY_HOST=home-old-pub
FASTRAG_DEPLOY_DIR=/home/duomi/fastrag
FASTRAG_DEPLOY_DB_CONTAINER=fastrag-mysql
FASTRAG_DEPLOY_ES_URL=http://127.0.0.1:9200
FASTRAG_DEPLOY_EVAL_IMAGE=fastrag/ragas-evaluator:20261125
FASTRAG_DEPLOY_HEALTH_URL=http://192.168.50.159:8088/health
```

`deploy/schema.sql` 和 `deploy/create-es-index.sh` 仍然是**单一事实源**，
deploy 脚本只负责把它们送到远端执行，不复制内容，避免两份定义漂移。
本地开发继续用 `make init-db` / `make es-index`，远端发布用 `make deploy-db` / `make deploy-es`。


---

## 起来之后验三件事

### ① IK 分词器真的生效了 ← 最关键

```bash
curl -s -XPOST "localhost:9200/_analyze" -H 'Content-Type: application/json' \
  -d '{"analyzer":"ik_smart","text":"知识库检索服务"}'
```

**期望**：输出 `知识库 / 检索 / 服务` 这样的词。

**如果输出一堆单字**（`知` / `识` / `库`），说明插件没装上——而且**不会报错**，只是静默退化成按字切。这是最容易踩的坑，务必亲眼确认。

也顺手确认插件在列表里：

```bash
docker exec fastrag-es bin/elasticsearch-plugin list
# 期望：analysis-ik
```

### ② 版本与向量能力

```bash
curl -s localhost:9200 | grep -E '"number"|"build_flavor"'
```

`dense_vector` 是 ES 内置类型，不需要额外插件。

> ⚠️ **RRF retriever 不可用**：`rrf` 与 `sub_searches`（带 `rank` 的那套）都属于
> 付费特性，`basic` license 下会直接报
> `current license is non-compliant for [Reciprocal Rank Fusion (RRF)]`。
> 免费可用的替代是 **`knn` + `query` 单请求 + `boost` 加权**（已实测通过），
> 或**两路独立检索 + 应用层 RRF 融合**。设计文档 §7.2 已按此修正。

### ③ 建索引

```bash
make es-index              # 或 bash deploy/create-es-index.sh
```

索引定义的**权威版本**就是这条 curl（mapping 里字段取值的理由也写在它上面的注释里）。
已存在时 ES 会回 `resource_already_exists_exception`，**不会覆盖**。

> 单索引多租户（设计文档 D2）：索引名固定 `fastrag`，所有租户共用一个物理索引，
> 隔离靠 `tenant` 字段——所以它必须是 `keyword`。本地要重来一遍时先删：
> `curl -XDELETE localhost:9200/fastrag`（**删索引 = 丢全部切片**，脚本故意不代劳这件事）

> ⚠️ **ES 默认 `action.auto_create_index=true`**：万一服务在索引建好之前先写入，
> ES 会**自己**建一个动态 mapping 的 `fastrag`（无 IK、无 `dense_vector`、
> `tenant` 是 text），写入还返回成功。脚本下次运行会因 mapping 不符而报错，
> 不会把它固化，但已经写进去的数据要重导。服务侧也会在第一次用到时直接报错。
> 若要根治（本服务独占的集群才建议）：`curl -XPUT 'localhost:9200/_cluster/settings'
> -H 'Content-Type: application/json'
> -d '{"persistent":{"action.auto_create_index":"false"}}'`

调查询体时，用 Kibana 的 Dev Tools 会比 curl 顺手得多（直接贴设计文档 §7.2 的 JSON）。

---

## 排障

| 现象 | 原因 | 处理 |
|---|---|---|
| `docker compose up --build` 长时间卡在 `Downloading xx MB` 不动 | **国内直连 `docker.elastic.co` 拉不动**——Docker Desktop 的镜像加速器只代理 `docker.io`，不代理它 | 本项目已改用 Docker Hub 的 `elasticsearch` / `kibana`（Elastic 官方同步发布）。若仍慢，确认 `docker info` 里的 Registry Mirrors 生效 |
| ES 起不来，日志报 `max virtual memory areas vm.max_map_count [65530] is too low` | Docker Desktop 的 Linux VM 没调这个内核参数 | `docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -u -n -i sysctl -w vm.max_map_count=262144` |
| 创建索引报 `analyzer [ik_max_word] not found` | 插件没装进镜像 | `docker compose build --no-cache es` 重构建 |
| `_analyze` 输出单字 | 插件装了但没生效，或用了错的 analyzer 名 | 查 `elasticsearch-plugin list`，并确认 mapping 里写的是 `ik_max_word` 而不是 `ik-max-word` |
| `docker compose up` 报端口被占 | 9200 / 5601 被别的服务用了 | 改 `docker-compose.yml` 里的宿主端口映射 |

> `vm.max_map_count` 在 **macOS 宿主机**上查不到是正常的——那是 Linux VM 内的设置，不是 macOS 的 sysctl 项。

---

## 镜像拉取太慢怎么办

**症状**：`docker compose up --build` 长时间卡在 `Downloading xx MB`。

**原因**：Docker Desktop 的 Registry Mirrors **只代理 `docker.io`，不代理 `docker.elastic.co`**。如果 daemon 里配的加速器效果不好（实测 daocloud 拉 alpine 要 31s），ES 这种 1.4GB 的镜像会慢到不可用。

**办法一：换更快的加速器**（实测 `docker.1ms.run` 拉 alpine 只要 2s）

编辑 Docker Desktop 的 daemon 配置（Settings → Docker Engine），把快的源放最前面：

```json
{
  "registry-mirrors": [
    "https://docker.1ms.run",
    "https://docker.m.daocloud.io",
    "https://dockerproxy.cn"
  ]
}
```

改完**重启 Docker Desktop**（会重启你现有的所有容器，挑个空闲时候做）。

**办法二：不改配置，单独走镜像源拉**

```bash
docker pull docker.1ms.run/library/elasticsearch:9.5.5
docker tag  docker.1ms.run/library/elasticsearch:9.5.5 elasticsearch:9.5.5
docker compose up -d --build        # 本地已有基础镜像，不再走网络
```

好处是**不用重启 Docker Desktop**，不影响你正在跑的其它容器。Kibana 同理，把 `kibana` 换成 `docker.1ms.run/library/kibana`。

**办法三：别处下好再搬**

```bash
# 网好的机器上
docker pull elasticsearch:9.5.5 && docker save elasticsearch:9.5.5 | gzip > es.tar.gz
# 本机
gunzip -c es.tar.gz | docker load
```

> ⚠️ 用公共镜像源意味着把镜像分发交给第三方。Docker 会校验层摘要，但清单也由镜像源提供，严格来说不能完全排除被替换的可能。公司如果有内网仓库（Harbor）优先走内网。

---

## 版本对齐

**analysis-ik 的版本号必须与 ES 版本严格一致**，否则插件拒绝加载。

当前锁定 `9.5.5`（ES 最新稳定版，IK 有对应包）。升级时**两个一起升**：

```bash
ES_VERSION=9.6.0 docker compose up -d --build
```

可用版本查 https://release.infinilabs.com/

---

## 相关文件

| 文件 | 用途 |
|---|---|
| `docker-compose.yml` | ES + Kibana（含可选的独立 MySQL/Redis 注释块） |
| `deploy/es/Dockerfile` | 基于官方 ES 镜像，装 analysis-ik 插件 |
| `schema.sql` | 业务库 DDL，单一事实源（本服务只建 `knowledge_doc`；`knowledge` 由外部系统预置）。执行见 `make init-db` |
| `create-es-index.sh` | ES 索引 mapping / settings，单一事实源（设计文档 §4.2、§9）。执行见 `make es-index` |
| `../common/constants/field.go` | 查询/写入用的字段名常量（mapping 的另一半） |
| `../docs/superpowers/specs/2026-10-08-fastrag-design.md` | 设计文档 |
