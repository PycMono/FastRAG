-- FastRAG 业务库 DDL —— 建表语句的**单一事实源**。
--
-- 由 scripts/init-db.sh 执行（make init-db）。本服务**不建表**：
-- 上线前先把库建好，运行期的连接账号也就不需要 DDL 权限。
--
-- 幂等：全部 CREATE ... IF NOT EXISTS，重复执行安全。
--
-- 两张表的设计约定见设计文档 §4.1：
--   ① knowledge_base —— 知识库，**外部预置**，本服务只写计数三列；
--   ② knowledge_doc  —— 文档，id 由应用侧（雪花）预分配，**没有** AUTO_INCREMENT。
--
-- 刻意**没有**的东西 —— 这几条是防退化闸门，改动前先想清楚：
--   · 切片表 —— 切片下沉 ES 是核心决策 D3；
--   · 租户路由表 —— 索引策略是单索引 + term(account)（D2）；
--   · 对象存储回灌所需的路径列 —— 本期不做权威副本；
--   · 发布台账 —— 本期不做发布与版本（§1.2）；
--   · 文档的代次列 —— 写侧一致性增强是 P2，本期不做（§13 待定 9）。
--
-- 上面这些约定由 `bash scripts/init-db.sh --check` 断言，改坏了会红。

CREATE DATABASE IF NOT EXISTS `fastrag` DEFAULT CHARSET=utf8mb4;

USE `fastrag`;

-- ① 知识库（外部预置；本服务只写 doc_count/chunk_count/update_ts 三列，见 §1 列所有权）
CREATE TABLE IF NOT EXISTS `knowledge_base` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `no`              VARCHAR(64)  NOT NULL COMMENT '对外编号',
  `account`         VARCHAR(64)  NOT NULL COMMENT '租户',
  `name`            VARCHAR(255) NOT NULL,
  `description`     VARCHAR(1024) DEFAULT '',
  `knowledge_type`  VARCHAR(32)  NOT NULL DEFAULT 'ordinary' COMMENT 'ordinary/qa',
  `search_mode`     VARCHAR(32)  NOT NULL DEFAULT 'title_and_content' COMMENT 'title/title_and_content',
  `biz_tag`         VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '业务标签，租户内再分域，单值',
  `split_options`   JSON         DEFAULT NULL COMMENT '切片参数快照',

  -- 冗余计数（列表页用，见 D3）
  `doc_count`       BIGINT       NOT NULL DEFAULT 0,
  `chunk_count`     BIGINT       NOT NULL DEFAULT 0,

  `is_system`       TINYINT      NOT NULL DEFAULT 0,
  `create_ts`       BIGINT       NOT NULL,
  `update_ts`       BIGINT       NOT NULL,
  `delete_ts`       BIGINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_no` (`no`, `delete_ts`),
  KEY `idx_account` (`account`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ② 文档
CREATE TABLE IF NOT EXISTS `knowledge_doc` (
  `id`              BIGINT UNSIGNED NOT NULL COMMENT '雪花 ID，写入前预分配',
  `kb_id`           BIGINT UNSIGNED NOT NULL,
  `account`         VARCHAR(64)  NOT NULL,
  `name`            VARCHAR(512) NOT NULL COMMENT '文档名。同名重导入 = 更新，doc_id 不变',
  `content_hash`    CHAR(64)     NOT NULL COMMENT '内容指纹',
  `chunk_count`     INT          NOT NULL DEFAULT 0,
  `create_ts`       BIGINT       NOT NULL,
  `update_ts`       BIGINT       NOT NULL,
  `delete_ts`       BIGINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_kb_name` (`kb_id`, `name`, `delete_ts`),
  KEY `idx_kb_delete` (`kb_id`, `delete_ts`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
