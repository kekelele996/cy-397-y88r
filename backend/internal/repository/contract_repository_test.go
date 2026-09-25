package repository

import (
	"errors"
	"testing"
	"time"

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
	contract.Status = "pending_signed"
	if err := repo.Update(contract); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	signer := &model.ContractSigner{ContractID: contract.ID, Seq: 1, Name: "张三", Role: "甲方", Status: "pending"}
	if err := repo.AddSigner(signer); err != nil {
		t.Fatalf("AddSigner() error = %v", err)
	}
	signer2 := &model.ContractSigner{ContractID: contract.ID, Seq: 2, Name: "李四", Role: "乙方", Status: "pending"}
	if err := repo.AddSigner(signer2); err != nil {
		t.Fatalf("AddSigner() error = %v", err)
	}

	// 仅当签署方仍处于待签署状态时才能写入签署结果。
	signer.Status = "signed"
	signer.SignedAt = &now
	signer.SignInfo = "本人确认"
	if err := repo.UpdateSigner(signer); err != nil {
		t.Fatalf("UpdateSigner() error = %v", err)
	}
	if err := repo.UpdateSigner(signer); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("UpdateSigner(again) error = %v, want ErrConcurrentModification", err)
	}

	signers, err := repo.ListSigners(contract.ID)
	if err != nil || len(signers) != 2 {
		t.Fatalf("ListSigners() = %d, %v", len(signers), err)
	}
	if signers[0].Name != "张三" || signers[0].Status != "signed" || signers[0].SignedAt == nil {
		t.Fatalf("ListSigners()[0] = %+v, want signed 张三", signers[0])
	}
	if signers[1].Name != "李四" || signers[1].Status != "pending" {
		t.Fatalf("ListSigners()[1] = %+v, want pending 李四", signers[1])
	}

	// 仅当合同仍处于待签署状态时才能置为已签署。
	contract.SignedAt = &now
	if err := repo.CompleteIfPending(contract); err != nil {
		t.Fatalf("CompleteIfPending() error = %v", err)
	}
	if contract.Status != "signed" {
		t.Fatalf("contract.Status = %q, want signed", contract.Status)
	}
	if err := repo.CompleteIfPending(contract); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("CompleteIfPending(again) error = %v, want ErrConcurrentModification", err)
	}

	if err := repo.DeleteSigners(contract.ID); err != nil {
		t.Fatalf("DeleteSigners() error = %v", err)
	}
	signers, err = repo.ListSigners(contract.ID)
	if err != nil || len(signers) != 0 {
		t.Fatalf("ListSigners() after delete = %d, %v", len(signers), err)
	}
}

func TestContractRepositoryWithTransaction(t *testing.T) {
	repo := NewContractRepository(newTestDB(t))

	contract := &model.Contract{
		UserID:     1,
		TemplateID: 1,
		Title:      "事务测试合同",
		Status:     "draft",
		Variables:  model.JSONMap{"party_a": "张三"},
	}
	if err := repo.Create(contract); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 事务内任一操作失败时整体回滚。
	err := repo.WithTransaction(func(txRepo ContractRepository) error {
		contract.Status = "pending_signed"
		if err := txRepo.Update(contract); err != nil {
			return err
		}
		if err := txRepo.AddSigner(&model.ContractSigner{ContractID: contract.ID, Seq: 1, Name: "张三", Role: "甲方", Status: "pending"}); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("WithTransaction() expected rollback error, got nil")
	}
	got, err := repo.FindByID(contract.ID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if got.Status != "draft" {
		t.Fatalf("contract status after rollback = %q, want draft", got.Status)
	}
	signers, err := repo.ListSigners(contract.ID)
	if err != nil || len(signers) != 0 {
		t.Fatalf("signers after rollback = %d, %v; want 0", len(signers), err)
	}
}
