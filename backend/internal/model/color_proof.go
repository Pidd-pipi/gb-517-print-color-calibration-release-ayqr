package model

import "time"

// ColorProof models 色彩校样 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
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
	// Acceptance snapshot: the moment a reviewer accepts the proof, the batch
	// identity and the verified readings are pinned so later configuration
	// revisions or proof edits can never rewrite what was accepted.
	RunCode       string     `json:"runCode" gorm:"size:64;index"`
	RunVersion    uint       `json:"runVersion"`
	AcceptedValue float64    `json:"acceptedValue"`
	AcceptedUnit  string     `json:"acceptedUnit" gorm:"size:24"`
	AcceptedAt    *time.Time `json:"acceptedAt,omitempty"`
	AcceptedBy    string     `json:"acceptedBy" gorm:"size:80"`
	// Stale is computed on read: an accepted proof whose pinned batch version
	// no longer matches the batch's current version is invalidated.
	Stale bool `json:"stale" gorm:"-"`
}

func (item *ColorProof) GetBase() *BaseModel { return &item.BaseModel }

func (item ColorProof) TableName() string { return "color_proofs" }

var ColorProofInitialStatus = "captured"
