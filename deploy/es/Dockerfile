# FastRAG 本地开发用 Elasticsearch 镜像
#
# 为什么要自建镜像：官方 ES 镜像**不含**中文分词器，
# 而本项目的索引 mapping 依赖 ik_max_word / ik_smart（见设计文档 §4.3）。
# 不装插件会直接报 `analyzer [ik_max_word] not found`，索引建不起来。
#
# 注意：analysis-ik 的版本号必须与 ES 版本**严格一致**，否则插件拒绝加载。
# 可用版本查 https://release.infinilabs.com/
#
# ⚠️ 基础镜像用 Docker Hub 的 elasticsearch，**不是** docker.elastic.co。
# 原因：Docker Desktop 的镜像加速器（Registry Mirrors）只代理 docker.io，
# 不代理 docker.elastic.co。国内直连 docker.elastic.co 会卡在 Downloading 不动。
# 网络条件好、想用规范源的话，把下面 FROM 换成：
#   docker.elastic.co/elasticsearch/elasticsearch:${ES_VERSION}

ARG ES_VERSION=9.5.5
FROM elasticsearch:${ES_VERSION}

# FROM 之前的 ARG 在镜像内不可见，必须在这里重新声明一次
ARG ES_VERSION

RUN bin/elasticsearch-plugin install --batch \
      https://get.infini.cloud/elasticsearch/analysis-ik/${ES_VERSION} \
    && bin/elasticsearch-plugin list
