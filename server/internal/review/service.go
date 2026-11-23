package review

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"web_backend/internal/authz"
	"web_backend/internal/models"
	"web_backend/internal/workflow"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrEditAccessRequired  = errors.New("edit access required")
	ErrMigrationNotFound   = errors.New("migration not found")
	ErrRequestNotFound     = errors.New("change request not found")
	ErrInvalidKind         = errors.New("unsupported change request kind")
	ErrNotDraft            = errors.New("only draft migrations can be submitted for review")
	ErrAlreadyPending      = errors.New("migration already has a change request pending review")
	ErrGroupsWithoutLeader = errors.New("every group with access must have a leader before review")
	ErrNotPending          = errors.New("change request is not pending review")
	ErrNotApproved         = errors.New("change request is not approved")
	ErrNotLeader           = errors.New("only a group leader of this database can decide")
	ErrAlreadyDecided      = errors.New("you have already decided on this change request")
	ErrStale               = errors.New("change request is no longer valid; submit a new one")
	ErrDispatchFailed      = errors.New("approved but could not be queued")
)

type EnqueueFunc func(ctx context.Context, cr models.ChangeRequest) (jobID string, err error)

type Deps struct {
	Enqueue  EnqueueFunc
	Validate func(*models.Migration) error
}

type Service struct {
	db   *gorm.DB
	deps Deps
}

func New(db *gorm.DB, deps Deps) *Service { return &Service{db: db, deps: deps} }

func (s *Service) Submit(ctx context.Context, userID, connectionID, migrationID string, kind models.JobKind) (*models.ChangeRequest, error) {
	if kind != models.JobKindUp && kind != models.JobKindRollback && kind != models.JobKindForward {
		return nil, ErrInvalidKind
	}
	canEdit, err := authz.CanEdit(ctx, s.db, userID, connectionID)
	if err != nil {
		return nil, err
	}
	if !canEdit {
		return nil, ErrEditAccessRequired
	}
	if _, err := uuid.Parse(migrationID); err != nil {
		return nil, ErrMigrationNotFound
	}

	var cr models.ChangeRequest
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m models.Migration
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&m, "id = ? AND connection_id = ?", migrationID, connectionID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMigrationNotFound
		}
		if err != nil {
			return err
		}

		groups, err := loadGroups(tx, connectionID)
		if err != nil {
			return err
		}
		if res := workflow.Evaluate(groups, nil); len(res.NoLeader) > 0 {
			return fmt.Errorf("%w: %s", ErrGroupsWithoutLeader, strings.Join(res.NoLeader, ", "))
		}

		cr = models.ChangeRequest{
			ConnectionID: connectionID,
			MigrationID:  m.ID,
			Kind:         kind,
			Status:       models.ChangeRequestPendingReview,
			RequestedBy:  userID,
		}

		switch kind {
		case models.JobKindUp:
			if m.Status != models.MigrationDraft {
				return ErrNotDraft
			}
			if s.deps.Validate != nil {
				if err := s.deps.Validate(&m); err != nil {
					return err
				}
			}
		case models.JobKindForward:
			if err := forwardCheck(tx, &m); err != nil {
				return err
			}
		default:
			target, err := rollbackTarget(tx, &m)
			if err != nil {
				return err
			}
			cr.RollbackOf = &target.ID
		}

		var pending int64
		if err := tx.Model(&models.ChangeRequest{}).
			Where("migration_id = ? AND status = ?", m.ID, models.ChangeRequestPendingReview).
			Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return ErrAlreadyPending
		}
		if err := tx.Create(&cr).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrAlreadyPending
			}
			return err
		}

		if kind == models.JobKindUp {
			return moveMigration(tx, m.ID, models.MigrationDraft, models.MigrationPendingReview)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &cr, nil
}

// Approve mencatat persetujuan; jika semua leader sudah setuju, permintaan langsung dikirim ke queue.
func (s *Service) Approve(ctx context.Context, userID, requestID, comment string) (*models.ChangeRequest, error) {
	cr, approved, err := s.decide(ctx, userID, requestID, models.DecisionApprove, comment)
	if err != nil || !approved {
		return cr, err
	}
	return s.Dispatch(ctx, cr.ID)
}

