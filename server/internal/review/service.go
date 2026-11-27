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
	ErrAlreadyPending      = errors.New("migration already has a change request in progress")
	ErrGroupsWithoutLeader = errors.New("every group with access must have a leader before review")
	ErrNotPending          = errors.New("change request is not pending review")
	ErrNotApproved         = errors.New("change request is not approved")
	ErrNotLeader           = errors.New("only a group leader of this database can decide")
	ErrAlreadyDecided      = errors.New("you have already decided on this change request")
	ErrStale               = errors.New("change request is no longer valid; submit a new one")
	ErrDispatchFailed      = errors.New("approved but could not be queued")
	ErrBatchRevert         = errors.New("batch migration must be cancelled/reverted via /batch/cancel")
	ErrSelfApproval        = errors.New("cannot approve your own change request")
	ErrNotRevertible       = errors.New("migration has no committed batch to revert")
)

type EnqueueFunc func(ctx context.Context, cr models.ChangeRequest) (jobID string, err error)

type Deps struct {
	Enqueue          EnqueueFunc
	Validate         func(*models.Migration) error
	DenySelfApproval bool
}

type Service struct {
	db   *gorm.DB
	deps Deps
}

func New(db *gorm.DB, deps Deps) *Service { return &Service{db: db, deps: deps} }

func (s *Service) Submit(ctx context.Context, userID, connectionID, migrationID string, kind models.JobKind) (*models.ChangeRequest, error) {
	if kind != models.JobKindUp && kind != models.JobKindRollback && kind != models.JobKindForward && kind != models.JobKindRevert {
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

		resume := kind == models.JobKindUp && isBatchResume(&m)
		switch kind {
		case models.JobKindUp:
			if m.Status != models.MigrationDraft && !resume {
				return ErrNotDraft
			}
			if s.deps.Validate != nil {
				if err := s.deps.Validate(&m); err != nil {
					return err
				}
			}
		case models.JobKindForward:
			if m.BatchSize > 0 {
				return ErrBatchRevert
			}
			if err := forwardCheck(tx, &m); err != nil {
				return err
			}
		case models.JobKindRevert:
			if m.BatchSize <= 0 || m.BatchRunID == nil || (m.Status != models.MigrationSuccess && m.Status != models.MigrationPartial) {
				return ErrNotRevertible
			}
		default:
			if m.BatchSize > 0 {
				return ErrBatchRevert
			}
			target, err := rollbackTarget(tx, &m)
			if err != nil {
				return err
			}
			cr.RollbackOf = &target.ID
		}

		inFlight, err := hasInFlight(tx, m.ID)
		if err != nil {
			return err
		}
		if inFlight {
			return ErrAlreadyPending
		}
		if err := tx.Create(&cr).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrAlreadyPending
			}
			return err
		}

		// Resume batch tetap berstatus partial selama direview; checkpoint-nya tidak boleh hilang.
		if kind == models.JobKindUp && !resume {
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
		if s.deps.DenySelfApproval && cr.RequestedBy == userID {
			return ErrSelfApproval
		}

		var prior models.Approval
		if err := tx.Where("change_request_id = ? AND approver_id = ?", cr.ID, userID).Limit(1).Find(&prior).Error; err != nil {
			return err
		}
		alreadyDecided := prior.ID != ""
		if alreadyDecided {
			if decision != models.DecisionApprove || prior.Decision != models.DecisionApprove {
				return ErrAlreadyDecided
			}
		} else if err := tx.Create(&models.Approval{
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
		if alreadyDecided {
			return ErrAlreadyDecided
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &cr, approved, nil
}

// Dispatch mengirim permintaan yang sudah disetujui ke queue.
func (s *Service) Dispatch(ctx context.Context, requestID string) (*models.ChangeRequest, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return nil, ErrRequestNotFound
	}

	var cr models.ChangeRequest
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cr, "id = ?", requestID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRequestNotFound
		}
		if err != nil {
			return err
		}
		if cr.Status != models.ChangeRequestApproved {
			return ErrNotApproved
		}
		if cr.JobID != nil {
			return nil
		}

		// Cari job yang sudah ada lebih dulu: job itu bisa sudah selesai sehingga pemeriksaan basi di bawah
		// salah menilainya.
		jobID, err := findJob(tx, &cr)
		if err != nil {
			return err
		}
		if jobID == "" {
			if err := checkStillValid(tx, &cr); err != nil {
				return err
			}
			if jobID, err = s.deps.Enqueue(ctx, cr); err != nil {
				return fmt.Errorf("%w: %w", ErrDispatchFailed, err)
			}
		}
		if err := tx.Model(&cr).Update("job_id", jobID).Error; err != nil {
			return err
		}
		cr.JobID = &jobID
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrDispatchFailed) || errors.Is(err, ErrStale) {
			return &cr, err
		}
		return nil, err
	}
	return &cr, nil
}

