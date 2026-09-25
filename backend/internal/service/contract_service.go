package service

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/contractapi/contractapi/internal/constants"
	"github.com/contractapi/contractapi/internal/dto"
	"github.com/contractapi/contractapi/internal/model"
	"github.com/contractapi/contractapi/internal/repository"
)

// ContractService 合同生成、签署状态流转与导出业务。
type ContractService struct {
	contractRepo repository.ContractRepository
	templateRepo repository.TemplateRepository
	pdf          *PDFService
	logger       *slog.Logger
}

// NewContractService 构造合同服务。
func NewContractService(
	contractRepo repository.ContractRepository,
	templateRepo repository.TemplateRepository,
	pdf *PDFService,
	logger *slog.Logger,
) *ContractService {
	return &ContractService{contractRepo: contractRepo, templateRepo: templateRepo, pdf: pdf, logger: logger}
}

// Create 根据模板与变量生成合同草稿。
func (s *ContractService) Create(userID uint64, req dto.CreateContractRequest) (*model.Contract, error) {
	templateModel, err := s.templateRepo.FindByID(req.TemplateID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, dto.NotFoundError("template not found")
		}
		return nil, fmt.Errorf("create contract: find template: %w", err)
	}
	variables := req.Variables
	if variables == nil {
		variables = map[string]string{}
	}
	missing := missingRequiredVariables(templateModel.Variables, variables)
	if len(missing) > 0 {
		return nil, dto.ValidationError("missing required variables: " + strings.Join(missing, ", "))
	}
	contentText, err := renderTextContract(templateModel.Content, variables)
	if err != nil {
		return nil, fmt.Errorf("create contract: render text: %w", err)
	}
	contentHTML, err := renderHTMLContract(templateModel.ContentHTML, contentText, variables)
	if err != nil {
		return nil, fmt.Errorf("create contract: render html: %w", err)
	}
	contract := &model.Contract{
		UserID:      userID,
		TemplateID:  templateModel.ID,
		Title:       req.Title,
		ContentText: contentText,
		ContentHTML: contentHTML,
		Status:      constants.ContractStatusDraft,
		Variables:   model.JSONMap(variables),
	}
	if err := s.contractRepo.Create(contract); err != nil {
		return nil, fmt.Errorf("create contract: save: %w", err)
	}
	s.logger.Info("contract created", "contract_id", contract.ID, "user_id", userID, "template_id", templateModel.ID)
	return contract, nil
}

// GetForUser 查询用户自己的合同，并返回模板名称。
func (s *ContractService) GetForUser(userID, contractID uint64) (*model.Contract, string, error) {
	contract, err := s.contractRepo.FindByIDForUser(contractID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, "", dto.NotFoundError("contract not found")
		}
		return nil, "", fmt.Errorf("get contract: %w", err)
	}
	templateName := ""
	if templateModel, err := s.templateRepo.FindByID(contract.TemplateID); err == nil {
		templateName = templateModel.Name
	}
	return contract, templateName, nil
}

// ListForUser 查询用户合同库，支持按状态筛选。
func (s *ContractService) ListForUser(userID uint64, status string, page, pageSize int) ([]model.Contract, int64, error) {
	if status != "" && !constants.IsValidContractStatus(status) {
		return nil, 0, dto.ValidationError("invalid contract status")
	}
	offset := (page - 1) * pageSize
	list, total, err := s.contractRepo.ListByUser(userID, status, offset, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list contracts: %w", err)
	}
	return list, total, nil
}

