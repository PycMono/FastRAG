-- 一次性迁移（2026-10-12 实体简化）。只对**已存在**的库跑一次；
-- 新建库直接用 scripts/schema.sql，不需要它。
--
-- 本文件**不是**幂等脚本：ALTER 重复执行会报「列不存在」。跑之前先备份。
--
-- ⚠️ knowledge_base 的 DDL 归**建库方**（§1.2）：这个文件里的第 ③ 段是给建库方执行
-- 的变更单，本服务只读它、只回写 doc_count / chunk_count / update_ts 三列。
-- 两张表必须**一起**改：只改 knowledge_doc 而 knowledge_base 不动，
-- 服务会按新列名去查旧的表，读不到值或直接报错。

USE `fastrag`;

-- ① 幽灵行先物理删掉。
--    DROP COLUMN 之后 delete_ts 一没，这些行会「复活」成活跃文档，
--    而它们的切片早已从 ES 清掉、或正被存活校验当成幽灵丢弃——
--    留着就是一批搜得到名字、搜不到内容的空文档。
DELETE FROM `knowledge_doc` WHERE `delete_ts` > 0;

-- ② knowledge_doc：丢软删列。唯一键会自动缩成 (kb_id, name)；
--    但 idx_kb_delete 会缩成单列 (kb_id)、与 uk_kb_name 的左前缀重复，显式删掉。
ALTER TABLE `knowledge_doc`
  DROP INDEX `idx_kb_delete`,
  DROP COLUMN `delete_ts`;

-- ③ knowledge_base（外部表，归建库方）：丢四个字段 + knowledge_type 改名为 type。
--    uk_no (no, delete_ts) 会在删列时自动缩成 (no)。
ALTER TABLE `knowledge_base`
  DROP COLUMN `search_mode`,
  DROP COLUMN `biz_tag`,
  DROP COLUMN `is_system`,
  DROP COLUMN `delete_ts`,
  CHANGE COLUMN `knowledge_type` `type` VARCHAR(32) NOT NULL DEFAULT 'ordinary' COMMENT 'ordinary/qa';
