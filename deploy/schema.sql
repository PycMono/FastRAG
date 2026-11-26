-- FastRAG 业务库 DDL —— 建表语句的**单一事实源**。
--
-- 由 make init-db 执行（把本文件灌进 MySQL）。本服务**不建表**：
-- 上线前先把库建好，运行期的连接账号也就不需要 DDL 权限。
--
-- 幂等：全部 CREATE ... IF NOT EXISTS，重复执行安全。
--
-- 本文件只建**本服务自己拥有**的表：
--   · knowledge_doc —— 文档，id 由应用侧（雪花）预分配，**没有** AUTO_INCREMENT。
--
-- **不建 knowledge**：知识库由外部系统预置（§1.2），本服务只读它、
-- 只回写 doc_count / chunk_count / update_ts 三列。它的 DDL 归建库方。
--
-- 刻意**没有**的东西 —— 这几条是防退化闸门，改动前先想清楚（对应设计文档 §4.1）：
--   · 切片表 —— 切片下沉 ES 是核心决策 D3；
--   · 租户路由表 —— 索引策略是单索引 + term(tenant)（D2）；
--   · 对象存储 —— 原文直接进 knowledge_doc.content（2026-10-12），
--     不引入对象存储、也不做「原文件」的权威副本；因此没有路径列。
--     （库级来源 URL 是 knowledge.source_url，归建库方维护，不是文档的路径列。）
--   · 发布台账 —— 本期不做发布与版本（设计文档 §1）；
--   · 文档的代次列 —— 写侧一致性增强是 P2，本期不做（§13 待定 9）。

CREATE DATABASE IF NOT EXISTS `fastrag` DEFAULT CHARSET=utf8mb4;

USE `fastrag`;