// findJob mencari job yang sudah dibuat untuk permintaan ini. Job dari permintaan sebelumnya tidak ikut
// karena permintaan baru untuk migration yang sama hanya bisa diajukan setelah yang lama selesai.
func findJob(tx *gorm.DB, cr *models.ChangeRequest) (string, error) {
	q := tx.Model(&models.MigrationJob{}).
		Where("migration_id = ? AND kind = ? AND created_at >= ?", cr.MigrationID, cr.Kind, cr.CreatedAt)
	if cr.Kind == models.JobKindRollback && cr.RollbackOf != nil {
		q = q.Where("rollback_of = ?", *cr.RollbackOf)
	}
	var ids []string
	if err := q.Order("created_at").Limit(1).Pluck("id", &ids).Error; err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

// StillValid dipakai worker untuk revalidation setelah lock: aturan yang sama dengan saat dispatch, diterapkan
// pada job yang akan dijalankan.
func StillValid(tx *gorm.DB, job *models.MigrationJob) error {
	return checkStillValid(tx, &models.ChangeRequest{MigrationID: job.MigrationID, Kind: job.Kind, RollbackOf: job.RollbackOf})
}

// checkStillValid memastikan rollback/forward yang sudah disetujui masih sesuai keadaan terkini.
func checkStillValid(tx *gorm.DB, cr *models.ChangeRequest) error {
	if cr.Kind == models.JobKindUp || cr.Kind == models.JobKindRevert {
		return nil
	}
	var m models.Migration
	if err := tx.First(&m, "id = ?", cr.MigrationID).Error; err != nil {
		return err
	}
	if cr.Kind == models.JobKindForward {
		if err := forwardCheck(tx, &m); err != nil {
			return fmt.Errorf("%w: %w", ErrStale, err)
		}
		return nil
	}
	target, err := rollbackTarget(tx, &m)
	if err != nil || cr.RollbackOf == nil || target.ID != *cr.RollbackOf {
		return fmt.Errorf("%w: rollback target changed", ErrStale)
	}
	return nil
}

// hasInFlight: ada permintaan untuk migration ini yang masih menunggu review, sudah disetujui tetapi belum
// masuk queue, atau sudah masuk queue tetapi jobnya belum selesai.
func hasInFlight(tx *gorm.DB, migrationID string) (bool, error) {
	var n int64
	err := tx.Model(&models.ChangeRequest{}).
		Where(`migration_id = ? AND (status = ? OR (status = ? AND (job_id IS NULL OR job_id IN (
			SELECT id FROM migration_jobs WHERE status NOT IN ?))))`,
			migrationID, models.ChangeRequestPendingReview, models.ChangeRequestApproved,
			[]string{string(models.JobSuccess), string(models.JobFailed)}).
		Count(&n).Error
	return n > 0, err
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
	var m models.Migration
	if err := tx.First(&m, "id = ?", cr.MigrationID).Error; err != nil {
		return err
	}
	if isBatchResume(&m) {
		return nil // engine yang memindahkan status saat job resume dibuat
	}
	return moveMigration(tx, cr.MigrationID, models.MigrationPendingReview, migration)
}

// isBatchResume: pengajuan up untuk migration batch yang berhenti di checkpoint berarti "lanjutkan dari checkpoint".
// Lanjutan tetap perlu approval leader seperti eksekusi lain.
func isBatchResume(m *models.Migration) bool {
	return m.BatchSize > 0 && m.BatchRunID != nil && m.Status == models.MigrationPartial
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

type groupRow struct {
	ConnectionID string
	GroupID      string
	GroupName    string
	LeaderID     *string
	LeaderName   *string
}

// queryGroups mengambil semua group yang punya akses (view maupun edit) ke database yang diberikan. Leader yang
// akunnya sudah dihapus dianggap tidak ada, sehingga group-nya dihitung belum punya leader.
func queryGroups(tx *gorm.DB, connectionIDs ...string) ([]groupRow, error) {
	var rows []groupRow
	err := tx.Table("groups AS g").
		Select("gda.connection_id AS connection_id, g.id AS group_id, g.name AS group_name, u.id AS leader_id, "+
			"COALESCE(NULLIF(u.name, ''), u.username) AS leader_name").
		Joins("JOIN group_database_access AS gda ON gda.group_id = g.id").
		Joins("LEFT JOIN users AS u ON u.id = g.leader_id AND u.deleted_at IS NULL").
		Where("gda.connection_id IN ?", connectionIDs).
		Order("g.id").Scan(&rows).Error
	return rows, err
}

// Semua group yang punya akses (view maupun edit) ikut menentukan approval.
func loadGroups(tx *gorm.DB, connectionID string) ([]workflow.Group, error) {
	rows, err := queryGroups(tx, connectionID)
	if err != nil {
		return nil, err
	}
	groups := make([]workflow.Group, 0, len(rows))
	for _, r := range rows {
		g := workflow.Group{ID: r.GroupID}
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
