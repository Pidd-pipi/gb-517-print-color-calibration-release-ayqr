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

type ColorProofService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ColorProof], error)
	Get(context.Context, uint) (model.ColorProof, error)
	Create(context.Context, dto.CreateColorProof, string, string) (model.ColorProof, error)
	Update(context.Context, uint, dto.UpdateColorProof, string, string) (model.ColorProof, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ColorProof, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type colorProofService struct {
	repository repository.ColorProofRepository
	runs       repository.PrintRunRepository
	security   SecurityService
}

func NewColorProofService(repo repository.ColorProofRepository, runs repository.PrintRunRepository, security SecurityService) ColorProofService {
	return &colorProofService{repository: repo, runs: runs, security: security}
}

func (s *colorProofService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ColorProof], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	versions, err := s.runVersionMap(ctx, collectProofRunIDs(page.Items))
	if err != nil {
		return page, err
	}
	applyProofStaleness(page.Items, versions)
	if query.EligibleOnly {
		page.Items = filterEligibleProofs(page.Items)
		page.Total = int64(len(page.Items))
	}
	return page, nil
}

func (s *colorProofService) Get(ctx context.Context, id uint) (model.ColorProof, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ColorProof{}, err
	}
	versions, err := s.runVersionMap(ctx, []uint{item.PrintRunID})
	if err != nil {
		return model.ColorProof{}, err
	}
	applyProofStaleness([]model.ColorProof{item}, versions)
	item.Stale = isProofStale(item, versions)
	return item, nil
}

func (s *colorProofService) Create(ctx context.Context, input dto.CreateColorProof, actor, requestID string) (model.ColorProof, error) {
	if err := validateColorProofBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ColorProof{}, err
	}
	run, err := s.runs.Get(ctx, input.PrintRunID)
	if err != nil {
		return model.ColorProof{}, fmt.Errorf("resolve proof print run: %w", err)
	}
	item := model.ColorProof{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ColorProofInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: run.Code, PrintRunID: run.ID,
	}
	if err := s.repository.Create(ctx, &item); err != nil {
		return model.ColorProof{}, fmt.Errorf("create 色彩校样: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "ColorProof", item.ID, "", item.Status,
		fmt.Sprintf("created 色彩校样 against %s v%d", run.Code, run.Version))
	return item, nil
}

func (s *colorProofService) Update(ctx context.Context, id uint, input dto.UpdateColorProof, actor, requestID string) (model.ColorProof, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ColorProof{}, err
	}
	// Once accepted, the readings and batch snapshot are evidence: the proof
	// must not be edited or re-pinned against a newer configuration version.
	if current.Pinned() {
		return model.ColorProof{}, ErrProofAccepted
	}
	if err := validateColorProofBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ColorProof{}, err
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
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current); err != nil {
		return model.ColorProof{}, fmt.Errorf("update 色彩校样: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "ColorProof", id, current.Status, current.Status, "updated business fields")
	return s.repository.Get(ctx, id)
}

func (s *colorProofService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ColorProof, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ColorProof{}, err
	}
	target := strings.TrimSpace(input.Status)
	if (target == "accepted" || target == "rejected" || current.Status == "accepted" || current.Status == "rejected") && !canReview(role) {
		return model.ColorProof{}, ErrForbidden
	}
	if !constants.CanTransition(constants.ColorProofTransitions, current.Status, target) {
		return model.ColorProof{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	pinDetail := "updated proof status"
	// Acceptance freezes the batch code, batch configuration version and the
	// measured readings at the moment of review. The snapshot cannot move.
	if target == "accepted" {
		run, runErr := s.runs.Get(ctx, current.PrintRunID)
		if runErr != nil {
			return model.ColorProof{}, fmt.Errorf("pin proof print run: %w", runErr)
		}
		current.PinnedRunCode = run.Code
		current.PinnedRunVersion = run.Version
		current.PinnedMetricValue = current.MetricValue
		current.PinnedMetricUnit = current.MetricUnit
		current.PinnedAt = time.Now().UTC()
		current.PinnedBy = actor
		pinDetail = fmt.Sprintf("accepted and pinned to %s v%d", run.Code, run.Version)
	}
	if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current); err != nil {
		return model.ColorProof{}, fmt.Errorf("transition 色彩校样: %w", err)
	}
	if err := s.security.Audit(ctx, actor, requestID, "transition", "ColorProof", id, before, target, pinDetail); err != nil {
		return model.ColorProof{}, fmt.Errorf("persist transition audit: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *colorProofService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Pinned() {
		return ErrProofAccepted
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ColorProof", id, current.Status, "deleted", "soft deleted 色彩校样")
}

func (s *colorProofService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

// runVersionMap loads the current code/version of the given runs for staleness
// evaluation. A run that no longer exists contributes no entry, which makes
// every pinned proof against it stale.
func (s *colorProofService) runVersionMap(ctx context.Context, runIDs []uint) (map[uint]model.PrintRun, error) {
	unique := make(map[uint]struct{}, len(runIDs))
	for _, id := range runIDs {
		if id != 0 {
			unique[id] = struct{}{}
		}
	}
	ids := make([]uint, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	runs, err := s.runs.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[uint]model.PrintRun, len(runs))
	for _, run := range runs {
		result[run.ID] = run
	}
	return result, nil
}

func collectProofRunIDs(items []model.ColorProof) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.PrintRunID)
	}
	return ids
}

func isProofStale(item model.ColorProof, runs map[uint]model.PrintRun) bool {
	if !item.Pinned() {
		return false
	}
	run, exists := runs[item.PrintRunID]
	if !exists {
		return true
	}
	return run.Version != item.PinnedRunVersion
}

func applyProofStaleness(items []model.ColorProof, runs map[uint]model.PrintRun) {
	for index := range items {
		items[index].Stale = isProofStale(items[index], runs)
	}
}

// filterEligibleProofs keeps accepted proofs pinned against their run's
// current configuration version. Used by the release selection dialog.
func filterEligibleProofs(items []model.ColorProof) []model.ColorProof {
	eligible := make([]model.ColorProof, 0, len(items))
	for _, item := range items {
		if item.Status == "accepted" && item.Pinned() && !item.Stale {
			eligible = append(eligible, item)
		}
	}
	return eligible
}

func validateColorProofBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
