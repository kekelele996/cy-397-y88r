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

func submitTestContract(t *testing.T, svc *service.ContractService) uint64 {
	t.Helper()
	contract, err := svc.Create(1, dto.CreateContractRequest{
		TemplateID: 1,
		Title:      "顺序签署测试",
		Variables:  map[string]string{"party_a": "张三", "party_b": "李四", "amount": "3000"},
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if err := svc.Submit(1, contract.ID, []dto.SignerInput{
		{Name: "张三", Role: "甲方"},
		{Name: "李四", Role: "乙方"},
		{Name: "王五", Role: "见证方"},
	}); err != nil {
		t.Fatalf("Submit() unexpected error: %v", err)
	}
	return contract.ID
}

func TestContractServiceSubmitRecordsSignerOrder(t *testing.T) {
	_, _, svc := newContractFixture()
	contractID := submitTestContract(t, svc)

	got, _, err := svc.GetForUser(1, contractID)
	if err != nil {
		t.Fatalf("GetForUser() unexpected error: %v", err)
	}
	if got.Status != constants.ContractStatusPendingSign {
		t.Fatalf("status after submit = %q, want pending_signed", got.Status)
	}
	signers, err := svc.ListSigners(1, contractID)
	if err != nil {
		t.Fatalf("ListSigners() unexpected error: %v", err)
	}
	if len(signers) != 3 {
		t.Fatalf("ListSigners() len = %d, want 3", len(signers))
	}
	wantOrder := []struct {
		name  string
		role  string
		order int
	}{
		{"张三", "甲方", 1},
		{"李四", "乙方", 2},
		{"王五", "见证方", 3},
	}
	for i, want := range wantOrder {
		if signers[i].Name != want.name || signers[i].Role != want.role || signers[i].SignOrder != want.order {
			t.Fatalf("signers[%d] = %+v, want name=%q role=%q order=%d", i, signers[i], want.name, want.role, want.order)
		}
		if signers[i].SignedAt != nil {
			t.Fatalf("signer %q should not be signed at submit time", want.name)
		}
	}
}

func TestContractServiceSequentialSign(t *testing.T) {
	_, _, svc := newContractFixture()
	contractID := submitTestContract(t, svc)

	// 第一位签署后：合同仍待签署，但该签署方留下签署时间。
	first, err := svc.Sign(1, contractID, "张三", "甲方", "本人确认")
	if err != nil {
		t.Fatalf("Sign(first) unexpected error: %v", err)
	}
	if first.Status != constants.ContractStatusPendingSign || first.Completed {
		t.Fatalf("after first sign status=%q completed=%v, want pending_signed/false", first.Status, first.Completed)
	}
	if first.SignedCount != 1 || first.TotalCount != 3 {
		t.Fatalf("after first sign progress=%d/%d, want 1/3", first.SignedCount, first.TotalCount)
	}
	if first.Signer.SignedAt == nil {
		t.Fatal("first signer SignedAt should be recorded")
	}
	got, _, err := svc.GetForUser(1, contractID)
	if err != nil {
		t.Fatalf("GetForUser() unexpected error: %v", err)
	}
	if got.Status != constants.ContractStatusPendingSign || got.SignedAt != nil {
		t.Fatalf("contract after first sign = status %q signed_at %v, want pending_signed/nil", got.Status, got.SignedAt)
	}

	// 第二位签署。
	second, err := svc.Sign(1, contractID, "李四", "", "")
	if err != nil {
		t.Fatalf("Sign(second) unexpected error: %v", err)
	}
	if second.Status != constants.ContractStatusPendingSign || second.Completed {
		t.Fatalf("after second sign status=%q completed=%v, want pending_signed/false", second.Status, second.Completed)
	}
	if second.SignedCount != 2 {
		t.Fatalf("after second sign SignedCount=%d, want 2", second.SignedCount)
	}

	// 最后一位签署：合同完成，记录最终完成时间。
	third, err := svc.Sign(1, contractID, "王五", "见证方", "")
	if err != nil {
		t.Fatalf("Sign(third) unexpected error: %v", err)
	}
	if third.Status != constants.ContractStatusSigned || !third.Completed {
		t.Fatalf("after last sign status=%q completed=%v, want signed/true", third.Status, third.Completed)
	}
	if third.SignedCount != 3 || third.TotalCount != 3 {
		t.Fatalf("after last sign progress=%d/%d, want 3/3", third.SignedCount, third.TotalCount)
	}
	got, _, err = svc.GetForUser(1, contractID)
	if err != nil {
		t.Fatalf("GetForUser() unexpected error: %v", err)
	}
	if got.Status != constants.ContractStatusSigned {
		t.Fatalf("final status = %q, want signed", got.Status)
	}
	if got.SignedAt == nil {
		t.Fatal("final SignedAt should be recorded after all signers signed")
	}
	signers, _ := svc.ListSigners(1, contractID)
	for _, signer := range signers {
		if signer.SignedAt == nil {
			t.Fatalf("signer %q SignedAt should be recorded", signer.Name)
		}
	}
}

func TestContractServiceSignRejectsOutOfTurnAndRepeated(t *testing.T) {
	tests := []struct {
		name       string
		signerName string
		signerRole string
		prepare    func(t *testing.T, svc *service.ContractService, contractID uint64)
	}{
		{
			name:       "later signer signs before predecessor",
			signerName: "李四",
			signerRole: "乙方",
		},
		{
			name:       "last signer signs before anyone",
			signerName: "王五",
			signerRole: "见证方",
		},
		{
			name:       "signer not in list",
			signerName: "赵六",
			signerRole: "甲方",
		},
		{
			name:       "role mismatch",
			signerName: "张三",
			signerRole: "乙方",
		},
		{
			name:       "repeated sign after completion",
			signerName: "张三",
			signerRole: "甲方",
			prepare: func(t *testing.T, svc *service.ContractService, contractID uint64) {
				t.Helper()
				for _, signer := range []struct{ name, role string }{
					{"张三", "甲方"}, {"李四", "乙方"}, {"王五", "见证方"},
				} {
					if _, err := svc.Sign(1, contractID, signer.name, signer.role, ""); err != nil {
						t.Fatalf("prepare Sign(%s) unexpected error: %v", signer.name, err)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, svc := newContractFixture()
			contractID := submitTestContract(t, svc)
			if tt.prepare != nil {
				tt.prepare(t, svc, contractID)
			}
			if _, err := svc.Sign(1, contractID, tt.signerName, tt.signerRole, ""); err == nil {
				t.Fatalf("Sign(%q) expected error, got nil", tt.signerName)
			}
		})
	}
}

func TestContractServiceSubmitValidation(t *testing.T) {
	_, _, svc := newContractFixture()
	contract, err := svc.Create(1, dto.CreateContractRequest{
		TemplateID: 1,
		Title:      "提交校验",
		Variables:  map[string]string{"party_a": "张三", "party_b": "李四", "amount": "3000"},
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	tests := []struct {
		name    string
		signers []dto.SignerInput
	}{
		{"empty signers", nil},
		{"blank name", []dto.SignerInput{{Name: "  ", Role: "甲方"}}},
		{"blank role", []dto.SignerInput{{Name: "张三", Role: ""}}},
		{"duplicate name", []dto.SignerInput{{Name: "张三", Role: "甲方"}, {Name: "张三", Role: "乙方"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := svc.Submit(1, contract.ID, tt.signers); err == nil {
				t.Fatalf("Submit(%s) expected error, got nil", tt.name)
			}
		})
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
	if _, err := svc.Sign(1, contract.ID, "张三", "甲方", ""); err == nil {
		t.Fatal("Sign() expected error when status is draft, got nil")
	}
}
