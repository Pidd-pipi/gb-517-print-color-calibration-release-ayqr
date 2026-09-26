package repository

import (
	"context"

	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
)

// ReleaseDecisionRepository owns all persistence operations for 放行决定.
type ReleaseDecisionRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ReleaseDecision], error)
	Get(context.Context, uint) (model.ReleaseDecision, error)
	CreateVersioned(context.Context, *model.ReleaseDecision, []model.ReleaseDecisionProof, string, string, string) error
	UpdateVersioned(context.Context, uint, uint, *model.ReleaseDecision, []model.ReleaseDecisionProof, string, string, string) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type releaseDecisionRepository struct {
	store *Store[model.ReleaseDecision]
}

func NewReleaseDecisionRepository(db *gorm.DB) ReleaseDecisionRepository {
	return &releaseDecisionRepository{store: NewStore[model.ReleaseDecision](db)}
}

func (r *releaseDecisionRepository) List(ctx context.Context, q dto.PageQuery) (page Page[model.ReleaseDecision], err error) {
	page, err = r.store.List(ctx, q)
	if err != nil || len(page.Items) == 0 {
		return page, err
	}
	// Preload only the newest revision's proof snapshots so list views can show
	// which batch version the current decision is backed on, without loading
	// the full immutable history of every row.
	ids := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	var revisions []model.ReleaseDecisionRevision
	err = r.store.db.WithContext(ctx).
		Preload("ProofSnapshots").
		Where("release_decision_id IN ?", ids).
		Order("release_decision_id, version DESC").
		Find(&revisions).Error
	if err != nil {
		return page, err
	}
	byDecision := make(map[uint]model.ReleaseDecisionRevision, len(revisions))
	for _, revision := range revisions {
		if _, seen := byDecision[revision.ReleaseDecisionID]; !seen {
			byDecision[revision.ReleaseDecisionID] = revision
		}
	}
	for index := range page.Items {
		if revision, exists := byDecision[page.Items[index].ID]; exists {
			page.Items[index].Revisions = []model.ReleaseDecisionRevision{revision}
		}
	}
	return page, nil
}

func (r *releaseDecisionRepository) Get(ctx context.Context, id uint) (model.ReleaseDecision, error) {
	var item model.ReleaseDecision
	err := r.store.db.WithContext(ctx).
		Preload("Revisions", func(db *gorm.DB) *gorm.DB {
			return db.Order("version DESC").Preload("ProofSnapshots")
		}).
		First(&item, id).Error
	return item, err
}

func (r *releaseDecisionRepository) CreateVersioned(ctx context.Context, item *model.ReleaseDecision, snapshots []model.ReleaseDecisionProof, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(item).Error; err != nil {
			return err
		}
		revision := releaseDecisionRevision(item, actor, requestID, reason)
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		return createProofSnapshots(tx, revision.ID, snapshots)
	})
}

func (r *releaseDecisionRepository) UpdateVersioned(ctx context.Context, id, version uint, item *model.ReleaseDecision, snapshots []model.ReleaseDecisionProof, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReleaseDecision{}).Where("id = ? AND version = ?", id, version).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		revision := releaseDecisionRevision(item, actor, requestID, reason)
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		return createProofSnapshots(tx, revision.ID, snapshots)
	})
}

// createProofSnapshots freezes the selected proofs under a freshly created
// revision. Historical snapshots are never updated or deleted.
func createProofSnapshots(tx *gorm.DB, revisionID uint, snapshots []model.ReleaseDecisionProof) error {
	for index := range snapshots {
		snapshots[index].ID = 0
		snapshots[index].RevisionID = revisionID
	}
	if len(snapshots) == 0 {
		return nil
	}
	return tx.Create(&snapshots).Error
}

func releaseDecisionRevision(item *model.ReleaseDecision, actor, requestID, reason string) *model.ReleaseDecisionRevision {
	return &model.ReleaseDecisionRevision{
		ReleaseDecisionID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
		RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode,
		Actor: actor, RequestID: requestID, Reason: reason,
	}
}
func (r *releaseDecisionRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *releaseDecisionRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
