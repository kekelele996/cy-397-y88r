package service_test

import (
	"strings"
	"testing"

	"github.com/contractapi/contractapi/internal/constants"
	"github.com/contractapi/contractapi/internal/dto"
	"github.com/contractapi/contractapi/internal/model"
	"github.com/contractapi/contractapi/internal/service"
)

func newContractFixture() (*mockTemplateRepo, *mockContractRepo, *service.ContractService) {
	templates := newMockTemplateRepo()
	templates.add(&model.ContractTemplate{
		Code:        "lease",
		Name:        "房屋租赁合同",
		Category:    constants.TemplateCategoryLease,
		Content:     "甲方：{{.party_a}}\n乙方：{{.party_b}}\n金额：{{.amount}}",
		ContentHTML: "<p>甲方：{{.party_a}}</p><p>乙方：{{.party_b}}</p><p>金额：{{.amount}}</p>",
		Variables: model.TemplateVariables{
			{Name: "party_a", Label: "甲方", Required: true},
			{Name: "party_b", Label: "乙方", Required: true},
			{Name: "amount", Label: "金额", Required: true},
		},
	})
	contracts := newMockContractRepo()
	svc := service.NewContractService(contracts, templates, service.NewPDFService(testLogger()), testLogger())
	return templates, contracts, svc
}

