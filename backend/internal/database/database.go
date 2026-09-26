package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/config"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(ctx context.Context, cfg config.Config, log *slog.Logger) (*gorm.DB, *redis.Client, error) {
	var dialector gorm.Dialector
	switch cfg.DatabaseDriver {
	case "postgres":
		dialector = postgres.Open(cfg.DatabaseDSN)
	case "mysql":
		dialector = mysql.Open(cfg.DatabaseDSN)
	case "sqlite":
		dialector = sqlite.Open(cfg.DatabaseDSN)
	default:
		return nil, nil, fmt.Errorf("unsupported database driver %q", cfg.DatabaseDriver)
	}
	logLevel := logger.Warn
	if cfg.Environment == "development" {
		logLevel = logger.Info
	}
	var db *gorm.DB
	var err error
	for attempt := 1; attempt <= 20; attempt++ {
		db, err = gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logLevel)})
		if err == nil {
			sqlDB, dbErr := db.DB()
			if dbErr == nil && sqlDB.PingContext(ctx) == nil {
				break
			}
			if dbErr != nil {
				err = dbErr
			} else {
				err = sqlDB.PingContext(ctx)
			}
		}
		log.Warn("database not ready", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("connect database: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, nil, err
	}
	if err := Seed(ctx, db); err != nil {
		return nil, nil, err
	}
	var redisClient *redis.Client
	if cfg.RedisAddr != "" {
		redisClient = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
		if err := redisClient.Ping(ctx).Err(); err != nil {
			return nil, nil, fmt.Errorf("connect redis: %w", err)
		}
	}
	return db, redisClient, nil
}

func migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.User{}, &model.AuditLog{},
		&model.PressUnit{},
		&model.PrintRun{}, &model.PrintRunRevision{},
		&model.ColorProof{},
		&model.ReleaseDecision{}, &model.ReleaseDecisionRevision{}, &model.ReleaseDecisionProof{},
	)
}

