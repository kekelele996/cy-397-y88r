package repository

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

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
	// CompleteIfPending 仅当合同仍处于待签署状态时置为已签署，否则返回 ErrConcurrentModification。
	CompleteIfPending(contract *model.Contract) error
	AddSigner(signer *model.ContractSigner) error
	// UpdateSigner 仅当签署方仍处于待签署状态时更新签署结果，否则返回 ErrConcurrentModification。
	UpdateSigner(signer *model.ContractSigner) error
	DeleteSigners(contractID uint64) error
	ListSigners(contractID uint64) ([]model.ContractSigner, error)
	// WithTransaction 在单个事务内执行多个仓储操作，fn 返回错误时整体回滚。
	WithTransaction(fn func(txRepo ContractRepository) error) error
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

func (r *contractRepository) CompleteIfPending(contract *model.Contract) error {
	result := r.db.Model(&model.Contract{}).
		Where("id = ? AND status = ?", contract.ID, constants.ContractStatusPendingSign).
		Updates(map[string]any{
			"status":    constants.ContractStatusSigned,
			"signed_at": contract.SignedAt,
		})
	if result.Error != nil {
		return fmt.Errorf("complete contract: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrConcurrentModification
	}
	contract.Status = constants.ContractStatusSigned
	return nil
}

func (r *contractRepository) AddSigner(signer *model.ContractSigner) error {
	if err := r.db.Create(signer).Error; err != nil {
		return fmt.Errorf("add contract signer: %w", err)
	}
	return nil
}

func (r *contractRepository) UpdateSigner(signer *model.ContractSigner) error {
	result := r.db.Model(&model.ContractSigner{}).
		Where("id = ? AND status = ?", signer.ID, constants.SignerStatusPending).
		Updates(map[string]any{
			"status":    signer.Status,
			"signed_at": signer.SignedAt,
			"sign_info": signer.SignInfo,
		})
	if result.Error != nil {
		return fmt.Errorf("update contract signer: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrConcurrentModification
	}
	return nil
}

func (r *contractRepository) DeleteSigners(contractID uint64) error {
	if err := r.db.Where("contract_id = ?", contractID).Delete(&model.ContractSigner{}).Error; err != nil {
		return fmt.Errorf("delete contract signers: %w", err)
	}
	return nil
}

func (r *contractRepository) ListSigners(contractID uint64) ([]model.ContractSigner, error) {
	var list []model.ContractSigner
	if err := r.db.Where("contract_id = ?", contractID).Order("seq ASC, id ASC").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("list contract signers: %w", err)
	}
	return list, nil
}

func (r *contractRepository) WithTransaction(fn func(txRepo ContractRepository) error) error {
	if err := r.db.Transaction(func(tx *gorm.DB) error {
		return fn(NewContractRepository(tx))
	}); err != nil {
		return fmt.Errorf("contract transaction: %w", err)
	}
	return nil
}