func (s *Service) Reject(ctx context.Context, userID, requestID, comment string) (*models.ChangeRequest, error) {
	cr, _, err := s.decide(ctx, userID, requestID, models.DecisionReject, comment)
	return cr, err
}

func (s *Service) decide(ctx context.Context, userID, requestID string, decision models.ApprovalDecision, comment string) (*models.ChangeRequest, bool, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return nil, false, ErrRequestNotFound
	}

	var cr models.ChangeRequest
	approved := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cr, "id = ?", requestID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRequestNotFound
		}
		if err != nil {
			return err
		}
		if cr.Status != models.ChangeRequestPendingReview {
			return ErrNotPending
		}

		groups, err := loadGroups(tx, cr.ConnectionID)
		if err != nil {
			return err
		}
		if !workflow.CanApprove(userID, groups) {
			return ErrNotLeader
		}

		var decided int64
		if err := tx.Model(&models.Approval{}).
			Where("change_request_id = ? AND approver_id = ?", cr.ID, userID).Count(&decided).Error; err != nil {
			return err
		}
		if decided > 0 {
			return ErrAlreadyDecided
		}
		if err := tx.Create(&models.Approval{
			ChangeRequestID: cr.ID, ApproverID: userID, Decision: decision, Comment: comment,
		}).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrAlreadyDecided
			}
			return err
		}

		if decision == models.DecisionReject {
			return settle(tx, &cr, models.ChangeRequestRejected, models.MigrationDraft)
		}

		var approvers []string
		if err := tx.Model(&models.Approval{}).
			Where("change_request_id = ? AND decision = ?", cr.ID, models.DecisionApprove).
			Pluck("approver_id", &approvers).Error; err != nil {
			return err
		}
		if workflow.Evaluate(groups, approvers).Approved {
			approved = true
			return settle(tx, &cr, models.ChangeRequestApproved, models.MigrationApproved)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &cr, approved, nil
}

// Dispatch mengirim permintaan yang sudah disetujui ke queue; aman dipanggil ulang jika sebelumnya gagal.
func (s *Service) Dispatch(ctx context.Context, requestID string) (*models.ChangeRequest, error) {
	var cr models.ChangeRequest
	err := s.db.WithContext(ctx).First(&cr, "id = ?", requestID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequestNotFound
	}
	if err != nil {
		return nil, err
	}
	if cr.Status != models.ChangeRequestApproved {
		return nil, ErrNotApproved
	}
	if cr.JobID != nil {
		return &cr, nil
	}

	if cr.Kind == models.JobKindRollback {
		var m models.Migration
		if err := s.db.WithContext(ctx).First(&m, "id = ?", cr.MigrationID).Error; err != nil {
			return nil, err
		}
		target, err := rollbackTarget(s.db.WithContext(ctx), &m)
		if err != nil || cr.RollbackOf == nil || target.ID != *cr.RollbackOf {
			return &cr, fmt.Errorf("%w: rollback target changed", ErrStale)
		}
	}
	if cr.Kind == models.JobKindForward {
		var m models.Migration
		if err := s.db.WithContext(ctx).First(&m, "id = ?", cr.MigrationID).Error; err != nil {
			return nil, err
		}
		if err := forwardCheck(s.db.WithContext(ctx), &m); err != nil {
			return &cr, fmt.Errorf("%w: %w", ErrStale, err)
		}
	}

	jobID, err := s.deps.Enqueue(ctx, cr)
	if err != nil {
		return &cr, fmt.Errorf("%w: %w", ErrDispatchFailed, err)
	}
	if err := s.db.WithContext(ctx).Model(&cr).
		Where("job_id IS NULL").Update("job_id", jobID).Error; err != nil {
		return &cr, err
	}
	cr.JobID = &jobID
	return &cr, nil
}

func settle(tx *gorm.DB, cr *models.ChangeRequest, status models.ChangeRequestStatus, migration models.MigrationStatus) error {
	now := time.Now()
	if err := tx.Model(cr).Updates(map[string]any{"status": status, "decided_at": now}).Error; err != nil {
		return err
	}
	cr.Status, cr.DecidedAt = status, &now
	if cr.Kind != models.JobKindUp {
		return nil
	}
	return moveMigration(tx, cr.MigrationID, models.MigrationPendingReview, migration)
}

