package models

import "time"

type ChangeRequestStatus string

const (
	ChangeRequestPendingReview ChangeRequestStatus = "pending_review"
	ChangeRequestApproved      ChangeRequestStatus = "approved"
	ChangeRequestRejected      ChangeRequestStatus = "rejected"
)

// ChangeRequest adalah permintaan perubahan (up, rollback, atau forward) yang menunggu approval leader.
type ChangeRequest struct {
	ID           string              `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ConnectionID string              `json:"connection_id" gorm:"column:connection_id;type:uuid;not null;index"`
	MigrationID  string              `json:"migration_id" gorm:"column:migration_id;type:uuid;not null;index;index:idx_change_requests_pending,unique,where:status = 'pending_review'"`
	Kind         JobKind             `json:"kind" gorm:"column:kind;type:varchar(10);not null;check:chk_change_requests_kind,kind IN ('up','rollback','forward')"`
	RollbackOf   *string             `json:"rollback_of" gorm:"column:rollback_of;type:uuid;check:chk_change_requests_rollback_of,(kind = 'rollback') = (rollback_of IS NOT NULL)"`
	Status       ChangeRequestStatus `json:"status" gorm:"column:status;type:varchar(20);not null;default:'pending_review';check:chk_change_requests_status,status IN ('pending_review','approved','rejected')"`
	RequestedBy  string              `json:"requested_by" gorm:"column:requested_by;type:varchar(64);not null"`
	JobID        *string             `json:"job_id" gorm:"column:job_id;type:uuid"`
	CreatedAt    time.Time           `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt    time.Time           `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	DecidedAt    *time.Time          `json:"decided_at" gorm:"column:decided_at;type:timestamptz"`

	Connection DBConnection `json:"-" gorm:"foreignKey:ConnectionID;constraint:OnDelete:RESTRICT"`
	Migration  Migration    `json:"-" gorm:"foreignKey:MigrationID;constraint:OnDelete:RESTRICT"`
}

func (ChangeRequest) TableName() string { return "change_requests" }

type ChangeRequestListResponse struct {
	ChangeRequests []ChangeRequest `json:"change_requests"`
}

type ApprovalDecision string

const (
	DecisionApprove ApprovalDecision = "approve"
	DecisionReject  ApprovalDecision = "reject"
)

// Approval adalah keputusan satu leader atas satu ChangeRequest.
type Approval struct {
	ID              string           `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ChangeRequestID string           `json:"change_request_id" gorm:"column:change_request_id;type:uuid;not null;uniqueIndex:idx_approvals_request_approver,priority:1"`
	ApproverID      string           `json:"approver_id" gorm:"column:approver_id;type:varchar(64);not null;uniqueIndex:idx_approvals_request_approver,priority:2"`
	Decision        ApprovalDecision `json:"decision" gorm:"column:decision;type:varchar(10);not null;check:chk_approvals_decision,decision IN ('approve','reject')"`
	Comment         string           `json:"comment" gorm:"column:comment;type:text;not null;default:''"`
	CreatedAt       time.Time        `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`

	ChangeRequest ChangeRequest `json:"-" gorm:"foreignKey:ChangeRequestID;constraint:OnDelete:CASCADE"`
}

func (Approval) TableName() string { return "approvals" }
