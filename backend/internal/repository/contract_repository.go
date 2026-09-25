package repository

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/contractapi/contractapi/internal/constants"
	"github.com/contractapi/contractapi/internal/model"
)

// ContractRepository 合同数据访问接口。
type ContractRepository interface {
	Create(contract *model.Contract) error
	FindByID(id uint64) (*model.Contract, error)
	FindByIDForUser(id, userID uint64) (*model.Contract, error)
	ListByUser(userID uint64, status string, offset, limit int) ([]model.Contract, int64, error)
	Update(contract *model.Contract) error
	AddSigner(signer *model.ContractSigner) error
	ListSigners(contractID uint64) ([]model.ContractSigner, error)
	// InTransaction 在一个数据库事务中执行 fn，fn 返回错误时回滚，否则提交。
	InTransaction(fn func(txRepo ContractRepository) error) error
	// FindByIDForUpdate 查询合同并对该行加写锁（FOR UPDATE），须在事务中调用。
	FindByIDForUpdate(id uint64) (*model.Contract, error)
	// ListSignersForUpdate 按签署顺位查询签署方并对各行加写锁，须在事务中调用。
	ListSignersForUpdate(contractID uint64) ([]model.ContractSigner, error)
	// MarkSignerSigned 仅在签署方尚未签署时记录其签署时间与签署信息。
	MarkSignerSigned(signerID uint64, signedAt time.Time, signInfo string) error
	// MarkContractSigned 仅在合同仍处于待签署状态时置为已签署并记录最终完成时间。
	MarkContractSigned(contractID uint64, signedAt time.Time) error
}

type contractRepository struct {
	db *gorm.DB
}

// NewContractRepository 构造合同仓储。
func NewContractRepository(db *gorm.DB) ContractRepository {
	return &contractRepository{db: db}
}

func (r *contractRepository) Create(contract *model.Contract) error {
	if err := r.db.Create(contract).Error; err != nil {
		return fmt.Errorf("create contract: %w", err)
	}
	return nil
}

func (r *contractRepository) FindByID(id uint64) (*model.Contract, error) {
	var contract model.Contract
	if err := r.db.First(&contract, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find contract by id: %w", err)
	}
	return &contract, nil
}

func (r *contractRepository) FindByIDForUser(id, userID uint64) (*model.Contract, error) {
	var contract model.Contract
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&contract).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find contract by id and user: %w", err)
	}
	return &contract, nil
}

func (r *contractRepository) ListByUser(userID uint64, status string, offset, limit int) ([]model.Contract, int64, error) {
	query := r.db.Model(&model.Contract{}).Where("user_id = ?", userID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count contracts: %w", err)
	}
	var list []model.Contract
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("list contracts: %w", err)
	}
	return list, total, nil
}

func (r *contractRepository) Update(contract *model.Contract) error {
	if err := r.db.Save(contract).Error; err != nil {
		return fmt.Errorf("update contract: %w", err)
	}
	return nil
}

func (r *contractRepository) AddSigner(signer *model.ContractSigner) error {
	if err := r.db.Create(signer).Error; err != nil {
		return fmt.Errorf("add contract signer: %w", err)
	}
	return nil
}

func (r *contractRepository) ListSigners(contractID uint64) ([]model.ContractSigner, error) {
	var list []model.ContractSigner
	if err := r.db.Where("contract_id = ?", contractID).
		Order("sign_order ASC, id ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list contract signers: %w", err)
	}
	return list, nil
}

func (r *contractRepository) InTransaction(fn func(txRepo ContractRepository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		return fn(&contractRepository{db: tx})
	})
}

func (r *contractRepository) FindByIDForUpdate(id uint64) (*model.Contract, error) {
	var contract model.Contract
	// FOR UPDATE 串行化同一合同上的并发签署；SQLite 驱动会忽略该子句。
	if err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&contract, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find contract for update: %w", err)
	}
	return &contract, nil
}

func (r *contractRepository) ListSignersForUpdate(contractID uint64) ([]model.ContractSigner, error) {
	var list []model.ContractSigner
	if err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("contract_id = ?", contractID).
		Order("sign_order ASC, id ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list contract signers for update: %w", err)
	}
	return list, nil
}

func (r *contractRepository) MarkSignerSigned(signerID uint64, signedAt time.Time, signInfo string) error {
	result := r.db.Model(&model.ContractSigner{}).
		Where("id = ? AND signed_at IS NULL", signerID).
		Updates(map[string]any{"signed_at": signedAt, "sign_info": signInfo})
	if result.Error != nil {
		return fmt.Errorf("mark contract signer signed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrAlreadySigned
	}
	return nil
}

func (r *contractRepository) MarkContractSigned(contractID uint64, signedAt time.Time) error {
	result := r.db.Model(&model.Contract{}).
		Where("id = ? AND status = ?", contractID, constants.ContractStatusPendingSign).
		Updates(map[string]any{"status": constants.ContractStatusSigned, "signed_at": signedAt})
	if result.Error != nil {
		return fmt.Errorf("mark contract signed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrAlreadySigned
	}
	return nil
}
