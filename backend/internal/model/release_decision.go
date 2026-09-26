package model

import "time"

// ReleaseDecision models 放行决定 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type ReleaseDecision struct {
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
	// Proof snapshot: the accepted colour proof this decision relies on,
	// copied at selection time so later proof or batch changes cannot
	// rewrite the historical basis of the decision.
	ProofID         uint                      `json:"proofId" gorm:"index"`
	ProofCode       string                    `json:"proofCode" gorm:"size:64"`
	ProofVersion    uint                      `json:"proofVersion"`
	ProofRunCode    string                    `json:"proofRunCode" gorm:"size:64"`
	ProofRunVersion uint                      `json:"proofRunVersion"`
	ProofValue      float64                   `json:"proofValue"`
	ProofUnit       string                    `json:"proofUnit" gorm:"size:24"`
	Revisions       []ReleaseDecisionRevision `json:"revisions,omitempty" gorm:"foreignKey:ReleaseDecisionID"`
}

func (item *ReleaseDecision) GetBase() *BaseModel { return &item.BaseModel }

func (item ReleaseDecision) TableName() string { return "release_decisions" }

var ReleaseDecisionInitialStatus = "draft"

// ReleaseDecisionRevision preserves every decision and its evidence as an
// immutable approval record, including who made it and which request did so.
type ReleaseDecisionRevision struct {
	ID                uint      `json:"id" gorm:"primaryKey"`
	ReleaseDecisionID uint      `json:"releaseDecisionId" gorm:"not null;uniqueIndex:idx_release_decision_revision"`
	Version           uint      `json:"version" gorm:"not null;uniqueIndex:idx_release_decision_revision"`
	Status            string    `json:"status" gorm:"size:40;not null"`
	Name              string    `json:"name" gorm:"size:160;not null"`
	RiskLevel         string    `json:"riskLevel" gorm:"size:32"`
	MetricValue       float64   `json:"metricValue"`
	MetricUnit        string    `json:"metricUnit" gorm:"size:24"`
	Evidence          string    `json:"evidence" gorm:"size:2000"`
	RelatedCode       string    `json:"relatedCode" gorm:"size:64"`
	ProofID           uint      `json:"proofId"`
	ProofCode         string    `json:"proofCode" gorm:"size:64"`
	ProofVersion      uint      `json:"proofVersion"`
	ProofRunCode      string    `json:"proofRunCode" gorm:"size:64"`
	ProofRunVersion   uint      `json:"proofRunVersion"`
	ProofValue        float64   `json:"proofValue"`
	ProofUnit         string    `json:"proofUnit" gorm:"size:24"`
	Actor             string    `json:"actor" gorm:"size:80;not null"`
	RequestID         string    `json:"requestId" gorm:"size:80;not null"`
	Reason            string    `json:"reason" gorm:"size:500;not null"`
	CreatedAt         time.Time `json:"createdAt"`
}
