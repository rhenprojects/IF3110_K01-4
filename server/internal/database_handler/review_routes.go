package databasehandler

import (
	"context"
	"errors"
	"fmt"
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
	review.ErrAlreadyDecided, review.ErrStale, review.ErrNotApproved,
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
func reviewEnqueue(db *gorm.DB, eng *engine.Engine) review.EnqueueFunc {
	return func(ctx context.Context, cr models.ChangeRequest) (string, error) {
		if cr.Kind == models.JobKindUp {
			var m models.Migration
			if err := db.WithContext(ctx).First(&m, "id = ?", cr.MigrationID).Error; err != nil {
				return "", err
			}
			if m.BatchSize > 0 && m.BatchRunID != nil && m.Status == models.MigrationPartial {
				job, err := eng.EnqueueResume(ctx, cr.ConnectionID, cr.MigrationID, cr.RequestedBy)
				if err != nil {
					return "", err
				}
				return job.ID, nil
			}
		}
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
	userID := authctx.UserID(c)
	cr, err := h.review.Submit(c.UserContext(), userID, conn.ID, c.Params("mid"), req.Kind)
	if err != nil {
		return h.writeError(c, err)
	}
	if err := h.review.Enrich(c.UserContext(), userID, cr); err != nil {
		return h.writeError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(cr)
}

// ListChangeRequests mengembalikan change request database ini, terbaru dulu; ?status= menyaring berdasarkan status.
func (h *Handler) ListChangeRequests(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	query := h.db.WithContext(c.UserContext()).Where("connection_id = ?", conn.ID)
	if raw := c.Query("status"); raw != "" {
		switch status := models.ChangeRequestStatus(raw); status {
		case models.ChangeRequestPendingReview, models.ChangeRequestApproved, models.ChangeRequestRejected:
			query = query.Where("status = ?", status)
		default:
			return h.writeError(c, &validationError{"status", "must be pending_review, approved or rejected"})
		}
	}
	requests := make([]models.ChangeRequest, 0)
	if err := query.Order("created_at DESC").Limit(queryInt(c.Query("limit"), 50, 1, 200)).Find(&requests).Error; err != nil {
		return h.writeError(c, err)
	}
	return h.respondRequests(c, requests)
}

// ListPendingChangeRequests adalah inbox leader: permintaan di database mana pun yang menunggu keputusan user ini.
func (h *Handler) ListPendingChangeRequests(c *fiber.Ctx) error {
	requests, err := h.review.PendingFor(c.UserContext(), authctx.UserID(c), queryInt(c.Query("limit"), 50, 1, 200))
	if err != nil {
		return h.writeError(c, err)
	}
	return h.respondRequests(c, requests)
}

func (h *Handler) respondRequests(c *fiber.Ctx, requests []models.ChangeRequest) error {
	ptrs := make([]*models.ChangeRequest, len(requests))
	for i := range requests {
		ptrs[i] = &requests[i]
	}
	if err := h.review.Enrich(c.UserContext(), authctx.UserID(c), ptrs...); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.ChangeRequestListResponse{ChangeRequests: requests})
}

func (h *Handler) ApproveChangeRequest(c *fiber.Ctx) error {
	return h.decideChangeRequest(c, h.review.Approve)
}

// maxBulkApprove membatasi satu panggilan bulk agar durasi request tetap wajar;
// klien dapat memanggil ulang untuk sisanya.
const maxBulkApprove = 100

// bulkApproveResult melaporkan hasil per change request; Error kosong berarti berhasil.
type bulkApproveResult struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// ApproveChangeRequests menyetujui beberapa change request dalam satu panggilan.
// Tiap permintaan tetap diputuskan sendiri-sendiri: leader tiap group, aturan
// self-approval, dispatch ke queue, dan audit log tetap berlaku per request.
// Kegagalan satu permintaan tidak membatalkan yang lain, jadi responsnya 200
// dengan hasil per id.
func (h *Handler) ApproveChangeRequests(c *fiber.Ctx) error {
	connectionID := c.Params("id")
	if _, err := uuid.Parse(connectionID); err != nil {
		return h.writeError(c, review.ErrRequestNotFound)
	}
	var req struct {
		IDs     []string `json:"ids"`
		Comment string   `json:"comment"`
	}
	if err := c.BodyParser(&req); err != nil {
		return h.writeError(c, &validationError{"ids", "invalid request body"})
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBulkApprove {
		return h.writeError(c, &validationError{"ids", fmt.Sprintf("isi 1 sampai %d id", maxBulkApprove)})
	}
	userID := authctx.UserID(c)
	results := make([]bulkApproveResult, 0, len(req.IDs))
	for _, requestID := range req.IDs {
		results = append(results, bulkApproveResult{
			ID:    requestID,
			Error: h.approveInConnection(c.UserContext(), userID, connectionID, requestID, req.Comment),
		})
	}
	return c.JSON(fiber.Map{"results": results})
}

// approveInConnection mengembalikan pesan error (kosong = sukses) agar satu
// kegagalan tidak menggagalkan seluruh panggilan bulk.
func (h *Handler) approveInConnection(ctx context.Context, userID, connectionID, requestID, comment string) string {
	if _, err := uuid.Parse(requestID); err != nil {
		return review.ErrRequestNotFound.Error()
	}
	var cr models.ChangeRequest
	err := h.db.WithContext(ctx).First(&cr, "id = ? AND connection_id = ?", requestID, connectionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return review.ErrRequestNotFound.Error()
	}
	if err != nil {
		return err.Error()
	}
	if _, err := h.review.Approve(ctx, userID, cr.ID, comment); err != nil {
		return err.Error()
	}
	return ""
}

func (h *Handler) RejectChangeRequest(c *fiber.Ctx) error {
	return h.decideChangeRequest(c, h.review.Reject)
}

// ownedRequest memuat change request dari path dan memastikannya milik database di path yang sama.
func (h *Handler) ownedRequest(c *fiber.Ctx) (*models.ChangeRequest, error) {
	requestID, connectionID := c.Params("rid"), c.Params("id")
	if _, err := uuid.Parse(requestID); err != nil {
		return nil, review.ErrRequestNotFound
	}
	if _, err := uuid.Parse(connectionID); err != nil {
		return nil, review.ErrRequestNotFound
	}
	var cr models.ChangeRequest
	if err := h.db.WithContext(c.UserContext()).First(&cr, "id = ? AND connection_id = ?", requestID, connectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, review.ErrRequestNotFound
		}
		return nil, err
	}
	return &cr, nil
}

// decideChangeRequest tidak memeriksa level akses: hak memutuskan ada pada leader group (diperiksa review.Service),
// dan leader tidak harus anggota group-nya.
func (h *Handler) decideChangeRequest(c *fiber.Ctx, decide func(ctx context.Context, userID, requestID, comment string) (*models.ChangeRequest, error)) error {
	// Permintaan hanya boleh diputuskan lewat database miliknya.
	cr, err := h.ownedRequest(c)
	if err != nil {
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
	userID := authctx.UserID(c)
	updated, err := decide(c.UserContext(), userID, cr.ID, req.Comment)
	if err != nil {
		return h.writeError(c, err)
	}
	if err := h.review.Enrich(c.UserContext(), userID, updated); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(updated)
}

// DispatchChangeRequest mencoba lagi memasukkan permintaan yang sudah disetujui tetapi belum masuk queue (502 saat
// persetujuan terakhir). Aman dipanggil berulang: permintaan yang sudah punya job dikembalikan apa adanya.
func (h *Handler) DispatchChangeRequest(c *fiber.Ctx) error {
	if _, _, err := h.accessible(c, true); err != nil {
		return h.writeError(c, err)
	}
	cr, err := h.ownedRequest(c)
	if err != nil {
		return h.writeError(c, err)
	}
	updated, err := h.review.Dispatch(c.UserContext(), cr.ID)
	if err != nil {
		return h.writeError(c, err)
	}
	if err := h.review.Enrich(c.UserContext(), authctx.UserID(c), updated); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(updated)
}

// RecoverApprovals mengirim ulang permintaan yang sudah disetujui tetapi belum sempat masuk queue (mis. setelah restart).
func (h *Handler) RecoverApprovals(ctx context.Context) (int, error) {
	if h.review == nil {
		return 0, nil
	}
	return h.review.RedispatchApproved(ctx)
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
