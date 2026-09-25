package model

import "time"

// ContractSigner 合同签署方信息。
type ContractSigner struct {
	ID         uint64 `gorm:"primaryKey" json:"id"`
	ContractID uint64 `gorm:"index;not null" json:"contract_id"`
	// SignOrder 为提交时填写的签署顺位，从 1 开始，签署按该顺序依次进行。
	SignOrder int        `gorm:"not null;default:0" json:"sign_order"`
	Name      string     `gorm:"size:128;not null" json:"name"`
	Role      string     `gorm:"size:64;not null" json:"role"`
	SignedAt  *time.Time `json:"signed_at,omitempty"`
	SignInfo  string     `gorm:"size:512" json:"sign_info"`
	CreatedAt time.Time  `json:"created_at"`
}

func (ContractSigner) TableName() string { return "contract_signers" }
