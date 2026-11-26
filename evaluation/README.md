# 检索评测

现有 Vue 工作台提供「检索评测」，Go 管理题库、单飞后台任务与 MySQL 结果，Python 负责 Ragas 评分和候选题起草。默认关闭，不影响正常检索。支持原文取样、人工复核、不可覆盖发布、基线比较与显式门槛报告；不自动部署，不提供独立保留集验收或全库快照保证。

## 业务闭环

### 切片前原始 Markdown 与快速选题

`evaluation/prepare_markdown.py` 从指定 MD 独立取样，不依赖 ES 切片。每本前/中/后按17/17/16条随机分层取50条，保存原文件哈希、行号、章节与证据快照；难度目标10简单/15中等/25困难。难度目标不是模型已实现难度的保证，需要人工复核。不同解析产物可能仍存在 OCR 错误，原始 MD 不是原始 PDF 的无损证明。

原始 Markdown 候选在页面显示来源标识。发布时保留独立原文证据，不能将其伪装为可验证的 ES chunk ID。当前保存的是原文快照，发布只校验快照自洽，不会自动访问最初开发机路径或证明当前 ES 与该文件完全一致；模型生成候选不得自动标为通过。

复核页“从已通过题中快速抽取”支持输入8、10等数量，最少3题，只从approved取样，按文档/难度分组随机轮询。发布为唯一不可变题集版本后，重跑保持同一组题，不会在基线比较时重新随机。这个功能不把未复核题自动变为通过。

### 自动导入与删除联动

设置 `evaluation.enabled=true`、`auto_generate=true`、`auto_sample_count=50`，重启后启用导入后出题。默认关闭收费出题。Markdown/text 以及解析导入会在成功落库后使用切片前完整正文准备来源草稿；chunks 导入没有原文，不拼接切片冒充原文。短文有效来源不足50条时按实际数量生成。

后台每5秒扫描待处理草稿，单线程执行；每份草稿每轮生成一题，已生成题即时保存，不自动通过。单题失败最多重试3次，仍失败则跳过该题、继续其他题；只剩失败题时停止并显示状态，可在页面点「继续生成」。关闭页面不会停止后台生成。原文采样使用完整文本位置分散选取，不依赖检索切片；目录排除、复杂章节及多证据质量仍需复核。出题失败不回滚已成功导入。

重导入同名文档和删除文档会移除关联草稿题，已发布关联题集标记 retired（不再出现在列表和运行入口），保留历史run与报告。混合草稿保留其他文档题；历史数据若需彻底清除需独立的隐私删除流程。删除知识库对其全部派生题集执行同样失效逻辑。模型生成时的文档删除将等待当前单题调用结束，再清理，避免删除后后台又写回草稿。

当前并非事务性 outbox：评测库写入失败时导入仍成功，服务日志会记录派生处理失败，需要修复后重试。不要将其当作具备跨库强一致保证的机制。

1. 选择真实工作区（例如 home/econ），在“构建与复核业务题集”读取原文。
2. 为未填写片段生成问题，逐题查看原文、编辑问题和答案，标记通过/弃用及关键题。每题生成完成即保存；「后台生成」在服务端继续，关闭页面不停止，进度会自动刷新。
3. 至少3条人工通过后发布。发布核验采样来源哈希，冻结题目、来源和检索 profile；发布版本不可覆盖。新建题库按每篇 md 最多 50 段、总数最多 100 段按篇均衡抽样，不宣称代表完整业务流量。
4. 固定数据集回归运行成功后设置基线，再运行同一题集。验收前再次检查采样来源，显示覆盖/排序变化、退步题、关键题回归和逐题延迟倍率。
5. 门槛由用户显式填写并随报告保存，默认只是起始建议。报告可能为满足门槛、不满足门槛、证据不足。它是开发回归报告，不代替独立保留集、安全测试与真实流量验收。

草稿、任务、基线和验收报告保存在 MySQL 的 `eval_*` 表。原文作为不可信模型输入；不能把模型生成的参考答案自动标为通过，也不能仅凭相关性1.0证明检索完整。

## 1. 数据集

通过工作台从当前知识库新建题库，或按 `evaluation/example/pilot/` 的字段结构导入私有数据；示例不是已导入的业务数据，不能直接当检索正确性基准。题库、草稿和任务都在 MySQL 的 `eval_*` 表。先准备 10 条人工复核 dev 题。

- reference 必须依据当前语料人工核对，reference_sources.doc_id 使用字符串。
- profile 固定检索参数；retrieval_settings 记录服务级实际设置，corpus_hash 由环境负责人核验。本期不自动验证 MySQL/ES 快照。
- dev 为当前唯一允许的 split，避免日常误跑 holdout。
- 无答案题设 answerable=false，填写 no_answer_reason；只做相关性检查。
- 边界题 expected_retrieval="empty" 并列出禁止 ID；本期不支持把指定业务错误当预期成功。
- ID 标注保留；有 `reference_context_ids` 且检索结果有 chunk ID 时，证据排序和必要信息覆盖直接使用确定性 ID 指标，不调用裁判模型。`md-original` 题库没有 chunk ID，才走模型判分。不要传入文档草案中尚未实现的 judgments / required_evidence 字段，解析器会明确拒绝未知字段。
- 评测任务按单实例单飞调度；任务和数据在 MySQL，按 `retention_days` 定期清理。

