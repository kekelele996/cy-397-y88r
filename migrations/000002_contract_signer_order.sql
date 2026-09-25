-- 多人顺序签署：为签署方增加签署顺位字段。
-- 新提交的合同按提交名单顺序从 1 开始编号，签署必须按 sign_order 依次进行。
USE contractapi;

ALTER TABLE contract_signers
    ADD COLUMN sign_order INT NOT NULL DEFAULT 0
        COMMENT '提交时填写的签署顺位，从 1 开始，按顺位依次签署' AFTER contract_id;

-- 存量数据：按插入顺序（id）回填每个合同内的签署顺位，保持与名单顺序一致。
UPDATE contract_signers cs
JOIN (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY contract_id ORDER BY id ASC) AS rn
    FROM contract_signers
) ordered ON ordered.id = cs.id
SET cs.sign_order = ordered.rn;

CREATE INDEX idx_contract_signers_order ON contract_signers (contract_id, sign_order);
