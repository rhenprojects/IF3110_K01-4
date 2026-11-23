package databasehandler

import (
	"context"
	"errors"
	"web_backend/internal/authctx"
	"web_backend/internal/engine"
	"web_backend/internal/models"
	"web_backend/internal/review"
	"web_backend/internal/workflow"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// reviewConflicts adalah penolakan karena keadaan permintaan atau riwayat migration saat ini (HTTP 409).
var reviewConflicts = []error{
	review.ErrNotDraft, review.ErrAlreadyPending, review.ErrGroupsWithoutLeader, review.ErrNotPending,
	review.ErrAlreadyDecided, review.ErrStale,
	workflow.ErrAlreadyActive, workflow.ErrNeverExecuted, workflow.ErrOutOfOrder, workflow.ErrNotInSequence,
	workflow.ErrNotActive, workflow.ErrIrreversible, workflow.ErrNotLastActive,
}

func isReviewConflict(err error) bool {
	for _, target := range reviewConflicts {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// reviewEnqueue meneruskan change request yang sudah disetujui ke queue engine.
func reviewEnqueue(eng *engine.Engine) review.EnqueueFunc {
	return func(ctx context.Context, cr models.ChangeRequest) (string, error) {
		req := engine.EnqueueRequest{
			ConnectionID: cr.ConnectionID, MigrationID: cr.MigrationID, Kind: cr.Kind, RequestedBy: cr.RequestedBy,
		}
		if cr.RollbackOf != nil {
			req.RollbackOf = *cr.RollbackOf
		}
		job, err := eng.Enqueue(ctx, req)
		if err != nil {
			return "", err
		}
		return job.ID, nil
	}
}

// SubmitChangeRequest mengajukan up, rollback, atau forward untuk direview leader; belum ada SQL yang dijalankan.
func (h *Handler) SubmitChangeRequest(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}
	var req struct {
		Kind models.JobKind `json:"kind"`
	}
	if err := c.BodyParser(&req); err != nil {
		return h.writeError(c, &validationError{"kind", "invalid request body"})
	}
	cr, err := h.review.Submit(c.UserContext(), authctx.UserID(c), conn.ID, c.Params("mid"), req.Kind)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(cr)
}

func (h *Handler) ListChangeRequests(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	requests := make([]models.ChangeRequest, 0)
	if err := h.db.WithContext(c.UserContext()).Where("connection_id = ?", conn.ID).
		Order("created_at DESC").Limit(queryInt(c.Query("limit"), 50, 1, 200)).Find(&requests).Error; err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.ChangeRequestListResponse{ChangeRequests: requests})
}

func (h *Handler) ApproveChangeRequest(c *fiber.Ctx) error {
	return h.decideChangeRequest(c, h.review.Approve)
}

func (h *Handler) RejectChangeRequest(c *fiber.Ctx) error {
	return h.decideChangeRequest(c, h.review.Reject)
}

// decideChangeRequest tidak memeriksa level akses: hak memutuskan ada pada leader group (diperiksa review.Service),
// dan leader tidak harus anggota group-nya.
func (h *Handler) decideChangeRequest(c *fiber.Ctx, decide func(ctx context.Context, userID, requestID, comment string) (*models.ChangeRequest, error)) error {
	requestID, connectionID := c.Params("rid"), c.Params("id")
	if _, err := uuid.Parse(requestID); err != nil {
		return h.writeError(c, review.ErrRequestNotFound)
	}
	if _, err := uuid.Parse(connectionID); err != nil {
		return h.writeError(c, review.ErrRequestNotFound)
	}
	// Permintaan hanya boleh diputuskan lewat database miliknya.
	var cr models.ChangeRequest
	if err := h.db.WithContext(c.UserContext()).First(&cr, "id = ? AND connection_id = ?", requestID, connectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return h.writeError(c, review.ErrRequestNotFound)
		}
		return h.writeError(c, err)
	}
	var req struct {
		Comment string `json:"comment"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return h.writeError(c, &validationError{"comment", "invalid request body"})
		}
	}
	updated, err := decide(c.UserContext(), authctx.UserID(c), cr.ID, req.Comment)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(updated)
}

// ListHistory mengembalikan seluruh attempt eksekusi di database ini; ?hide_rollback=true menyembunyikan
// pasangan eksekusi yang dibatalkan dan rollback sukses yang membatalkannya.
func (h *Handler) ListHistory(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	executions, err := h.review.History(c.UserContext(), conn.ID, c.Query("hide_rollback") == "true")
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.ExecutionListResponse{Executions: executions})
}
