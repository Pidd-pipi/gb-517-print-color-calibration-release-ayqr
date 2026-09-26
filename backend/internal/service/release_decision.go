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
	page, err := s.repository.List(ctx, query)
	if err != nil || len(page.Items) == 0 {
		return page, err
	}
	runIDs := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		runIDs = append(runIDs, item.PrintRunID)
	}
	runs, err := s.runs.ListByIDs(ctx, runIDs)
	if err != nil {
		return page, err
	}
	versionByRun := make(map[uint]uint, len(runs))
	for _, run := range runs {
		versionByRun[run.ID] = run.Version
	}
	for index := range page.Items {
		currentVersion := versionByRun[page.Items[index].PrintRunID]
		for revisionIndex := range page.Items[index].Revisions {
			for snapshotIndex := range page.Items[index].Revisions[revisionIndex].ProofSnapshots {
				snapshot := &page.Items[index].Revisions[revisionIndex].ProofSnapshots[snapshotIndex]
				snapshot.Stale = currentVersion == 0 || snapshot.PinnedRunVersion != currentVersion
			}
		}
	}
	return page, nil
}

func (s *releaseDecisionService) Get(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseDecision{}, err
	}
	if err := s.annotateSnapshotStaleness(ctx, &item); err != nil {
		return model.ReleaseDecision{}, err
	}
	return item, nil
}

func (s *releaseDecisionService) Create(ctx context.Context, input dto.CreateReleaseDecision, actor, requestID string) (model.ReleaseDecision, error) {
	if err := validateReleaseDecisionBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ReleaseDecision{}, err
	}
	run, err := s.runs.Get(ctx, input.PrintRunID)
	if err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("resolve decision print run: %w", err)
	}
	snapshots, err := s.resolveEligibleProofs(ctx, run.ID, run.Version, input.ProofIDs)
	if err != nil {
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
		RelatedCode: run.Code, PrintRunID: run.ID,
	}
	if err := s.repository.CreateVersioned(ctx, &item, snapshots, actor, requestID, "created release decision with pinned proof snapshots"); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("create 放行决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "ReleaseDecision", item.ID, "", item.Status,
		fmt.Sprintf("created 放行决定 for %s v%d with %d proof(s)", run.Code, run.Version, len(snapshots)))
	return s.Get(ctx, item.ID)
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
	run, err := s.runs.Get(ctx, current.PrintRunID)
	if err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("resolve decision print run: %w", err)
	}
	// Reselecting proofs must again use accepted proofs of the run's current
	// version; proofs that went stale after a configuration change are blocked.
	snapshots, err := s.resolveEligibleProofs(ctx, run.ID, run.Version, input.ProofIDs)
	if err != nil {
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
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, snapshots, actor, requestID, "reselected current-version proof snapshots"); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("update 放行决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "ReleaseDecision", id, current.Status, current.Status,
		fmt.Sprintf("updated decision evidence; %d current-version proof(s)", len(snapshots)))
	return s.Get(ctx, id)
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
	// A release may only be based on accepted proofs of the batch's current
	// configuration version. Rework/quarantine may proceed on older evidence.
	if target == string(constants.DecisionTypeRelease) {
		if err := s.ensureCurrentVersionProofs(ctx, current); err != nil {
			return model.ReleaseDecision{}, err
		}
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	// Carry the currently selected proof snapshots forward into the new
	// revision so the decision record remains self-contained and immutable.
	snapshots := latestProofSnapshots(current)
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, snapshots, actor, requestID, input.Reason); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("transition 放行决定: %w", err)
	}
	if err := s.security.Audit(ctx, actor, requestID, "transition", "ReleaseDecision", id, before, target, input.Reason); err != nil {
		return model.ReleaseDecision{}, fmt.Errorf("persist transition audit: %w", err)
	}
	return s.Get(ctx, id)
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

