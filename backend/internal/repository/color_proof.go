package repository

import (
	"context"

	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
)

// ColorProofRepository owns all persistence operations for 色彩校样.
type ColorProofRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ColorProof], error)
	Get(context.Context, uint) (model.ColorProof, error)
	ListByIDs(ctx context.Context, ids []uint) ([]model.ColorProof, error)
	Create(context.Context, *model.ColorProof) error
	Update(context.Context, uint, uint, *model.ColorProof) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type colorProofRepository struct {
	store *Store[model.ColorProof]
}

func NewColorProofRepository(db *gorm.DB) ColorProofRepository {
	return &colorProofRepository{store: NewStore[model.ColorProof](db)}
}

func (r *colorProofRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ColorProof], error) {
	page, pageSize := normalizePage(q.Page, q.PageSize)
	db := r.store.db.WithContext(ctx).Model(&model.ColorProof{})
	if search := searchWildcard(q.Search); search != "" {
		db = db.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ?", search, search)
	}
	if status := q.Status; status != "" {
		db = db.Where("status = ?", status)
	}
	if q.RunID != 0 {
		db = db.Where("print_run_id = ?", q.RunID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[model.ColorProof]{}, err
	}
	items := make([]model.ColorProof, 0)
	err := db.Order("updated_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[model.ColorProof]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (r *colorProofRepository) Get(ctx context.Context, id uint) (model.ColorProof, error) {
	return r.store.Get(ctx, id)
}

func (r *colorProofRepository) ListByIDs(ctx context.Context, ids []uint) ([]model.ColorProof, error) {
	items := make([]model.ColorProof, 0, len(ids))
	if len(ids) == 0 {
		return items, nil
	}
	err := r.store.db.WithContext(ctx).Where("id IN ?", ids).Find(&items).Error
	return items, err
}

func (r *colorProofRepository) Create(ctx context.Context, item *model.ColorProof) error {
	return r.store.Create(ctx, item)
}
func (r *colorProofRepository) Update(ctx context.Context, id, version uint, item *model.ColorProof) error {
	return r.store.Update(ctx, id, version, item)
}
func (r *colorProofRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *colorProofRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
