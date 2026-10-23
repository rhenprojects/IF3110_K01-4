package main

import (
	"log"
	"os"
	"web_backend/internal/api"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"
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
		if err := db.AutoMigrate(&models.User{}, &models.DBConnection{}); err != nil {
			log.Printf("[WARN] Gagal melakukan migrasi database: %v", err)
		}
		created, err := userhandler.EnsureAdmin(db, os.Getenv("ADMIN_USERNAME"), os.Getenv("ADMIN_PASSWORD"))
		switch {
		case err != nil:
			log.Printf("[WARN] Gagal menyiapkan Platform Admin: %v", err)
		case created:
			log.Printf("Platform Admin %q dibuat dari ADMIN_USERNAME", os.Getenv("ADMIN_USERNAME"))
		}
	}

	var encKey []byte
	if encoded := os.Getenv("DB_ENC_KEY"); encoded != "" {
		if encKey, err = targetdb.ParseKey(encoded); err != nil {
			log.Fatalf("DB_ENC_KEY tidak valid: %v", err)
		}
	} else {
		log.Println("[WARN] DB_ENC_KEY belum diset. Kredensial database target memakai key acak dan tidak bisa didekripsi setelah server restart")
		encKey = targetdb.RandomKey()
	}
	connCipher, err := targetdb.NewCipher(encKey)
	if err != nil {
		log.Fatalf("Gagal membuat cipher: %v", err)
	}
	var connService *targetdb.Service
	if db != nil {
		connService = targetdb.NewService(db, connCipher, targetdb.NewPoolManager())
	}

	devAuth := os.Getenv("DEV_AUTH") == "true"
	if devAuth {
		log.Println("[WARN] DEV_AUTH aktif: identitas user diambil dari header X-Debug-User. Jangan dipakai di production")
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
	api.SetupRoutes(app, db, connService, devAuth)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	log.Printf("Server berhasil dijalankan pada http://localhost:%s", port)
	log.Fatal(app.Listen(":" + port))
}
