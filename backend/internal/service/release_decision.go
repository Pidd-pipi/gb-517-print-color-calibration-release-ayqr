package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/constants"
	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"github.com/blueship581/print-color-calibration-release/backend/internal/repository"
)

type ReleaseDecisionService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ReleaseDecision], error)
	Get(context.Context, uint) (model.ReleaseDecision, error)
	Create(context.Context, dto.CreateReleaseDecision, string, string) (model.ReleaseDecision, error)
	Update(context.Context, uint, dto.UpdateReleaseDecision, string, string) (model.ReleaseDecision, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ReleaseDecision, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type releaseDecisionService struct {
	repository repository.ReleaseDecisionRepository
	proofs     repository.ColorProofRepository
	runs       repository.PrintRunRepository
	security   SecurityService
}

func NewReleaseDecisionService(repo repository.ReleaseDecisionRepository, proofs repository.ColorProofRepository, runs repository.PrintRunRepository, security SecurityService) ReleaseDecisionService {
	return &releaseDecisionService{repository: repo, proofs: proofs, runs: runs, security: security}
}

func (s *releaseDecisionService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ReleaseDecision], error) {
	return s.repository.List(ctx, query)
}

func (s *releaseDecisionService) Get(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	return s.repository.Get(ctx, id)
}

func (s *releaseDecisionService) Create(ctx context.Context, input dto.CreateReleaseDecision, actor, requestID string) (model.ReleaseDecision, error) {
	if err := validateReleaseDecisionBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ReleaseDecision{}, err
	}
	item := model.ReleaseDecision{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ReleaseDecisionInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if input.ProofID != 0 {
		if err := s.attachProof(ctx, &item, input.ProofID); err != nil {
			return model.ReleaseDecision{}, err
		}
	}
	if err := s.repository.CreateVersioned(ctx, &item, actor, requestID, "created release decision"); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("create 放行决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "ReleaseDecision", item.ID, "", item.Status, "created 放行决定")
	return item, nil
}

func (s *releaseDecisionService) Update(ctx context.Context, id uint, input dto.UpdateReleaseDecision, actor, requestID string) (model.ReleaseDecision, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	if current.Status == string(constants.DecisionTypeRelease) || current.Status == string(constants.DecisionTypeQuarantine) {
		return model.ReleaseDecision{}, ErrLocked
	}
	if err := validateReleaseDecisionBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ReleaseDecision{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	if input.ProofID != 0 {
		// Re-selecting a proof always re-pins its acceptance snapshot, so a
		// proof re-accepted after a configuration revision can refresh the
		// decision while a stale snapshot can never slip through an edit.
		if err := s.attachProof(ctx, &current, input.ProofID); err != nil {
			return model.ReleaseDecision{}, err
		}
	}
	if current.ProofID != 0 && current.ProofRunCode != current.RelatedCode {
		return model.ReleaseDecision{}, fmt.Errorf("%w: 批次编码 %s 与已选校样所属批次 %s 不一致", ErrProofNotUsable, current.RelatedCode, current.ProofRunCode)
	}
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, "updated decision evidence"); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("update 放行决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "ReleaseDecision", id, current.Status, current.Status, "updated business fields")
	return s.repository.Get(ctx, id)
}

func (s *releaseDecisionService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ReleaseDecision, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	target := strings.TrimSpace(input.Status)
	if (target == string(constants.DecisionTypeRelease) || target == string(constants.DecisionTypeQuarantine) ||
		current.Status == string(constants.DecisionTypeRelease) || current.Status == string(constants.DecisionTypeQuarantine)) && !canReview(role) {
		return model.ReleaseDecision{}, ErrForbidden
	}
	if !constants.CanTransition(constants.ReleaseDecisionTransitions, current.Status, target) {
		return model.ReleaseDecision{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	if target == string(constants.DecisionTypeRelease) {
		if err := s.ensureReleaseProofFresh(ctx, current); err != nil {
			return model.ReleaseDecision{}, err
		}
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, input.Reason); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("transition 放行决定: %w", err)
	}
	if err := s.security.Audit(ctx, actor, requestID, "transition", "ReleaseDecision", id, before, target, input.Reason); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("persist transition audit: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func (s *releaseDecisionService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != model.ReleaseDecisionInitialStatus {
		return ErrLocked
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ReleaseDecision", id, current.Status, "deleted", "soft deleted 放行决定")
}

func (s *releaseDecisionService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateReleaseDecisionBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}

// attachProof validates that the selected proof was accepted under the current
// version of the decision's batch and copies an immutable snapshot of it onto
// the decision. The snapshot (not the live proof row) is what later revisions
// and the release gate rely on.
func (s *releaseDecisionService) attachProof(ctx context.Context, item *model.ReleaseDecision, proofID uint) error {
	proof, err := s.proofs.Get(ctx, proofID)
	if err != nil {
		return fmt.Errorf("%w: 所选校样不存在", ErrProofNotUsable)
	}
	if proof.Status != "accepted" {
		return fmt.Errorf("%w: 校样 %s 尚未接收", ErrProofNotUsable, proof.Code)
	}
	if proof.RunCode == "" || proof.RunCode != item.RelatedCode {
		return fmt.Errorf("%w: 校样 %s 不属于批次 %s", ErrProofNotUsable, proof.Code, item.RelatedCode)
	}
	run, err := s.runs.GetByCode(ctx, proof.RunCode)
	if err != nil {
		return fmt.Errorf("%w: 关联批次 %s 不存在", ErrProofNotUsable, proof.RunCode)
	}
	if run.Version != proof.RunVersion {
		return fmt.Errorf("%w: 校样 %s 接收于批次版本 v%d，当前版本 v%d，已失效", ErrProofNotUsable, proof.Code, proof.RunVersion, run.Version)
	}
	item.ProofID = proof.ID
	item.ProofCode = proof.Code
	item.ProofVersion = proof.Version
	item.ProofRunCode = proof.RunCode
	item.ProofRunVersion = proof.RunVersion
	item.ProofValue = proof.AcceptedValue
	item.ProofUnit = proof.AcceptedUnit
	return nil
}

// ensureReleaseProofFresh re-validates the pinned proof at the release gate:
// the proof must still be accepted and the batch must still sit on the version
// the proof was accepted under. A configuration revision in between blocks the
// release until a fresh proof is selected.
func (s *releaseDecisionService) ensureReleaseProofFresh(ctx context.Context, item model.ReleaseDecision) error {
	if item.ProofID == 0 {
		return fmt.Errorf("%w: 放行前必须选择同一批次当前版本下已接收的校样", ErrProofNotUsable)
	}
	proof, err := s.proofs.Get(ctx, item.ProofID)
	if err != nil || proof.Status != "accepted" {
		return fmt.Errorf("%w: 所选校样已不再是已接收状态", ErrProofNotUsable)
	}
	run, err := s.runs.GetByCode(ctx, item.ProofRunCode)
	if err != nil {
		return fmt.Errorf("%w: 关联批次 %s 不存在", ErrProofNotUsable, item.ProofRunCode)
	}
	if run.Version != item.ProofRunVersion {
		return fmt.Errorf("%w: 批次配置已改版至 v%d，校样对应版本 v%d 已失效", ErrProofNotUsable, run.Version, item.ProofRunVersion)
	}
	return nil
}
