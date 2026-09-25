-- 合同多人顺序签署：签署方增加顺序号与签署状态（MySQL 8.0）
USE contractapi;
SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;

ALTER TABLE contract_signers
    ADD COLUMN seq INT NOT NULL DEFAULT 0 AFTER contract_id,
    ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'pending' AFTER role;

-- 存量数据补齐：已留下签署时间的记录视为已签署。
UPDATE contract_signers SET status = 'signed' WHERE signed_at IS NOT NULL;

-- 存量数据补齐：按创建顺序为每份合同的签署名单生成顺序号。
UPDATE contract_signers cs
JOIN (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY contract_id ORDER BY id) AS rn
    FROM contract_signers
) ranked ON ranked.id = cs.id
SET cs.seq = ranked.rn;
