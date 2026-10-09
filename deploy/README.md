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

初始化业务库：

```bash
mysql -uroot -p < infrastructure/persistence/migration/schema.sql
```

建表语句的**单一事实源**是 `infrastructure/persistence/migration/schema.sql`——
服务里通过 `go:embed` 读它，容器初始化也挂它。改表结构只改这一处，
不会再出现「代码建的库和 docker 建的库不一致」。

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

### ③ 按设计文档 §4.2 建一次索引

把 mapping 里的 `ik_max_word` / `ik_smart` 换成第 ① 步验证过的分词器，建索引并确认字段全部被接受——尤其是两个 `dense_vector`：

```bash
curl -s -XPUT "localhost:9200/fastrag" -H 'Content-Type: application/json' -d @- <<'JSON'
{
  "settings": { "number_of_shards": 1, "number_of_replicas": 0, "refresh_interval": "30s" },
  "mappings": { "properties": {
    "account":     { "type": "keyword" },
    "kb_id":       { "type": "long" },
    "doc_id":      { "type": "long" },
    "chunk_id":    { "type": "keyword" },
    "order":       { "type": "integer" },
    "biz_tag":     { "type": "keyword" },
    "title":       { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
    "content":     { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
    "heading_path":{ "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
    "title_vec":   { "type": "dense_vector", "dims": 1024, "index": true, "similarity": "cosine" },
    "content_vec": { "type": "dense_vector", "dims": 1024, "index": true, "similarity": "cosine" },
    "create_ts":   { "type": "long" }
  }}
}
JSON
```

> 单索引多租户（设计文档 D2）：索引名固定 `fastrag`，所有租户共用一个物理索引，隔离靠 `account` 字段。本地验完记得删掉：`curl -XDELETE localhost:9200/fastrag`

调 mapping 和 RRF 查询体时，用 Kibana 的 Dev Tools 会比 curl 顺手得多（直接贴设计文档 §7.2 的 JSON）。

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
| `../infrastructure/persistence/migration/schema.sql` | 业务库 DDL，单一事实源（设计文档 §4.1 的两张表） |
| `../infrastructure/persistence/knowledge/index_template.go` | ES 索引 mapping / 命名规则所在（设计文档 §4.2、§9） |
| `../docs/superpowers/specs/2026-10-08-fastrag-design.md` | 设计文档 |