-- ① 文档
CREATE TABLE IF NOT EXISTS `knowledge_doc` (
  `id`              BIGINT UNSIGNED NOT NULL COMMENT '雪花 ID，写入前预分配',
  `kb_id`           BIGINT UNSIGNED NOT NULL,
  `tenant`         VARCHAR(64)  NOT NULL,
  `name`            VARCHAR(512) NOT NULL COMMENT '文档名。同名重导入 = 更新，doc_id 不变',
  `title`           VARCHAR(255) NOT NULL DEFAULT '' COMMENT '展示用标题，与 name（文档身份）分开',
  `content`         MEDIUMTEXT   NOT NULL COMMENT '整篇原文：markdown 或纯文本；format=chunks 时为空串',
  `content_hash`    CHAR(64)     NOT NULL COMMENT '内容指纹',
  `chunk_count`     INT          NOT NULL DEFAULT 0,
  `create_ts`       BIGINT       NOT NULL,
  `update_ts`       BIGINT       NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_kb_name` (`kb_id`, `name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ② 评测题库：一个发布版本一行，发布后不可覆盖。
-- manifest/profiles/cases 是完整快照；cases 可能到数 MB，所以用 LONGTEXT 而不是 JSON 列。
CREATE TABLE IF NOT EXISTS `eval_dataset` (
  `name`            VARCHAR(128) NOT NULL,
  `version`         VARCHAR(160) NOT NULL DEFAULT '' COMMENT 'manifest.dataset_version，列表展示用',
  `tenant`          VARCHAR(64)  NOT NULL DEFAULT '',
  `kb_nos`          JSON         NOT NULL,
  `cases_count`     INT          NOT NULL DEFAULT 0,
  `profiles_count`  INT          NOT NULL DEFAULT 0,
  `profile_summary` VARCHAR(255) NOT NULL DEFAULT '',
  `search_requests` INT          NOT NULL DEFAULT 0,
  `manifest`        LONGTEXT     NOT NULL,
  `profiles`        LONGTEXT     NOT NULL,
  `cases`           LONGTEXT     NOT NULL,
  `retired`         TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '关联文档删除后置 1，不再出现在列表/运行入口',
  `created_ms`      BIGINT       NOT NULL,
  PRIMARY KEY (`name`),
  KEY `idx_eval_dataset_retired` (`retired`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ③ 评测任务：元数据与汇总单独成列，dataset_snapshot 保存任务当时的完整题库快照。
-- 快照不随题库后续变化而变；历史任务因此仍可复核。
CREATE TABLE IF NOT EXISTS `eval_run` (
  `id`                 VARCHAR(64)  NOT NULL,
  `dataset_name`       VARCHAR(128) NOT NULL,
  `experiment_name`    VARCHAR(120) NOT NULL,
  `hypothesis`         VARCHAR(1000) NOT NULL DEFAULT '',
  `purpose`            VARCHAR(32)  NOT NULL,
  `status`             VARCHAR(32)  NOT NULL,
  `error`              VARCHAR(1000) NOT NULL DEFAULT '',
  `config_hash`        CHAR(64)     NOT NULL DEFAULT '',
  `metadata`           LONGTEXT     NOT NULL,
  `dataset_snapshot`   LONGTEXT     NOT NULL,
  `profiles`           LONGTEXT     NOT NULL COMMENT '仅配置，列表展示用，避免解析完整快照',
  `expected`           INT          NOT NULL DEFAULT 0,
  `done`               INT          NOT NULL DEFAULT 0,
  `retrieval_failures` INT          NOT NULL DEFAULT 0,
  `boundary_failures`  INT          NOT NULL DEFAULT 0,
  `rerank_degraded`    INT          NOT NULL DEFAULT 0,
  `scoring_failures`   INT          NOT NULL DEFAULT 0,
  `summary`            LONGTEXT     NOT NULL,
  `created_ms`         BIGINT       NOT NULL,
  `finished_ms`        BIGINT       NULL,
  PRIMARY KEY (`id`),
  KEY `idx_eval_run_created` (`created_ms`),
  KEY `idx_eval_run_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ④ 逐题结果：一行一个 case/profile，payload 是完整 EvalRow JSON。
-- 单行写入让 100 题任务不必每次重写整份结果；id 自增保留追加顺序。
CREATE TABLE IF NOT EXISTS `eval_run_row` (
  `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `run_id`   VARCHAR(64)  NOT NULL,
  `case_id`  VARCHAR(128) NOT NULL,
  `profile`  VARCHAR(64)  NOT NULL,
  `payload`  LONGTEXT     NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_eval_run_row` (`run_id`, `case_id`, `profile`),
  KEY `idx_eval_run_row_run` (`run_id`),
  CONSTRAINT `fk_eval_run_row_run` FOREIGN KEY (`run_id`) REFERENCES `eval_run` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ⑤ 题库草稿：items 是逐题 JSON，版本号用于人工复核的并发冲突检测。
CREATE TABLE IF NOT EXISTS `eval_draft` (
  `id`               VARCHAR(64)  NOT NULL,
  `tenant`           VARCHAR(64)  NOT NULL,
  `kb_no`            VARCHAR(64)  NOT NULL,
  `version`          INT          NOT NULL DEFAULT 1,
  `auto_generate`    TINYINT(1)   NOT NULL DEFAULT 0,
  `generation_state` VARCHAR(32)  NOT NULL DEFAULT '',
  `generation_error` VARCHAR(500) NOT NULL DEFAULT '',
  `items`            LONGTEXT     NOT NULL,
  `created_ms`       BIGINT       NOT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_eval_draft_scope` (`tenant`, `kb_no`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ⑥ 基线：一个题库版本一条，删除任务时级联删除。
CREATE TABLE IF NOT EXISTS `eval_baseline` (
  `dataset_name` VARCHAR(128) NOT NULL,
  `run_id`       VARCHAR(64)  NOT NULL,
  `created_ms`   BIGINT       NOT NULL,
  PRIMARY KEY (`dataset_name`),
  KEY `idx_eval_baseline_run` (`run_id`),
  CONSTRAINT `fk_eval_baseline_run` FOREIGN KEY (`run_id`) REFERENCES `eval_run` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ⑦ 验收报告：payload 是完整 EvalComparison JSON；删除任务时级联删除。
CREATE TABLE IF NOT EXISTS `eval_comparison` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `run_id`      VARCHAR(64)  NOT NULL,
  `baseline_id` VARCHAR(64)  NOT NULL DEFAULT '',
  `verdict`     VARCHAR(32)  NOT NULL,
  `payload`     LONGTEXT     NOT NULL,
  `created_ms`  BIGINT       NOT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_eval_comparison_run` (`run_id`),
  CONSTRAINT `fk_eval_comparison_run` FOREIGN KEY (`run_id`) REFERENCES `eval_run` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