// Submit 提交合同进入待签署状态，并按提交名单顺序建立签署方。
func (s *ContractService) Submit(userID, contractID uint64, signers []dto.SignerInput) error {
	if err := validateSigners(signers); err != nil {
		return err
	}
	contract, err := s.contractRepo.FindByIDForUser(contractID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return dto.NotFoundError("contract not found")
		}
		return fmt.Errorf("submit contract: %w", err)
	}
	if !constants.CanTransitionContract(contract.Status, constants.ContractStatusPendingSign) {
		return dto.InvalidTransitionError(fmt.Sprintf("cannot submit contract in status %q", contract.Status))
	}

	contract.Status = constants.ContractStatusPendingSign
	if err := s.contractRepo.InTransaction(func(txRepo repository.ContractRepository) error {
		if err := txRepo.Update(contract); err != nil {
			return fmt.Errorf("submit contract: update: %w", err)
		}
		for i, signer := range signers {
			if err := txRepo.AddSigner(&model.ContractSigner{
				ContractID: contract.ID,
				SignOrder:  i + 1,
				Name:       strings.TrimSpace(signer.Name),
				Role:       strings.TrimSpace(signer.Role),
			}); err != nil {
				return fmt.Errorf("submit contract: add signer: %w", err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("submit contract: %w", err)
	}
	s.logger.Info("contract submitted", "contract_id", contract.ID, "signer_count", len(signers))
	return nil
}

// SignResult 单次签署的结果，包含合同最新状态与签署进度。
type SignResult struct {
	ContractID  uint64
	Status      string
	Signer      model.ContractSigner
	SignedCount int
	TotalCount  int
	Completed   bool
}

// Sign 由当前轮到的签署方签署：前一位未签署时后一位不能签署；
// 全部签署方完成后合同才置为已签署并记录最终完成时间。
func (s *ContractService) Sign(userID, contractID uint64, signerName, signerRole, signInfo string) (*SignResult, error) {
	signerName = strings.TrimSpace(signerName)
	if signerName == "" {
		return nil, dto.ValidationError("signer_name is required")
	}
	signerRole = strings.TrimSpace(signerRole)

	var result *SignResult
	err := s.contractRepo.InTransaction(func(txRepo repository.ContractRepository) error {
		contract, err := txRepo.FindByIDForUpdate(contractID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return dto.NotFoundError("contract not found")
			}
			return fmt.Errorf("sign contract: find contract: %w", err)
		}
		if contract.UserID != userID {
			return dto.NotFoundError("contract not found")
		}
		if contract.Status != constants.ContractStatusPendingSign {
			return dto.InvalidTransitionError(fmt.Sprintf("cannot sign contract in status %q", contract.Status))
		}

		signers, err := txRepo.ListSignersForUpdate(contractID)
		if err != nil {
			return fmt.Errorf("sign contract: list signers: %w", err)
		}

		targetIdx := -1
		for i, signer := range signers {
			if signer.Name == signerName {
				targetIdx = i
				break
			}
		}
		if targetIdx == -1 {
			return dto.ValidationError(fmt.Sprintf("signer %q is not in the contract signer list", signerName))
		}
		target := signers[targetIdx]
		if signerRole != "" && signerRole != target.Role {
			return dto.ValidationError(fmt.Sprintf("signer %q role mismatch, expected %q", signerName, target.Role))
		}
		if target.SignedAt != nil {
			return dto.ConflictError(fmt.Sprintf("signer %q has already signed", signerName))
		}
		if targetIdx > 0 && signers[targetIdx-1].SignedAt == nil {
			return dto.ConflictError(fmt.Sprintf("it is not %q's turn: waiting for %q to sign first", signerName, signers[targetIdx-1].Name))
		}

		now := time.Now()
		if err := txRepo.MarkSignerSigned(target.ID, now, signInfo); err != nil {
			return fmt.Errorf("sign contract: mark signer: %w", err)
		}
		signers[targetIdx].SignedAt = &now
		signers[targetIdx].SignInfo = signInfo
		target = signers[targetIdx]

		signedCount := 0
		for _, signer := range signers {
			if signer.SignedAt != nil {
				signedCount++
			}
		}
		status := contract.Status
		completed := signedCount == len(signers)
		if completed {
			if err := txRepo.MarkContractSigned(contract.ID, now); err != nil {
				return fmt.Errorf("sign contract: mark contract: %w", err)
			}
			status = constants.ContractStatusSigned
			contract.Status = status
			contract.SignedAt = &now
		}
		result = &SignResult{
			ContractID:  contract.ID,
			Status:      status,
			Signer:      target,
			SignedCount: signedCount,
			TotalCount:  len(signers),
			Completed:   completed,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info("contract signer signed",
		"contract_id", contractID,
		"signer", signerName,
		"signed_count", result.SignedCount,
		"total_count", result.TotalCount,
		"completed", result.Completed,
	)
	return result, nil
}

// validateSigners 校验提交的签署名单：不能为空、姓名不能为空、同份合同内姓名不能重复。
func validateSigners(signers []dto.SignerInput) error {
	if len(signers) == 0 {
		return dto.ValidationError("signers must contain at least one signer")
	}
	seen := make(map[string]bool, len(signers))
	for _, signer := range signers {
		name := strings.TrimSpace(signer.Name)
		role := strings.TrimSpace(signer.Role)
		if name == "" {
			return dto.ValidationError("signer name is required")
		}
		if role == "" {
			return dto.ValidationError(fmt.Sprintf("signer %q role is required", name))
		}
		if seen[name] {
			return dto.ValidationError(fmt.Sprintf("duplicate signer name %q; each signer must have a unique name", name))
		}
		seen[name] = true
	}
	return nil
}

// Expire 将合同置为已过期。
func (s *ContractService) Expire(userID, contractID uint64) error {
	contract, err := s.contractRepo.FindByIDForUser(contractID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return dto.NotFoundError("contract not found")
		}
		return fmt.Errorf("expire contract: %w", err)
	}
	if !constants.CanTransitionContract(contract.Status, constants.ContractStatusExpired) {
		return dto.InvalidTransitionError(fmt.Sprintf("cannot expire contract in status %q", contract.Status))
	}
	now := time.Now()
	contract.Status = constants.ContractStatusExpired
	contract.ExpiresAt = &now
	if err := s.contractRepo.Update(contract); err != nil {
		return fmt.Errorf("expire contract: update: %w", err)
	}
	s.logger.Info("contract expired", "contract_id", contract.ID)
	return nil
}

// ListSigners 查询合同签署方。
func (s *ContractService) ListSigners(userID, contractID uint64) ([]model.ContractSigner, error) {
	if _, err := s.contractRepo.FindByIDForUser(contractID, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, dto.NotFoundError("contract not found")
		}
		return nil, fmt.Errorf("list signers: %w", err)
	}
	signers, err := s.contractRepo.ListSigners(contractID)
	if err != nil {
		return nil, fmt.Errorf("list signers: %w", err)
	}
	return signers, nil
}

// CleanupPDF 删除导出的临时 PDF 文件。
func (s *ContractService) CleanupPDF(path string) {
	s.pdf.Cleanup(path)
}

// ExportPDF 将合同 HTML 导出为 PDF 文件路径。
func (s *ContractService) ExportPDF(userID, contractID uint64) (string, error) {
	contract, _, err := s.GetForUser(userID, contractID)
	if err != nil {
		return "", err
	}
	path, err := s.pdf.Generate(contract.ContentHTML)
	if err != nil {
		return "", fmt.Errorf("export contract pdf: %w", err)
	}
	return path, nil
}

func missingRequiredVariables(variables model.TemplateVariables, values map[string]string) []string {
	missing := make([]string, 0)
	for _, v := range variables {
		if !v.Required {
			continue
		}
		if strings.TrimSpace(values[v.Name]) == "" {
			missing = append(missing, v.Name)
		}
	}
	return missing
}

func renderTextContract(content string, values map[string]string) (string, error) {
	tmpl, err := texttemplate.New("contract").Parse(content)
	if err != nil {
		return "", fmt.Errorf("parse text template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, values); err != nil {
		return "", fmt.Errorf("execute text template: %w", err)
	}
	return buf.String(), nil
}

func renderHTMLContract(contentHTML, contentText string, values map[string]string) (string, error) {
	if contentHTML == "" {
		escaped := htmltemplate.HTMLEscapeString(contentText)
		return "<html><head><meta charset=\"utf-8\"><title>合同</title></head><body><pre>" + escaped + "</pre></body></html>", nil
	}
	tmpl, err := htmltemplate.New("contract").Parse(contentHTML)
	if err != nil {
		return "", fmt.Errorf("parse html template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, values); err != nil {
		return "", fmt.Errorf("execute html template: %w", err)
	}
	return buf.String(), nil
}