// resolveEligibleProofs loads the selected proofs and guarantees each one is an
// accepted proof pinned to the given run at its current configuration version.
func (s *releaseDecisionService) resolveEligibleProofs(ctx context.Context, runID, runVersion uint, proofIDs []uint) ([]model.ReleaseDecisionProof, error) {
	proofIDs = uniqueUintIDs(proofIDs)
	if len(proofIDs) == 0 {
		return nil, ErrDecisionNoProof
	}
	proofs, err := s.proofs.ListByIDs(ctx, proofIDs)
	if err != nil {
		return nil, fmt.Errorf("load selected proofs: %w", err)
	}
	byID := make(map[uint]model.ColorProof, len(proofs))
	for _, proof := range proofs {
		byID[proof.ID] = proof
	}
	snapshots := make([]model.ReleaseDecisionProof, 0, len(proofIDs))
	for _, proofID := range proofIDs {
		proof, exists := byID[proofID]
		if !exists {
			return nil, fmt.Errorf("%w: proof %d", ErrProofNotAccepted, proofID)
		}
		if proof.PrintRunID != runID {
			return nil, fmt.Errorf("%w: proof %s", ErrProofWrongRun, proof.Code)
		}
		if proof.Status != "accepted" || !proof.Pinned() {
			return nil, fmt.Errorf("%w: %s", ErrProofNotAccepted, proof.Code)
		}
		if proof.PinnedRunVersion != runVersion {
			return nil, fmt.Errorf("%w: %s pinned to v%d, run is v%d", ErrProofStale, proof.Code, proof.PinnedRunVersion, runVersion)
		}
		snapshots = append(snapshots, proofSnapshot(proof))
	}
	return snapshots, nil
}

// ensureCurrentVersionProofs validates the snapshots backing the latest
// revision at release time against the run's current version.
func (s *releaseDecisionService) ensureCurrentVersionProofs(ctx context.Context, decision model.ReleaseDecision) error {
	run, err := s.runs.Get(ctx, decision.PrintRunID)
	if err != nil {
		return fmt.Errorf("resolve decision print run: %w", err)
	}
	latest := latestRevision(decision)
	if latest == nil || len(latest.ProofSnapshots) == 0 {
		return ErrDecisionNoProof
	}
	for _, snapshot := range latest.ProofSnapshots {
		if snapshot.PinnedRunCode != run.Code || snapshot.PinnedRunVersion != run.Version {
			return fmt.Errorf("%w: %s pinned to %s v%d, run is %s v%d",
				ErrProofStale, snapshot.ProofCode, snapshot.PinnedRunCode, snapshot.PinnedRunVersion, run.Code, run.Version)
		}
	}
	return nil
}

// annotateSnapshotStaleness flags snapshots in every revision whose pinned
// version differs from the run's current version. Snapshots themselves are not
// modified; this only drives the "已失效" presentation on reads.
func (s *releaseDecisionService) annotateSnapshotStaleness(ctx context.Context, decision *model.ReleaseDecision) error {
	run, err := s.runs.ListByIDs(ctx, []uint{decision.PrintRunID})
	if err != nil {
		return err
	}
	var currentVersion uint
	if len(run) == 1 {
		currentVersion = run[0].Version
	}
	for revisionIndex := range decision.Revisions {
		revision := &decision.Revisions[revisionIndex]
		for snapshotIndex := range revision.ProofSnapshots {
			snapshot := &revision.ProofSnapshots[snapshotIndex]
			snapshot.Stale = currentVersion == 0 || snapshot.PinnedRunVersion != currentVersion
		}
	}
	return nil
}

func latestRevision(decision model.ReleaseDecision) *model.ReleaseDecisionRevision {
	if len(decision.Revisions) == 0 {
		return nil
	}
	latest := &decision.Revisions[0]
	for index := range decision.Revisions {
		if decision.Revisions[index].Version > latest.Version {
			latest = &decision.Revisions[index]
		}
	}
	return latest
}

// latestProofSnapshots returns the snapshots of the newest revision, with the
// transient Stale flag cleared so they are persisted cleanly on the next one.
func latestProofSnapshots(decision model.ReleaseDecision) []model.ReleaseDecisionProof {
	latest := latestRevision(decision)
	if latest == nil {
		return []model.ReleaseDecisionProof{}
	}
	snapshots := make([]model.ReleaseDecisionProof, len(latest.ProofSnapshots))
	for index, snapshot := range latest.ProofSnapshots {
		snapshot.ID = 0
		snapshot.RevisionID = 0
		snapshot.Stale = false
		snapshots[index] = snapshot
	}
	return snapshots
}

func proofSnapshot(proof model.ColorProof) model.ReleaseDecisionProof {
	return model.ReleaseDecisionProof{
		ProofID: proof.ID, ProofCode: proof.Code, ProofName: proof.Name, ProofStatus: proof.Status,
		PinnedRunCode: proof.PinnedRunCode, PinnedRunVersion: proof.PinnedRunVersion,
		PinnedMetricValue: proof.PinnedMetricValue, PinnedMetricUnit: proof.PinnedMetricUnit,
		Evidence: proof.Evidence, PinnedAt: proof.PinnedAt,
	}
}

func uniqueUintIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func validateReleaseDecisionBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
