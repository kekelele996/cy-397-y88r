package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/contractapi/contractapi/internal/constants"
	"github.com/contractapi/contractapi/internal/model"
)

func TestContractRepository(t *testing.T) {
	repo := NewContractRepository(newTestDB(t))

	contract := &model.Contract{
		UserID:     1,
		TemplateID: 1,
		Title:      "测试合同",
		Status:     "draft",
		Variables:  model.JSONMap{"party_a": "张三"},
	}
	if err := repo.Create(contract); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.FindByIDForUser(contract.ID, 1)
	if err != nil || got.ID != contract.ID {
		t.Fatalf("FindByIDForUser() = %+v, %v", got, err)
	}
	if _, err := repo.FindByIDForUser(contract.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByIDForUser(other) error = %v, want ErrNotFound", err)
	}

	list, total, err := repo.ListByUser(1, "draft", 0, 10)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("ListByUser() = %d, %d, %v", len(list), total, err)
	}

	now := time.Now()
	contract.Status = "signed"
	contract.SignedAt = &now
	if err := repo.Update(contract); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	signer := &model.ContractSigner{ContractID: contract.ID, Name: "张三", Role: "甲方", SignedAt: &now}
	if err := repo.AddSigner(signer); err != nil {
		t.Fatalf("AddSigner() error = %v", err)
	}
	signers, err := repo.ListSigners(contract.ID)
	if err != nil || len(signers) != 1 {
		t.Fatalf("ListSigners() = %d, %v", len(signers), err)
	}
}

// TestContractRepositorySequentialSigningInTransaction 验证顺序签署的持久化流程：
// 条件更新防重复签署，最后一位完成时合同原子置为已签署。
func TestContractRepositorySequentialSigningInTransaction(t *testing.T) {
	repo := NewContractRepository(newTestDB(t))

	contract := &model.Contract{UserID: 1, TemplateID: 1, Title: "顺序签署合同", Status: constants.ContractStatusPendingSign}
	if err := repo.Create(contract); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	signerRecords := []*model.ContractSigner{
		{ContractID: contract.ID, SignOrder: 1, Name: "张三", Role: "甲方"},
		{ContractID: contract.ID, SignOrder: 2, Name: "李四", Role: "乙方"},
	}
	for _, signer := range signerRecords {
		if err := repo.AddSigner(signer); err != nil {
			t.Fatalf("AddSigner() error = %v", err)
		}
	}

	now := time.Now()
	if err := repo.InTransaction(func(txRepo ContractRepository) error {
		locked, err := txRepo.FindByIDForUpdate(contract.ID)
		if err != nil {
			return err
		}
		if locked.Status != constants.ContractStatusPendingSign {
			t.Fatalf("locked contract status = %q, want pending_signed", locked.Status)
		}
		lockedSigners, err := txRepo.ListSignersForUpdate(contract.ID)
		if err != nil {
			return err
		}
		if len(lockedSigners) != 2 || lockedSigners[0].SignOrder != 1 || lockedSigners[0].Name != "张三" {
			t.Fatalf("locked signers = %+v, want order 张三(1), 李四(2)", lockedSigners)
		}
		return txRepo.MarkSignerSigned(lockedSigners[0].ID, now, "ok")
	}); err != nil {
		t.Fatalf("first sign transaction error = %v", err)
	}

	// 非最后一位签署时，合同仍为待签署、无最终完成时间。
	got, err := repo.FindByID(contract.ID)
	if err != nil || got.Status != constants.ContractStatusPendingSign || got.SignedAt != nil {
		t.Fatalf("after first sign contract = %+v, %v; want pending_signed without signed_at", got, err)
	}

	// 第一位不能重复签署。
	if err := repo.MarkSignerSigned(signerRecords[0].ID, now, "again"); !errors.Is(err, ErrAlreadySigned) {
		t.Fatalf("repeat MarkSignerSigned() error = %v, want ErrAlreadySigned", err)
	}

	// 最后一位签署并在同一事务内完成合同。
	if err := repo.InTransaction(func(txRepo ContractRepository) error {
		lockedSigners, err := txRepo.ListSignersForUpdate(contract.ID)
		if err != nil {
			return err
		}
		if err := txRepo.MarkSignerSigned(lockedSigners[1].ID, now, "ok"); err != nil {
			return err
		}
		return txRepo.MarkContractSigned(contract.ID, now)
	}); err != nil {
		t.Fatalf("second sign transaction error = %v", err)
	}

	got, err = repo.FindByID(contract.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.Status != constants.ContractStatusSigned || got.SignedAt == nil {
		t.Fatalf("after final sign contract = status %q signed_at %v, want signed with signed_at", got.Status, got.SignedAt)
	}

	// 已完成的合同不能再次标记完成。
	if err := repo.MarkContractSigned(contract.ID, now); !errors.Is(err, ErrAlreadySigned) {
		t.Fatalf("repeat MarkContractSigned() error = %v, want ErrAlreadySigned", err)
	}

	// 签署方按顺位返回，且均带签署时间。
	signers, err := repo.ListSigners(contract.ID)
	if err != nil {
		t.Fatalf("ListSigners() error = %v", err)
	}
	if len(signers) != 2 || signers[0].SignedAt == nil || signers[1].SignedAt == nil {
		t.Fatalf("signers after completion = %+v, want both signed in order", signers)
	}
}
