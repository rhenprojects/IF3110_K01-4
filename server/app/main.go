package main

import (
	"log"
	"os"
	"web_backend/internal/api"
	"web_backend/internal/models"
	userhandler "web_backend/internal/user_handler"

	fiber "github.com/gofiber/fiber/v2"
	fiberLogger "github.com/gofiber/fiber/v2/middleware/logger"
	fiberRecover "github.com/gofiber/fiber/v2/middleware/recover"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	var db *gorm.DB
	var err error

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=web_backend_db port=5432 sslmode=disable"
	}

	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Printf("[WARN] Gagal terhubung ke database: %v (Menjalankan server tanpa database)", err)
		db = nil
	} else {
		if err := db.AutoMigrate(&models.User{}); err != nil {
			log.Printf("[WARN] Gagal melakukan migrasi database: %v", err)
		}
	}

	hashKey := []byte(os.Getenv("SESSION_HASH_KEY"))
	blockKey := []byte(os.Getenv("SESSION_BLOCK_KEY"))
	if len(hashKey) == 0 || len(blockKey) == 0 {
		log.Println("[WARN] SESSION_HASH_KEY atau SESSION_BLOCK_KEY belum diset. Cookie session memakai key acak dan tidak valid setelah server restart")
	}
	userhandler.InitSession(hashKey, blockKey)

	app := fiber.New()
	app.Use(fiberLogger.New())
	app.Use(fiberRecover.New())
	api.SetupRoutes(app, db)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	log.Printf("Server berhasil dijalankan pada http://localhost:%s", port)
	log.Fatal(app.Listen(":" + port))
}
