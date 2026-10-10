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
-- **不建 knowledge_base**：知识库由外部系统预置（§1.2），本服务只读它、
-- 只回写 doc_count / chunk_count / update_ts 三列。它的 DDL 归建库方。
--
-- 刻意**没有**的东西 —— 这几条是防退化闸门，改动前先想清楚（对应设计文档 §4.1）：
--   · 切片表 —— 切片下沉 ES 是核心决策 D3；
--   · 租户路由表 —— 索引策略是单索引 + term(account)（D2）；
--   · 对象存储回灌所需的路径列 —— 本期不做权威副本；
--   · 发布台账 —— 本期不做发布与版本（§1.2）；
--   · 文档的代次列 —— 写侧一致性增强是 P2，本期不做（§13 待定 9）。

CREATE DATABASE IF NOT EXISTS `fastrag` DEFAULT CHARSET=utf8mb4;

USE `fastrag`;

-- ① 文档
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