func moveMigration(tx *gorm.DB, id string, from, to models.MigrationStatus) error {
	if _, err := workflow.Transition(workflow.Status(from), workflow.Status(to)); err != nil {
		return err
	}
	res := tx.Model(&models.Migration{}).Where("id = ? AND status = ?", id, from).Update("status", to)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrStale
	}
	return nil
}

// Semua group yang punya akses (view maupun edit) ikut menentukan approval.
func loadGroups(tx *gorm.DB, connectionID string) ([]workflow.Group, error) {
	var rows []struct {
		ID       string
		LeaderID *string
	}
	err := tx.Table("groups AS g").
		Select("g.id AS id, g.leader_id AS leader_id").
		Joins("JOIN group_database_access AS gda ON gda.group_id = g.id").
		Where("gda.connection_id = ?", connectionID).
		Order("g.id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	groups := make([]workflow.Group, 0, len(rows))
	for _, r := range rows {
		g := workflow.Group{ID: r.ID}
		if r.LeaderID != nil {
			g.LeaderID = *r.LeaderID
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// Beberapa attempt dengan execution ID yang sama dihitung sebagai satu eksekusi; attempt terakhir menentukan hasilnya.
func loadHistory(tx *gorm.DB, connectionID string) ([]workflow.Execution, error) {
	var rows []models.MigrationExecution
	if err := tx.Where("connection_id = ?", connectionID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return collapse(rows), nil
}

func collapse(rows []models.MigrationExecution) []workflow.Execution {
	var history []workflow.Execution
	index := make(map[string]int)
	for _, r := range rows {
		e := workflow.Execution{
			ID:          r.ExecutionID,
			MigrationID: r.MigrationID,
			Kind:        workflow.Kind(r.Kind),
			Status:      workflow.Status(r.Status),
		}
		if r.RollbackOf != nil {
			e.RollbackOf = *r.RollbackOf
		}
		if i, ok := index[r.ExecutionID]; ok {
			history[i].Status = e.Status
			continue
		}
		index[r.ExecutionID] = len(history)
		history = append(history, e)
	}
	return history
}

// History mengembalikan seluruh attempt di sebuah database secara kronologis. Dengan hideRollback, eksekusi yang
// dibatalkan dan rollback sukses yang membatalkannya disembunyikan
func (s *Service) History(ctx context.Context, connectionID string, hideRollback bool) ([]models.MigrationExecution, error) {
	rows := make([]models.MigrationExecution, 0)
	if err := s.db.WithContext(ctx).Where("connection_id = ?", connectionID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	if !hideRollback {
		return rows, nil
	}
	visible := make(map[string]bool)
	for _, e := range workflow.HideRollback(collapse(rows)) {
		visible[e.ID] = true
	}
	shown := make([]models.MigrationExecution, 0, len(rows))
	for _, r := range rows {
		if visible[r.ExecutionID] {
			shown = append(shown, r)
		}
	}
	return shown, nil
}

func rollbackTarget(tx *gorm.DB, m *models.Migration) (workflow.Execution, error) {
	history, err := loadHistory(tx, m.ConnectionID)
	if err != nil {
		return workflow.Execution{}, err
	}
	reversible := !m.Irreversible && strings.TrimSpace(m.SQLDown) != ""
	return workflow.RollbackTarget(m.ID, reversible, history)
}

// Urutan linear = urutan kemunculan pertama migration di riwayat eksekusi.
func migrationOrder(history []workflow.Execution) []string {
	seen := make(map[string]bool)
	var order []string
	for _, e := range history {
		if !seen[e.MigrationID] {
			seen[e.MigrationID] = true
			order = append(order, e.MigrationID)
		}
	}
	return order
}

func forwardCheck(tx *gorm.DB, m *models.Migration) error {
	history, err := loadHistory(tx, m.ConnectionID)
	if err != nil {
		return err
	}
	order := migrationOrder(history)
	if !slices.Contains(order, m.ID) {
		order = append(order, m.ID) // belum pernah dieksekusi: CheckForward menolaknya sebagai ErrNeverExecuted
	}
	return workflow.CheckForward(m.ID, order, history)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