func Seed(ctx context.Context, db *gorm.DB) error {
	var users int64
	if err := db.WithContext(ctx).Model(&model.User{}).Count(&users).Error; err != nil {
		return err
	}
	if users == 0 {
		password, err := bcrypt.GenerateFromPassword([]byte("Admin123!"), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		seedUsers := []model.User{
			{Username: "admin", DisplayName: "系统管理员", PasswordHash: string(password), Role: model.RoleAdmin, Active: true},
			{Username: "reviewer", DisplayName: "质量复核员", PasswordHash: string(password), Role: model.RoleReviewer, Active: true},
			{Username: "operator", DisplayName: "现场操作员", PasswordHash: string(password), Role: model.RoleOperator, Active: true},
			{Username: "viewer", DisplayName: "只读观察员", PasswordHash: string(password), Role: model.RoleViewer, Active: true},
		}
		if err := db.WithContext(ctx).Create(&seedUsers).Error; err != nil {
			return err
		}
	}

	if err := seedPressUnit(ctx, db); err != nil {
		return err
	}

	if err := seedPrintRun(ctx, db); err != nil {
		return err
	}

	if err := seedColorProof(ctx, db); err != nil {
		return err
	}

	if err := seedReleaseDecision(ctx, db); err != nil {
		return err
	}

	return nil
}

func seedPressUnit(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.PressUnit{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	now := time.Now().UTC()
	items := []model.PressUnit{

		{BaseModel: model.BaseModel{Code: "PU-001", Name: "印刷设备示例一", Status: "ready", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷设备记录"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: "unit",
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-01"},

		{BaseModel: model.BaseModel{Code: "PU-002", Name: "印刷设备示例二", Status: "setup", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷设备记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: "%",
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-02"},

		{BaseModel: model.BaseModel{Code: "PU-003", Name: "印刷设备示例三", Status: "printing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷设备记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: "score",
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-03"},
	}
	return db.WithContext(ctx).Create(&items).Error
}

func seedPrintRun(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.PrintRun{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	now := time.Now().UTC()
	items := []model.PrintRun{

		{BaseModel: model.BaseModel{Code: "PR-001", Name: "印刷批次示例一", Status: "hold", Version: 2,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 11.8, MetricUnit: "unit",
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "改版后重新核对的色彩配置", RelatedCode: "REL-517-01"},

		{BaseModel: model.BaseModel{Code: "PR-002", Name: "印刷批次示例二", Status: "printing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: "%",
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-02"},

		{BaseModel: model.BaseModel{Code: "PR-003", Name: "印刷批次示例三", Status: "proofing", Version: 1,
			Description: "用于启动验证和主要流程演示的印刷批次记录"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: "score",
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: "REL-517-03"},
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(&items).Error; err != nil {
			return err
		}
		revisions := make([]model.PrintRunRevision, 0, len(items)+1)
		for _, item := range items {
			revisions = append(revisions, model.PrintRunRevision{
				PrintRunID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
				Facility: item.Facility, Owner: item.Owner, Category: item.Category,
				RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
				Evidence: item.Evidence, RelatedCode: item.RelatedCode,
				Actor: "seed", RequestID: "startup-seed", Reason: "current colour configuration",
			})
		}
		// PR-001 keeps its v1 revision so proofs accepted against v1 can be
		// demonstrated as stale once the configuration was revised to v2.
		pr001 := items[0]
		revisions = append(revisions, model.PrintRunRevision{
			PrintRunID: pr001.ID, Version: 1, Status: "printing", Name: pr001.Name,
			Facility: pr001.Facility, Owner: pr001.Owner, Category: pr001.Category,
			RiskLevel: pr001.RiskLevel, MetricValue: 12.5, MetricUnit: pr001.MetricUnit,
			Evidence: "初始色彩配置（已被 v2 改版）", RelatedCode: pr001.RelatedCode,
			Actor: "seed", RequestID: "startup-seed", Reason: "initial colour configuration",
		})
		return tx.Create(&revisions).Error
	})
}

func seedColorProof(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.ColorProof{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	var runs []model.PrintRun
	if err := db.WithContext(ctx).Order("code ASC").Find(&runs).Error; err != nil {
		return err
	}
	runByCode := make(map[string]model.PrintRun, len(runs))
	for _, run := range runs {
		runByCode[run.Code] = run
	}
	now := time.Now().UTC()
	pr001 := runByCode["PR-001"]
	pr002 := runByCode["PR-002"]
	pr003 := runByCode["PR-003"]

	items := []model.ColorProof{

		{BaseModel: model.BaseModel{Code: "CP-001", Name: "色彩校样示例一", Status: "captured", Version: 1,
			Description: "改版后重新采集的校样，尚未接收"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 11.8, MetricUnit: pr001.MetricUnit,
			EffectiveAt: now.Add(0 * time.Hour), Evidence: "按 v2 配置重新测量", RelatedCode: pr001.Code, PrintRunID: pr001.ID},

		{BaseModel: model.BaseModel{Code: "CP-002", Name: "色彩校样示例二", Status: "review", Version: 1,
			Description: "等待质量复核员接收的校样"}, Facility: "印刷色彩批次校准放行区域2", Owner: "质量复核组",
			Category: "重点", RiskLevel: "medium", MetricValue: 25.0, MetricUnit: pr002.MetricUnit,
			EffectiveAt: now.Add(3 * time.Hour), Evidence: "已完成基础证据核对", RelatedCode: pr002.Code, PrintRunID: pr002.ID},

		{BaseModel: model.BaseModel{Code: "CP-003", Name: "色彩校样示例三", Status: "accepted", Version: 2,
			Description: "已按当前批次版本接收，可用于放行"}, Facility: "印刷色彩批次校准放行区域3", Owner: "安全主管组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: pr003.MetricUnit,
			EffectiveAt: now.Add(6 * time.Hour), Evidence: "分光密度计连续三次读数一致", RelatedCode: pr003.Code, PrintRunID: pr003.ID,
			PinnedRunCode: pr003.Code, PinnedRunVersion: pr003.Version, PinnedMetricValue: 37.5, PinnedMetricUnit: pr003.MetricUnit,
			PinnedAt: now.Add(-1 * time.Hour), PinnedBy: "reviewer"},

		// Accepted against PR-001 v1: once the run was revised to v2 this
		// proof is reported stale and cannot be carried into a release.
		{BaseModel: model.BaseModel{Code: "CP-004", Name: "色彩校样示例四（旧版本）", Status: "accepted", Version: 2,
			Description: "在 PR-001 v1 配置下接收，配置改版后已失效"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: pr001.MetricUnit,
			EffectiveAt: now.Add(-2 * time.Hour), Evidence: "旧版配置读数（仅供历史追溯）", RelatedCode: pr001.Code, PrintRunID: pr001.ID,
			PinnedRunCode: pr001.Code, PinnedRunVersion: 1, PinnedMetricValue: 12.5, PinnedMetricUnit: pr001.MetricUnit,
			PinnedAt: now.Add(-2 * time.Hour), PinnedBy: "reviewer"},
	}
	return db.WithContext(ctx).Create(&items).Error
}

func seedReleaseDecision(ctx context.Context, db *gorm.DB) error {
	var count int64
	if err := db.WithContext(ctx).Model(&model.ReleaseDecision{}).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	var runs []model.PrintRun
	if err := db.WithContext(ctx).Order("code ASC").Find(&runs).Error; err != nil {
		return err
	}
	runByCode := make(map[string]model.PrintRun, len(runs))
	for _, run := range runs {
		runByCode[run.Code] = run
	}
	var proofs []model.ColorProof
	if err := db.WithContext(ctx).Order("code ASC").Find(&proofs).Error; err != nil {
		return err
	}
	proofByCode := make(map[string]model.ColorProof, len(proofs))
	for _, proof := range proofs {
		proofByCode[proof.Code] = proof
	}
	now := time.Now().UTC()
	pr001 := runByCode["PR-001"]
	pr003 := runByCode["PR-003"]
	cp003 := proofByCode["CP-003"]
	cp004 := proofByCode["CP-004"]

	items := []model.ReleaseDecision{

		// Draft for PR-003 backed by the eligible CP-003 snapshot.
		{BaseModel: model.BaseModel{Code: "RD-001", Name: "放行决定示例一", Status: "draft", Version: 1,
			Description: "等待复核员放行的决定草稿"}, Facility: "印刷色彩批次校准放行区域3", Owner: "质量复核组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: pr003.MetricUnit,
			EffectiveAt: now.Add(0 * time.Hour), Evidence: cp003.Evidence, RelatedCode: pr003.Code, PrintRunID: pr003.ID},

		// Released decision: its revision keeps the CP-003 snapshot forever.
		{BaseModel: model.BaseModel{Code: "RD-002", Name: "放行决定示例二", Status: "release", Version: 1,
			Description: "已按当前版本校样放行"}, Facility: "印刷色彩批次校准放行区域3", Owner: "质量复核组",
			Category: "复核", RiskLevel: "high", MetricValue: 37.5, MetricUnit: pr003.MetricUnit,
			EffectiveAt: now.Add(3 * time.Hour), Evidence: cp003.Evidence, RelatedCode: pr003.Code, PrintRunID: pr003.ID},

		// Rework decision kept for history on PR-001; its CP-004 snapshot is
		// flagged stale in the UI because the run has since moved to v2.
		{BaseModel: model.BaseModel{Code: "RD-003", Name: "放行决定示例三", Status: "rework", Version: 1,
			Description: "旧版校样触发返修，配置改版后校样已失效"}, Facility: "印刷色彩批次校准放行区域1", Owner: "运行一组",
			Category: "常规", RiskLevel: "low", MetricValue: 12.5, MetricUnit: pr001.MetricUnit,
			EffectiveAt: now.Add(6 * time.Hour), Evidence: cp004.Evidence, RelatedCode: pr001.Code, PrintRunID: pr001.ID},
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(&items).Error; err != nil {
			return err
		}
		revisions := make([]model.ReleaseDecisionRevision, 0, len(items))
		for _, item := range items {
			revisions = append(revisions, model.ReleaseDecisionRevision{
				ReleaseDecisionID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
				RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
				Evidence: item.Evidence, RelatedCode: item.RelatedCode,
				Actor: "seed", RequestID: "startup-seed", Reason: "initial release decision",
			})
		}
		if err := tx.Create(&revisions).Error; err != nil {
			return err
		}
		snapshotOf := func(revisionID uint, proof model.ColorProof) model.ReleaseDecisionProof {
			return model.ReleaseDecisionProof{
				RevisionID: revisionID, ProofID: proof.ID, ProofCode: proof.Code, ProofName: proof.Name,
				ProofStatus: proof.Status, PinnedRunCode: proof.PinnedRunCode, PinnedRunVersion: proof.PinnedRunVersion,
				PinnedMetricValue: proof.PinnedMetricValue, PinnedMetricUnit: proof.PinnedMetricUnit,
				Evidence: proof.Evidence, PinnedAt: proof.PinnedAt,
			}
		}
		snapshots := []model.ReleaseDecisionProof{
			snapshotOf(revisions[0].ID, cp003),
			snapshotOf(revisions[1].ID, cp003),
			snapshotOf(revisions[2].ID, cp004),
		}
		return tx.Create(&snapshots).Error
	})
}
