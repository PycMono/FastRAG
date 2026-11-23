# FastRAG Vue 工作台

Vue 3 + TypeScript + Vite。生产静态文件嵌入 Go，同一个服务提供管理页面和 API，无需单独部署前端，也无需在生产机器安装 Node.js。前端不请求 CDN 或在线字体。

## 本地开发

需要 Node.js 20.19+ 或 22.12+，建议使用维护中的 LTS 版本。

```sh
# 在项目根目录
npm --prefix frontend ci
make run
# 另一个终端
make frontend-dev
```

访问 http://127.0.0.1:5173 。开发服务默认将 `/api`、`/health`、`/ready` 代理到 http://127.0.0.1:8080 。后端使用其他端口时：

```sh
FASTRAG_API_URL=http://127.0.0.1:8090 npm --prefix frontend run dev
```

## 构建与部署

```sh
make build
# 配置好 config.json 之后
./bin/server
```

`make build` 安装锁定的前端依赖、执行类型检查与 Vite 构建，再构建 Go。产物写入 `infrastructure/controller/http/web/static/`，通过 `go:embed` 编译进二进制。部署时只需要 Go 二进制和现有配置、数据库、检索服务等依赖。

构建产物应与源码变更一起提交，便于只有 Go 环境的机器使用 `make backend-build` 或 `go build ./cmd/server`。**修改前端后必须重新执行前端构建和 Go 构建**；已有 Go 进程不会自动更新内嵌页面。

入口 HTML 每次校验缓存，带内容哈希的 `/assets/` 资源长期缓存。前端使用 `#home`、`#documents`、`#import`、`#search`、`#config`、`#logs` 导航，不需要反向代理配置 SPA fallback。代理时将根路径、`/assets/`、`/api/` 和探针路由一起转发给 Go。

## 页面能力

- 文档管理：自动加载、服务端分页、原文详情、切片分页、删除确认。
- 导入文档：本地 Markdown/TXT、粘贴正文、HTTP(S) URL 解析导入；可选切片长度和向量模型。
- 检索测试：混合检索、精排、模型选择、相关性阈值、本次结果分页、原文溯源。
- 工作区：统一设置租户与知识库编号，保存到当前浏览器。切换后清空旧结果。

编号填写现有知识库，不会创建知识库。前端沿用后端现有访问模型；正式公开部署的身份认证与授权应由网关或后端落实。探针显示来自 `/ready`，包含 MySQL 和 Elasticsearch 的就绪状态。

## 验证

```sh
npm --prefix frontend test
npm --prefix frontend run build
go test ./...
npm --prefix frontend run test:e2e
```

浏览器测试默认使用本机 Google Chrome（`channel: chrome`），并自动启动开发服务器。无 Chrome 的 CI 可以安装 Chromium 并调整 `playwright.config.ts` 的 channel。测试使用模拟 API 验证前端交互及请求契约，不代替真实模型、MySQL 和 Elasticsearch 的集成测试。

## 日常操作增强

导入页增加批量文件队列（最多 100 份），展示每个文件的成功或失败结果；成功项不会随其他文件失败而自动重传。切换上传、粘贴和链接方式时保留当前会话中的草稿；导入成功后可直接进入检索验证。进行中的导入会禁用工作区切换，并触发浏览器离开提醒。

文档列表支持当前页名称、内容类型筛选及每页 10/20/50 篇，筛选不覆盖其他分页。详情支持安全的 Markdown 阅读预览、原始文本、复制和下载。预览禁用脚本、内嵌页面、表单和外部图片，避免打开文档时自动请求外部资源。

检索提供均衡、关键词优先、语义优先三种快捷权重。最近检索按工作区保存在本地浏览器，可手动清空；长结果支持展开和复制。最近工作区可从切换区直接选择，服务状态可展开查看依赖详情。

## 首页、配置与运行日志

默认首页采用产品展示布局，顶部导航直达文档管理、导入、检索、配置管理和运行日志。各功能仍在同一 Vue 项目中，由 Go 提供页面和 API，生产无需单独部署前端。

管理接口默认关闭。启动前设置至少 24 字符的管理令牌，并在配置/日志页输入相同令牌：

```sh
export FASTRAG_ADMIN_TOKEN='替换为你自己保存的至少24字符随机令牌'
make run
```

