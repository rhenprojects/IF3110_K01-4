package audit

import (
	"log"
	"strconv"
	"strings"

	"web_backend/internal/authctx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// Middleware mencatat setiap request mutasi yang berhasil melewati autentikasi.
// Body tidak dicatat agar password/token tidak masuk audit.
func Middleware(db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		err := c.Next()
		method := c.Method()
		if db == nil || (method != fiber.MethodPost && method != fiber.MethodPut && method != fiber.MethodDelete) {
			return err
		}
		path := c.Path()
		if strings.HasSuffix(path, "/users/login") {
			return err
		}
		entry := models.AuditLog{
			ActorID: authctx.UserID(c), Action: method + " " + path,
			TargetType: "http", TargetID: path, IP: c.IP(),
			Detail: "status=" + strconv.Itoa(c.Response().StatusCode()),
		}
		if e := db.WithContext(c.UserContext()).Create(&entry).Error; e != nil {
			log.Printf("[audit] write failed: %v", e)
		}
		return err
	}
}

// Log menulis satu entri audit eksplisit (misalnya login gagal/berhasil).
func Log(db *gorm.DB, actorID, action, targetType, targetID, ip, detail string) {
	if db == nil {
		return
	}
	entry := models.AuditLog{ActorID: actorID, Action: action, TargetType: targetType,
		TargetID: targetID, IP: ip, Detail: detail}
	if err := db.Create(&entry).Error; err != nil {
		log.Printf("[audit] write failed: %v", err)
	}
}
