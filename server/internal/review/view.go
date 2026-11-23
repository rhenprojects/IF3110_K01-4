package review

import (
	"context"
	"log/slog"

	"web_backend/internal/models"

	"gorm.io/gorm"
)

// Enrich mengisi field turunan change request untuk SPA: nama migration, database, dan pemohon, progres approval per
// reviewer, serta can_decide (userID adalah leader yang belum memutuskan permintaan yang masih menunggu review).
func (s *Service) Enrich(ctx context.Context, userID string, crs ...*models.ChangeRequest) error {
	if len(crs) == 0 {
		return nil
	}
	db := s.db.WithContext(ctx)

	var requestIDs, connectionIDs, migrationIDs []string
	seenConnection, seenMigration := make(map[string]bool), make(map[string]bool)
	for _, cr := range crs {
		requestIDs = append(requestIDs, cr.ID)
		if !seenConnection[cr.ConnectionID] {
			seenConnection[cr.ConnectionID] = true
			connectionIDs = append(connectionIDs, cr.ConnectionID)
		}
		if !seenMigration[cr.MigrationID] {
			seenMigration[cr.MigrationID] = true
			migrationIDs = append(migrationIDs, cr.MigrationID)
		}
	}

	groups, err := queryGroups(db, connectionIDs...)
	if err != nil {
		return err
	}
	groupsByConnection := make(map[string][]groupRow)
	for _, g := range groups {
		groupsByConnection[g.ConnectionID] = append(groupsByConnection[g.ConnectionID], g)
	}

	var approvals []models.Approval
	if err := db.Where("change_request_id IN ?", requestIDs).Order("created_at").Find(&approvals).Error; err != nil {
		return err
	}
	type decisionKey struct{ request, approver string }
	decided := make(map[decisionKey]models.Approval, len(approvals))
	approvalsByRequest := make(map[string][]models.Approval)
	for _, a := range approvals {
		decided[decisionKey{a.ChangeRequestID, a.ApproverID}] = a
		approvalsByRequest[a.ChangeRequestID] = append(approvalsByRequest[a.ChangeRequestID], a)
	}

	migrationNames, err := lookupNames(db, &models.Migration{}, migrationIDs)
	if err != nil {
		return err
	}
	connectionNames, err := lookupNames(db, &models.DBConnection{}, connectionIDs)
	if err != nil {
		return err
	}

	var userIDs []string
	seenUser := make(map[string]bool)
	addUser := func(id string) {
		if id != "" && !seenUser[id] {
			seenUser[id] = true
			userIDs = append(userIDs, id)
		}
	}
	for _, cr := range crs {
		addUser(cr.RequestedBy)
	}
	for _, a := range approvals {
		addUser(a.ApproverID)
	}
	userNames, err := lookupUserNames(db, userIDs)
	if err != nil {
		return err
	}

	for _, cr := range crs {
		cr.MigrationName = migrationNames[cr.MigrationID]
		cr.ConnectionName = connectionNames[cr.ConnectionID]
		cr.RequestedByName = userNames[cr.RequestedBy]
		cr.Reviewers = make([]models.Reviewer, 0)
		cr.CanDecide = false

		if cr.Status != models.ChangeRequestPendingReview {
			for _, a := range approvalsByRequest[cr.ID] {
				approver, decidedAt := a.ApproverID, a.CreatedAt
				name := userNames[approver]
				cr.Reviewers = append(cr.Reviewers, models.Reviewer{
					LeaderID: &approver, LeaderName: &name,
					Decision: string(a.Decision), Comment: a.Comment, DecidedAt: &decidedAt,
				})
			}
			continue
		}

		for _, g := range groupsByConnection[cr.ConnectionID] {
			r := models.Reviewer{
				GroupID: g.GroupID, GroupName: g.GroupName, LeaderID: g.LeaderID, LeaderName: g.LeaderName,
				Decision: models.ReviewerPending,
			}
			if g.LeaderID != nil {
				if a, ok := decided[decisionKey{cr.ID, *g.LeaderID}]; ok {
					decidedAt := a.CreatedAt
					r.Decision, r.Comment, r.DecidedAt = string(a.Decision), a.Comment, &decidedAt
				} else if userID != "" && *g.LeaderID == userID {
					cr.CanDecide = true
				}
			}
			cr.Reviewers = append(cr.Reviewers, r)
		}
	}
	return nil
}

// PendingFor mengembalikan permintaan yang masih menunggu review dan belum diputuskan userID, di database mana pun
// tempat userID memimpin sebuah group yang punya akses. Yang paling lama menunggu didahulukan.
func (s *Service) PendingFor(ctx context.Context, userID string, limit int) ([]models.ChangeRequest, error) {
	led := s.db.Table("group_database_access AS gda").
		Select("gda.connection_id").
		Joins("JOIN groups AS g ON g.id = gda.group_id").
		Where("g.leader_id::text = ?", userID)

	crs := make([]models.ChangeRequest, 0)
	err := s.db.WithContext(ctx).
		Where("status = ?", models.ChangeRequestPendingReview).
		Where("connection_id IN (?)", led).
		Where("NOT EXISTS (SELECT 1 FROM approvals AS a WHERE a.change_request_id = change_requests.id AND a.approver_id = ?)", userID).
		Order("created_at").Limit(limit).Find(&crs).Error
	if err != nil {
		return nil, err
	}
	return crs, nil
}

// RedispatchApproved mengirim ulang permintaan yang sudah disetujui tetapi belum punya job, misalnya karena queue tidak
// tersedia saat persetujuan terakhir atau server berhenti sebelum sempat mengirim. Permintaan yang gagal dilewati
// (dicatat di log) dan akan dicoba lagi pada pemanggilan berikutnya.
func (s *Service) RedispatchApproved(ctx context.Context) (int, error) {
	var ids []string
	if err := s.db.WithContext(ctx).Model(&models.ChangeRequest{}).
		Where("status = ? AND job_id IS NULL", models.ChangeRequestApproved).
		Order("decided_at").Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	dispatched := 0
	for _, id := range ids {
		if _, err := s.Dispatch(ctx, id); err != nil {
			slog.WarnContext(ctx, "redispatch approved change request failed",
				"event", "review.redispatch", "status", "failed", "change_request_id", id, "error", err.Error())
			continue
		}
		dispatched++
	}
	return dispatched, nil
}

// lookupNames memetakan id → name untuk model yang punya kolom id dan name.
func lookupNames(db *gorm.DB, model any, ids []string) (map[string]string, error) {
	names := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	var rows []struct {
		ID   string
		Name string
	}
	if err := db.Model(model).Select("id, name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	return names, nil
}

// lookupUserNames memetakan id user → nama tampilan, termasuk user yang sudah dihapus agar riwayat tetap terbaca.
func lookupUserNames(db *gorm.DB, ids []string) (map[string]string, error) {
	names := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	var users []models.User
	// id::text: RequestedBy bisa berisi nilai non-UUID (mis. identitas dev), yang tidak boleh membuat query gagal.
	if err := db.Unscoped().Select("id, username, name").Where("id::text IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		name := u.Name
		if name == "" {
			name = u.Username
		}
		names[u.ID] = name
	}
	return names, nil
}
