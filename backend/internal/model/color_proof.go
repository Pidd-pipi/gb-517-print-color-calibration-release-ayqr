package model

import "time"

// ColorProof models 色彩校样 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
//
// A proof is measured against one PrintRun. When a reviewer accepts the proof
// the batch code, batch configuration version and the measured readings are
// pinned into the Pinned* snapshot fields. The snapshot is immutable: later
// configuration revisions of the batch never rewrite it. Stale is derived at
// read time by comparing the pinned version against the run's current version.
type ColorProof struct {
	BaseModel
	Facility    string    `json:"facility" gorm:"size:120;index"`
	Owner       string    `json:"owner" gorm:"size:120;index"`
	Category    string    `json:"category" gorm:"size:80;index"`
	RiskLevel   string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue float64   `json:"metricValue"`
	MetricUnit  string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt time.Time `json:"effectiveAt"`
	Evidence    string    `json:"evidence" gorm:"size:2000"`
	RelatedCode string    `json:"relatedCode" gorm:"size:64;index"`

	// PrintRunID links the proof to the measured batch. It is required on
	// creation and never changes afterwards.
	PrintRunID uint `json:"printRunId" gorm:"index;not null"`

	// Pinned* fields are empty until a reviewer accepts the proof, at which
	// point they freeze the batch identity/version and the readings used for
	// the release decision. Updates to the proof after acceptance are blocked.
	PinnedRunCode     string    `json:"pinnedRunCode,omitempty" gorm:"size:64;index"`
	PinnedRunVersion  uint      `json:"pinnedRunVersion"`
	PinnedMetricValue float64   `json:"pinnedMetricValue"`
	PinnedMetricUnit  string    `json:"pinnedMetricUnit" gorm:"size:24"`
	PinnedAt          time.Time `json:"pinnedAt,omitempty"`
	PinnedBy          string    `json:"pinnedBy" gorm:"size:80"`

	// Stale is computed by the service when reading: true means the pinned
	// batch configuration version is older than the run's current version.
	Stale bool `json:"stale" gorm:"-"`
}

func (item *ColorProof) GetBase() *BaseModel { return &item.BaseModel }

func (item ColorProof) TableName() string { return "color_proofs" }

// Pinned reports whether the proof has already been accepted and therefore
// carries an immutable batch/readings snapshot.
func (item *ColorProof) Pinned() bool { return item.PinnedRunCode != "" }

var ColorProofInitialStatus = "captured"
