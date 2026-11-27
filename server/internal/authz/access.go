package authz

import (
	"context"
	"errors"
	"web_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Access adalah hak efektif user pada satu koneksi plus apakah user memenuhi
// syarat menjadi pelaksana batch (leader semua group yang punya akses).
type Access struct {
	Level    models.AccessLevel
	CanBatch bool
}

// AccessFor menghitung hak banyak koneksi sekaligus agar daftar database tidak
// menjadi N+1 query.
func AccessFor(ctx context.Context, db *gorm.DB, userID string, connectionIDs []string) (map[string]Access, error) {
	result := make(map[string]Access, len(connectionIDs))
	if len(connectionIDs) == 0 {
		return result, nil
	}
	if _, err := uuid.Parse(userID); err != nil {
		return result, nil
	}
	var user models.User
	if err := db.WithContext(ctx).Select("id").First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, nil
		}
		return nil, err
	}

	var levels []struct {
		ConnectionID string
		Level        string
	}
	if err := db.WithContext(ctx).
		Table("group_database_access AS gda").
		Select("gda.connection_id::text AS connection_id, gda.level").
		Joins("JOIN group_members AS gm ON gm.group_id = gda.group_id").
		Where("gm.user_id = ? AND gda.connection_id::text IN ?", userID, connectionIDs).
		Scan(&levels).Error; err != nil {
		return nil, err
	}
	for _, row := range levels {
		access := result[row.ConnectionID]
		if models.AccessLevel(row.Level) == models.AccessEdit || access.Level == models.AccessNone {
			access.Level = models.AccessLevel(row.Level)
		}
		if access.Level != models.AccessEdit && models.AccessLevel(row.Level) == models.AccessView {
			access.Level = models.AccessView
		}
		result[row.ConnectionID] = access
	}

	var leaders []struct {
		ConnectionID string
		LeaderID     *string
	}
	if err := db.WithContext(ctx).
		Table("group_database_access AS gda").
		Select("gda.connection_id::text AS connection_id, g.leader_id AS leader_id").
		Joins("JOIN groups AS g ON g.id = gda.group_id").
		Where("gda.connection_id::text IN ?", connectionIDs).
		Scan(&leaders).Error; err != nil {
		return nil, err
	}
	groupCount := make(map[string]int, len(connectionIDs))
	allLead := make(map[string]bool, len(connectionIDs))
	for _, row := range leaders {
		groupCount[row.ConnectionID]++
		ok := row.LeaderID != nil && *row.LeaderID == userID
		if _, seen := allLead[row.ConnectionID]; !seen {
			allLead[row.ConnectionID] = ok
		} else {
			allLead[row.ConnectionID] = allLead[row.ConnectionID] && ok
		}
	}
	for id, access := range result {
		access.CanBatch = groupCount[id] > 0 && allLead[id]
		result[id] = access
	}
	return result, nil
}