func TestContractServiceCreate(t *testing.T) {
	tests := []struct {
		name      string
		variables map[string]string
		wantErr   bool
		wantText  string
	}{
		{
			name:      "success",
			variables: map[string]string{"party_a": "张三", "party_b": "李四", "amount": "3000"},
			wantErr:   false,
			wantText:  "甲方：张三",
		},
		{
			name:      "missing required",
			variables: map[string]string{"party_a": "张三"},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, svc := newContractFixture()
			contract, err := svc.Create(1, dto.CreateContractRequest{
				TemplateID: 1,
				Title:      "测试合同",
				Variables:  tt.variables,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Create() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if contract.Status != constants.ContractStatusDraft {
				t.Fatalf("Create() status = %q, want draft", contract.Status)
			}
			if !strings.Contains(contract.ContentText, tt.wantText) {
				t.Fatalf("Create() content = %q, want contains %q", contract.ContentText, tt.wantText)
			}
			if !strings.Contains(contract.ContentHTML, "甲方：张三") {
				t.Fatalf("Create() html = %q, want contains rendered html", contract.ContentHTML)
			}
		})
	}
}

func TestContractServiceSubmitAndSign(t *testing.T) {
	_, _, svc := newContractFixture()
	contract, err := svc.Create(1, dto.CreateContractRequest{
		TemplateID: 1,
		Title:      "签署测试",
		Variables:  map[string]string{"party_a": "张三", "party_b": "李四", "amount": "3000"},
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	if err := svc.Submit(1, contract.ID, []dto.SignerInput{{Name: "张三", Role: "甲方"}, {Name: "李四", Role: "乙方"}}); err != nil {
		t.Fatalf("Submit() unexpected error: %v", err)
	}
	got, _, err := svc.GetForUser(1, contract.ID)
	if err != nil {
		t.Fatalf("GetForUser() unexpected error: %v", err)
	}
	if got.Status != constants.ContractStatusPendingSign {
		t.Fatalf("status after submit = %q, want pending_signed", got.Status)
	}
	signers, err := svc.ListSigners(1, contract.ID)
	if err != nil || len(signers) != 2 {
		t.Fatalf("ListSigners() = %d, %v; want 2 signers", len(signers), err)
	}
	for i, signer := range signers {
		if signer.Seq != i+1 {
			t.Fatalf("signer[%d].Seq = %d, want %d", i, signer.Seq, i+1)
		}
		if signer.Status != constants.SignerStatusPending {
			t.Fatalf("signer[%d].Status = %q, want pending", i, signer.Status)
		}
	}

	// 张三签署后，李四尚未签署，合同仍为待签署。
	status, err := svc.Sign(1, contract.ID, "张三", "本人确认")
	if err != nil {
		t.Fatalf("Sign(张三) unexpected error: %v", err)
	}
	if status != constants.ContractStatusPendingSign {
		t.Fatalf("status after first sign = %q, want pending_signed", status)
	}
	got, _, err = svc.GetForUser(1, contract.ID)
	if err != nil {
		t.Fatalf("GetForUser() unexpected error: %v", err)
	}
	if got.Status != constants.ContractStatusPendingSign {
		t.Fatalf("contract status after first sign = %q, want pending_signed", got.Status)
	}
	if got.SignedAt != nil {
		t.Fatal("contract SignedAt should be empty before all signers signed")
	}
	signers, err = svc.ListSigners(1, contract.ID)
	if err != nil {
		t.Fatalf("ListSigners() unexpected error: %v", err)
	}
	if signers[0].Status != constants.SignerStatusSigned || signers[0].SignedAt == nil {
		t.Fatalf("first signer = %+v, want signed with signed_at", signers[0])
	}
	if signers[1].Status != constants.SignerStatusPending || signers[1].SignedAt != nil {
		t.Fatalf("second signer = %+v, want pending without signed_at", signers[1])
	}

	// 李四签署后，名单全部完成，合同置为已签署并记录完成时间。
	status, err = svc.Sign(1, contract.ID, "李四", "本人确认")
	if err != nil {
		t.Fatalf("Sign(李四) unexpected error: %v", err)
	}
	if status != constants.ContractStatusSigned {
		t.Fatalf("status after final sign = %q, want signed", status)
	}
	got, _, err = svc.GetForUser(1, contract.ID)
	if err != nil {
		t.Fatalf("GetForUser() unexpected error: %v", err)
	}
	if got.Status != constants.ContractStatusSigned {
		t.Fatalf("contract status after final sign = %q, want signed", got.Status)
	}
	if got.SignedAt == nil {
		t.Fatal("contract SignedAt should be recorded after all signers signed")
	}
	signers, err = svc.ListSigners(1, contract.ID)
	if err != nil {
		t.Fatalf("ListSigners() unexpected error: %v", err)
	}
	for i, signer := range signers {
		if signer.Status != constants.SignerStatusSigned || signer.SignedAt == nil {
			t.Fatalf("signer[%d] = %+v, want signed with signed_at", i, signer)
		}
	}
}

func TestContractServiceSignOutOfTurn(t *testing.T) {
	_, _, svc := newContractFixture()
	contract, err := svc.Create(1, dto.CreateContractRequest{
		TemplateID: 1,
		Title:      "顺序签署测试",
		Variables:  map[string]string{"party_a": "张三", "party_b": "李四", "amount": "3000"},
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if err := svc.Submit(1, contract.ID, []dto.SignerInput{{Name: "张三", Role: "甲方"}, {Name: "李四", Role: "乙方"}}); err != nil {
		t.Fatalf("Submit() unexpected error: %v", err)
	}

	// 前一位未签署时，后一位不能先签。
	if _, err := svc.Sign(1, contract.ID, "李四", ""); err == nil {
		t.Fatal("Sign(李四) expected error when 张三 has not signed, got nil")
	}
	// 不在名单中的签署人不能签署。
	if _, err := svc.Sign(1, contract.ID, "王五", ""); err == nil {
		t.Fatal("Sign(王五) expected error for signer not in list, got nil")
	}
	// 签署失败不应留下任何签署记录。
	signers, err := svc.ListSigners(1, contract.ID)
	if err != nil {
		t.Fatalf("ListSigners() unexpected error: %v", err)
	}
	for i, signer := range signers {
		if signer.Status != constants.SignerStatusPending || signer.SignedAt != nil {
			t.Fatalf("signer[%d] = %+v, want untouched pending signer", i, signer)
		}
	}

	if _, err := svc.Sign(1, contract.ID, "张三", ""); err != nil {
		t.Fatalf("Sign(张三) unexpected error: %v", err)
	}
	// 同一人不能重复签署。
	if _, err := svc.Sign(1, contract.ID, "张三", ""); err == nil {
		t.Fatal("Sign(张三) expected error for duplicate signing, got nil")
	}
	if _, err := svc.Sign(1, contract.ID, "李四", ""); err != nil {
		t.Fatalf("Sign(李四) unexpected error: %v", err)
	}
	// 全部签署完成后，合同不再接受签署。
	if _, err := svc.Sign(1, contract.ID, "张三", ""); err == nil {
		t.Fatal("Sign() expected error after contract fully signed, got nil")
	}
}

func TestContractServiceSubmitValidation(t *testing.T) {
	_, _, svc := newContractFixture()
	contract, err := svc.Create(1, dto.CreateContractRequest{
		TemplateID: 1,
		Title:      "提交校验测试",
		Variables:  map[string]string{"party_a": "张三", "party_b": "李四", "amount": "3000"},
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if err := svc.Submit(1, contract.ID, nil); err == nil {
		t.Fatal("Submit() expected error for empty signers, got nil")
	}
	if err := svc.Submit(1, contract.ID, []dto.SignerInput{{Name: "张三", Role: "甲方"}, {Name: "张三", Role: "乙方"}}); err == nil {
		t.Fatal("Submit() expected error for duplicate signer names, got nil")
	}
	// 校验失败后合同仍停留在草稿状态。
	got, _, err := svc.GetForUser(1, contract.ID)
	if err != nil {
		t.Fatalf("GetForUser() unexpected error: %v", err)
	}
	if got.Status != constants.ContractStatusDraft {
		t.Fatalf("status after failed submit = %q, want draft", got.Status)
	}
}

func TestContractServiceInvalidTransition(t *testing.T) {
	_, _, svc := newContractFixture()
	contract, err := svc.Create(1, dto.CreateContractRequest{
		TemplateID: 1,
		Title:      "流转测试",
		Variables:  map[string]string{"party_a": "张三", "party_b": "李四", "amount": "3000"},
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	// 草稿状态不能签署。
	if _, err := svc.Sign(1, contract.ID, "张三", ""); err == nil {
		t.Fatal("Sign() expected error when status is draft, got nil")
	}
	if err := svc.Submit(1, contract.ID, []dto.SignerInput{{Name: "张三", Role: "甲方"}}); err != nil {
		t.Fatalf("Submit() unexpected error: %v", err)
	}
	// 单人名单签署完成后合同即已签署，再次签署应报错。
	if _, err := svc.Sign(1, contract.ID, "张三", ""); err != nil {
		t.Fatalf("Sign() unexpected error: %v", err)
	}
	if _, err := svc.Sign(1, contract.ID, "张三", ""); err == nil {
		t.Fatal("Sign() expected error when contract already signed, got nil")
	}
}
