package authz

import (
	"context"
	"testing"
	"web_backend/internal/models"
)

func TestLevelWithoutDatabase(t *testing.T) {
	level, err := Level(context.Background(), nil, "not-a-uuid", "also-not-a-uuid")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if level != models.AccessNone {
		t.Errorf("level = %q, want none", level)
	}
}

func TestCanViewAndCanEditWithoutAccess(t *testing.T) {
	ctx := context.Background()
	canView, err := CanView(ctx, nil, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	if err != nil || canView {
		t.Errorf("CanView = %v, err = %v; want false, nil", canView, err)
	}
	canEdit, err := CanEdit(ctx, nil, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	if err != nil || canEdit {
		t.Errorf("CanEdit = %v, err = %v; want false, nil", canEdit, err)
	}
}
