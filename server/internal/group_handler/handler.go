package grouphandler

import (
	"errors"
	"log"
	"web_backend/internal/authctx"
	"web_backend/internal/httpx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GroupHandler struct {
	DB *gorm.DB
}

func NewGroupHandler(db *gorm.DB) *GroupHandler {
	return &GroupHandler{DB: db}
}

func (h *GroupHandler) requireDB(c *fiber.Ctx) error {
	if h.DB == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Database is not connected")
	}
	return c.Next()
}

func (h *GroupHandler) RegisterRoutes(router fiber.Router, middleware ...fiber.Handler) {
	handlers := make([]fiber.Handler, 0, len(middleware)+2)
	handlers = append(handlers, middleware...)
	handlers = append(handlers, h.requireDB, authctx.RequireAdmin)
	g := router.Group("/admin/groups", handlers...)

	g.Post("/", h.CreateGroup)
	g.Get("/", h.ListGroups)
	g.Get("/:id", h.GetGroup)
	g.Put("/:id", h.UpdateGroup)
	g.Delete("/:id", h.DeleteGroup)

	g.Get("/:id/members", h.ListMembers)
	g.Post("/:id/members", h.AddMember)
	g.Delete("/:id/members/:userID", h.RemoveMember)

	g.Get("/:id/connections", h.ListAccess)
	g.Put("/:id/connections/:connectionID", h.SetAccess)
	g.Delete("/:id/connections/:connectionID", h.RemoveAccess)
}

func (h *GroupHandler) CreateGroup(c *fiber.Ctx) error {
	var req models.CreateGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}
	name, err := validateName(req.Name)
	if err != nil {
		return writeError(c, err)
	}
	description, err := validateDescription(req.Description)
	if err != nil {
		return writeError(c, err)
	}

	group := models.Group{Name: name, Description: description}
	if err := h.DB.Create(&group).Error; err != nil {
		return writeError(c, translateDBError(err))
	}
	return c.Status(fiber.StatusCreated).JSON(group)
}