令牌仅保存在当前页面内存中，刷新后重新输入，不写入浏览器存储。远程访问时应通过 HTTPS 和现有网关访问控制保护管理入口。

配置页支持分组编辑、模型新增/删除和完整 JSON 编辑。密码与 API Key 默认脱敏；圆点占位表示保留，清空表示删除凭据。保存执行结构/模型校验，使用版本号防止覆盖旧版本，原子替换项目根目录的 `config.json`。配置目录需可写；保存不会自动重启或热加载，需人工重启 Go 服务后生效。必需字段不能随意删除，无法通过校验的修改不会写入。

日志页支持 INFO/WARN/ERROR/DEBUG、关键词、时间范围、分页、字段展开与自动刷新，显示连接时的服务运行时长。采集结构化应用日志、请求访问及接入日志器的业务错误；不包含任意 stdout/stderr、Fx 控制台、Docker 或数据库自身日志。日志按北京时间自然月写入 `logs/service-YYYY-MM.jsonl`，仅保留当月。跨月时自动切换文件并清理旧月份，空闲时每分钟检查一次；停机跨月会在启动时清理。页面只查询最近 2000 条，重启从当月文件恢复。已有 `service.jsonl` 及 `.1/.2` 旧格式文件在当月仍兼容读取，跨月后清理。已知配置凭据和管理令牌会脱敏。磁盘写入异常会在日志页面提示。

管理接口均要求 `Authorization: Bearer <令牌>`，返回禁止缓存的 JSON 响应：

| 接口 | 用途 |
| --- | --- |
| `GET /api/admin/status` | 运行时长、日志范围及保留策略 |
| `GET /api/admin/config` | 脱敏配置和版本号 |
| `PUT /api/admin/config` | 提交 `{version, config}`，保存后人工重启 |
| `GET /api/admin/logs` | `level/q/since/until/page/limit` 查询 |

检索页直接展示混合检索权重滑块：语义权重 0–100%，关键词权重为其余部分，两者合计 100%。支持拖动、键盘方向键和数值输入，与三个快捷预设同步；点击「开始检索」后生效。请求仍使用后端现有 `dense_weight`（0–1），0 为仅关键词、1 为仅语义，其余值使用加权融合；开启精排后再由精排模型调整排序。最低相关性分数是独立过滤条件，与权重不同，关闭精排时不能沿用精排模型的分数尺度。

## 检索诊断与精排预算

检索设置默认开启「记录本次检索诊断」，检索后展开「检索诊断」可查看实际生效的配置、各阶段候选数量、阶段耗时，以及当前片段的关键词/向量排名与原始分、RRF 融合分和精排分。切换选中片段时同步更新来源信息。关闭诊断后仍返回降级及评分类型元数据。

关键词召回上限由 `search.bm25_top` 控制，向量召回上限由 `search.knn_top` 控制；`search.num_candidates` 作用于每个向量子句、每个分片的近似搜索候选池，实际值至少为向量召回上限。前两项 0 默认 100，候选池 0 默认向量召回上限 ×5。召回上限最大 1000，候选池最大 10000。`retrieve_count` 仅限制融合去重、存活校验和融合分过滤后送入精排的候选数，不再改写两路 ES 召回上限。默认精排预算读取 `search.default_retrieve_count`，0 使用 `limit ×2`，有效预算至少为 `limit`、最多 200。未开启精排时不使用该预算截断融合结果。

API 请求增加 `diagnostics`（默认 false）和 `fusion_min_score`（默认 0）。响应增加 `meta`，包含 `score_type`（rrf/rerank/mixed）、`rerank_requested`、`rerank_applied`、`degraded`、`threshold_skipped` 和不含上游原始错误的 `warnings`。开启诊断时增加顶层 `diagnostics` 和每条结果的 `diagnostics`，旧的 `items` 与 `score` 字段保留。

`fusion_min_score` 在精排之前过滤 RRF 融合分。`min_score` 在请求精排时只过滤实际拿到精排分的候选；精排失败或配置未启用时不把该阈值套在 RRF 分上，页面明确提示。显式 `rerank_switch=false` 的旧 API 调用仍保留 `min_score` 过滤融合分的行为；新版页面关闭精排时提交 `min_score:0`，仅使用独立融合分阈值。分数均不表示答案正确率，跨模型/跨评分阶段不可直接比较。