## 2. Python 服务

Python 3.12+，Ragas 0.4.4。生产依赖锁见 `ragas-service/requirements.lock`。

```bash
cd evaluation/ragas-service
uv venv --python 3.12
uv pip sync requirements.lock --python .venv/bin/python
```

通过私有环境变量设置 EVAL_SERVICE_TOKEN（至少24位随机值）、JUDGE_API_KEY、JUDGE_MODEL；可选 JUDGE_BASE_URL、JUDGE_MODEL_VERSION、EVAL_METRIC_TIMEOUT_SECONDS（默认60秒，部署模板用120秒）。不要将密钥提交 Git。然后运行：

```bash
.venv/bin/uvicorn app.main:app --host 127.0.0.1 --port 18080 --workers 1 --no-access-log
```

`GET /health` 只检查进程存活；`POST /evaluate` 是单题评分，`POST /evaluate-batch` 用 Ragas 批量实验接口评分，两者都必须携带 Bearer token。服务单并发；批量按指标组合分组调用 `ragas.evaluate`，单指标失败会变成该项失败而不是整批失败。模型实际调用仍需在自己的提供商上冒烟验证。模型用量取不到时为 unknown，不虚构成本。

## 3. Go 配置

在本机私有 config.json 增加以下块，保留其他现有配置。token 使用环境变量 `FASTRAG_EVAL_TOKEN`，与 Python EVAL_SERVICE_TOKEN 一致。

```json
"evaluation": {
  "enabled": true,
  "auto_generate": false,
  "auto_sample_count": 50,
  "judge": {
    "base_url": "https://api.minimaxi.com/v1",
    "model": "MiniMax-M3.1-Flash",
    "api_key": ""
  },
  "retention_days": 30,
  "scorer_base_url": "http://127.0.0.1:18080",
  "request_timeout_seconds": 240,
  "run_timeout_seconds": 3600,
  "environment": "isolated-eval",
  "git_commit": "填写被测代码版本"
}
```

仍需要现有 FASTRAG_ADMIN_TOKEN 管理鉴权，并设置 FASTRAG_EVAL_TOKEN（与 Python 的 EVAL_SERVICE_TOKEN 一致，至少24位）。重启后打开工作台「检索评测」，选择数据集，核对请求量后提交。结果持久化，页面关闭不取消后台任务；主动取消才停止调度。启动恢复会把历史 queued/running 标记 interrupted，不自动重跑。

## 4. Docker / home

在 home 部署前先确认数据允许传入 home 及模型供应商。本次实现不自动 SSH 或部署。

复制 `deploy/ragas/.env.example` 为该目录的私有 `.env` 并填入凭据，在仓库根目录：

```bash
docker compose -f deploy/ragas/compose.yml --env-file deploy/ragas/.env config --quiet
docker compose -f deploy/ragas/compose.yml --env-file deploy/ragas/.env up -d --build
```

只启动评分容器，端口绑定 home 回环。Go 在另一主机时，通过受管理的 VPN/隧道连接；开发隧道示例 `ssh -N -L 18080:127.0.0.1:18080 home`。不能用容器 localhost 访问另一个容器，也不能假定 Compose 服务名跨主机生效。

## 5. 结果含义

结果页另外按 profile 提供检索P50/P95、评分P95、检索失败率、成功请求空结果率、归一化完全重复率、平均结果数、正文字符数和实际提交评分的指标覆盖率。覆盖率不包含未能进入评分阶段的检索失败，必须同时查看检索失败率。

有非空人工相关片段ID集合时，展示二值 Recall@5、Precision@5、MRR@5、Success@5、nDCG@5（线性二值gain，log2折损，逐查询宏平均）。不足5条的precision分母仍为5，排名位置不因重复而压缩；每ID只计首次命中。无标注为null，不伪造0。当前单测使用手算夹具，尚未完成trec_eval参照认证，不应将其作为未经核验的发布硬门槛。原始MD证据不会自动转换为chunk标注。延迟不是容量压测结论，字符数不是token。

- ContextRecall：参考信息覆盖，主观察值；ContextPrecision：有用证据的排序；ContextRelevance：整体相关性。有 `reference_context_ids` 时排序和覆盖走确定性 ID 指标，其余为模型判断；模型分不是正确性证明。
- 可回答题空检索：覆盖按策略记0，另外两项不适用，不调用模型。
- 检索失败与评分失败分开；均值只统计有效评分，必须同时看评分覆盖、检索失败、精排降级、边界失败和逐题证据。
- 输出保留原始片段；页面仅200字预览不改变评分输入。不同数据/裁判版本不直接比较。
- 写库失败停止后续调用并写服务错误日志；任务可能保留非终态，重启后可识别，不继续计费。按 `retention_days` 清理历史任务，基线任务保留。

## 6. 测试

Go：`go test -race ./application/service/evaluation ./infrastructure/persistence ./infrastructure/serviceimpl ./infrastructure/controller/http/evaluation`。

Python：在评分服务目录安装 `pytest httpx` 后运行 `python -m pytest`。测试使用替身模型，不使用业务凭据，不触发收费调用。正式采用前仍需10题人工校准与真实模型冒烟。
