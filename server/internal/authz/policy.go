package authz

import (
	"context"
	"errors"
	"web_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Level mengembalikan hak akses efektif user terhadap sebuah koneksi database.
// Akses hanya berasal dari group; menjadi Platform Admin tidak memberi akses
// otomatis ke isi database (docs/api-contract.md bagian 3.5).
func Level(ctx context.Context, db *gorm.DB, userID, connectionID string) (models.AccessLevel, error) {
	if db == nil {
		return models.AccessNone, nil
	}
	if _, err := uuid.Parse(userID); err != nil {
		return models.AccessNone, nil
	}
	if _, err := uuid.Parse(connectionID); err != nil {
		return models.AccessNone, nil
	}

	var user models.User
	if err := db.WithContext(ctx).Select("id").First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.AccessNone, nil
		}
		return models.AccessNone, err
	}

	var levels []string
	err := db.WithContext(ctx).
		Table("group_database_access AS gda").
		Joins("JOIN group_members AS gm ON gm.group_id = gda.group_id").
		Where("gm.user_id = ? AND gda.connection_id = ?", userID, connectionID).
		Pluck("gda.level", &levels).Error
	if err != nil {
		return models.AccessNone, err
	}

	level := models.AccessNone
	for _, value := range levels {
		switch models.AccessLevel(value) {
		case models.AccessEdit:
			return models.AccessEdit, nil
		case models.AccessView:
			level = models.AccessView
		}
	}
	return level, nil
}

func CanView(ctx context.Context, db *gorm.DB, userID, connectionID string) (bool, error) {
	level, err := Level(ctx, db, userID, connectionID)
	return level == models.AccessView || level == models.AccessEdit, err
}

func CanEdit(ctx context.Context, db *gorm.DB, userID, connectionID string) (bool, error) {
	level, err := Level(ctx, db, userID, connectionID)
	return level == models.AccessEdit, err
}