func (h *GroupHandler) ListGroups(c *fiber.Ctx) error {
	var groups []models.Group
	if err := h.DB.Order("name").Find(&groups).Error; err != nil {
		log.Printf("[group] list failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
	}
	return c.JSON(models.GroupListResponse{Groups: groups})
}

func (h *GroupHandler) GetGroup(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(group)
}

func (h *GroupHandler) UpdateGroup(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	var req models.UpdateGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name != nil {
		name, err := validateName(*req.Name)
		if err != nil {
			return writeError(c, err)
		}
		group.Name = name
	}
	if req.Description != nil {
		description, err := validateDescription(*req.Description)
		if err != nil {
			return writeError(c, err)
		}
		group.Description = description
	}

	if err := h.DB.Save(group).Error; err != nil {
		return writeError(c, translateDBError(err))
	}
	return c.JSON(group)
}

func (h *GroupHandler) DeleteGroup(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	if err := h.DB.Delete(&models.Group{}, "id = ?", group.ID).Error; err != nil {
		return writeError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *GroupHandler) ListMembers(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	var members []models.GroupMemberView
	err = h.DB.Table("group_members AS gm").
		Select("gm.user_id, u.username, u.name, gm.created_at").
		Joins("JOIN users AS u ON u.id = gm.user_id AND u.deleted_at IS NULL").
		Where("gm.group_id = ?", group.ID).
		Order("u.username").
		Scan(&members).Error
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(models.GroupMemberListResponse{Members: members})
}

func (h *GroupHandler) AddMember(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	var req models.AddGroupMemberRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if _, err := uuid.Parse(req.UserID); err != nil {
		return writeError(c, &ValidationError{"user_id", "must be a valid uuid"})
	}

	var user models.User
	if err := h.DB.First(&user, "id = ?", req.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return httpx.Error(c, fiber.StatusNotFound, "user not found")
		}
		return writeError(c, err)
	}

	member := models.GroupMember{GroupID: group.ID, UserID: user.ID}
	if err := h.DB.Create(&member).Error; err != nil {
		if isUniqueViolation(err) {
			return writeError(c, ErrMemberExists)
		}
		return writeError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(models.GroupMemberView{
		UserID: user.ID, Username: user.Username, Name: user.Name, CreatedAt: member.CreatedAt,
	})
}

func (h *GroupHandler) RemoveMember(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	userID := c.Params("userID")
	if _, err := uuid.Parse(userID); err != nil {
		return writeError(c, &ValidationError{"user_id", "must be a valid uuid"})
	}
	res := h.DB.Delete(&models.GroupMember{}, "group_id = ? AND user_id = ?", group.ID, userID)
	if res.Error != nil {
		return writeError(c, res.Error)
	}
	if res.RowsAffected == 0 {
		return writeError(c, ErrMemberNotFind)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *GroupHandler) ListAccess(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	var access []models.GroupAccessView
	err = h.DB.Table("group_database_access AS gda").
		Select("gda.group_id, gda.connection_id, c.name AS connection_name, gda.level, gda.created_at, gda.updated_at").
		Joins("JOIN db_connections AS c ON c.id = gda.connection_id AND c.deleted_at IS NULL").
		Where("gda.group_id = ?", group.ID).
		Order("c.name").
		Scan(&access).Error
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(models.GroupAccessListResponse{Access: access})
}

func (h *GroupHandler) SetAccess(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	connectionID := c.Params("connectionID")
	if _, err := uuid.Parse(connectionID); err != nil {
		return writeError(c, &ValidationError{"connection_id", "must be a valid uuid"})
	}

	var connection models.DBConnection
	if err := h.DB.First(&connection, "id = ?", connectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return httpx.Error(c, fiber.StatusNotFound, "database connection not found")
		}
		return writeError(c, err)
	}

	var req models.SetGroupAccessRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}
	level, err := validateLevel(req.Level)
	if err != nil {
		return writeError(c, err)
	}

	access := models.GroupDatabaseAccess{
		GroupID: group.ID, ConnectionID: connection.ID, Level: level,
	}
	err = h.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "group_id"}, {Name: "connection_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"level", "updated_at"}),
	}).Create(&access).Error
	if err != nil {
		return writeError(c, err)
	}

	if err := h.DB.First(&access, "group_id = ? AND connection_id = ?", group.ID, connection.ID).Error; err != nil {
		return writeError(c, err)
	}
	return c.JSON(models.GroupAccessView{
		GroupID: access.GroupID, ConnectionID: access.ConnectionID,
		ConnectionName: connection.Name, Level: access.Level,
		CreatedAt: access.CreatedAt, UpdatedAt: access.UpdatedAt,
	})
}

func (h *GroupHandler) RemoveAccess(c *fiber.Ctx) error {
	group, err := h.findGroup(c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	connectionID := c.Params("connectionID")
	if _, err := uuid.Parse(connectionID); err != nil {
		return writeError(c, &ValidationError{"connection_id", "must be a valid uuid"})
	}
	res := h.DB.Delete(&models.GroupDatabaseAccess{}, "group_id = ? AND connection_id = ?", group.ID, connectionID)
	if res.Error != nil {
		return writeError(c, res.Error)
	}
	if res.RowsAffected == 0 {
		return writeError(c, ErrAccessNotFind)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *GroupHandler) findGroup(id string) (*models.Group, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrGroupNotFound
	}
	var group models.Group
	if err := h.DB.First(&group, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	return &group, nil
}

func writeError(c *fiber.Ctx, err error) error {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return httpx.ValidationError(c, verr.Field, verr.Message)
	case errors.Is(err, ErrGroupNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrGroupNotFound.Error())
	case errors.Is(err, ErrDuplicateName):
		return httpx.Error(c, fiber.StatusConflict, ErrDuplicateName.Error())
	case errors.Is(err, ErrMemberExists):
		return httpx.Error(c, fiber.StatusConflict, ErrMemberExists.Error())
	case errors.Is(err, ErrMemberNotFind):
		return httpx.Error(c, fiber.StatusNotFound, ErrMemberNotFind.Error())
	case errors.Is(err, ErrAccessNotFind):
		return httpx.Error(c, fiber.StatusNotFound, ErrAccessNotFind.Error())
	}
	log.Printf("[group] internal error: %v", err)
	return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
}
