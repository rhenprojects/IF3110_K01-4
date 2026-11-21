package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"web_backend/internal/api"
	"web_backend/internal/database_handler"
	"web_backend/internal/engine"
	"web_backend/internal/models"
	"web_backend/internal/observability"
	"web_backend/internal/targetdb"
	"web_backend/internal/telemetry"
	userhandler "web_backend/internal/user_handler"

	fiber "github.com/gofiber/fiber/v2"
	fiberRecover "github.com/gofiber/fiber/v2/middleware/recover"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	logger := telemetry.NewLogger(os.Stdout)
	slog.SetDefault(logger)
	if _, err := telemetry.Setup("dbmp-backend", os.Stdout); err != nil {
		log.Fatalf("Gagal menyiapkan OpenTelemetry: %v", err)
	}

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
		if err := models.Migrate(db); err != nil {
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
	var eng *engine.Engine
	if db != nil {
		connService = targetdb.NewService(db, connCipher, targetdb.NewPoolManager())
		eng, err = engine.New(db, connService, engine.Options{})
		if err != nil {
			log.Fatalf("Gagal membuat migration engine: %v", err)
		}
		if workers := envInt("WORKER_COUNT", 2); workers > 0 {
			eng.Start(workers)
		}
		coordinator := databasehandler.NewHandlerWithEngine(db, connService, eng).NewBatchCoordinator()
		if recovered, err := coordinator.Recover(context.Background()); err != nil {
			log.Printf("[WARN] Gagal memulihkan batch migration: %v", err)
		} else if recovered > 0 {
			log.Printf("Memulihkan %d batch migration yang terhenti", recovered)
		}
	}

	if addr := strings.TrimSpace(os.Getenv("METRICS_ADDR")); addr != "" {
		go func() {
			log.Printf("Metrik Prometheus tersedia pada http://%s/metrics", addr)
			mux := http.NewServeMux()
			mux.Handle("/metrics", observability.Handler())
			if err := http.ListenAndServe(addr, mux); err != nil {
				log.Printf("[WARN] Server metrik berhenti: %v", err)
			}
		}()
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
	sessionService := userhandler.NewSessionService(db, hashKey, blockKey)
	env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	secureCookies := env == "production" || env == "prod"
	if !secureCookies {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("COOKIE_SECURE"))) {
		case "true", "1":
			secureCookies = true
		}
	}
	sessionService.SecureCookies = secureCookies

	app := fiber.New()
	app.Use(telemetry.Middleware(logger))
	app.Use(fiberRecover.New())
	api.SetupRoutes(app, db, connService, devAuth, sessionService, eng)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	log.Printf("Server berhasil dijalankan pada http://localhost:%s", port)
	log.Fatal(app.Listen(":" + port))
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
