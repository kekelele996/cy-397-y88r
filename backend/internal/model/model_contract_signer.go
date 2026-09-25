package model

import "time"

// ContractSigner 合同签署方信息，按 Seq 顺序逐个签署。
type ContractSigner struct {
	ID         uint64     `gorm:"primaryKey" json:"id"`
	ContractID uint64     `gorm:"index;not null" json:"contract_id"`
	Seq        int        `gorm:"not null;default:0" json:"seq"`
	Name       string     `gorm:"size:128;not null" json:"name"`
	Role       string     `gorm:"size:64;not null" json:"role"`
	Status     string     `gorm:"size:16;not null;default:pending" json:"status"`
	SignedAt   *time.Time `json:"signed_at,omitempty"`
	SignInfo   string     `gorm:"size:512" json:"sign_info"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (ContractSigner) TableName() string { return "contract_signers" }
