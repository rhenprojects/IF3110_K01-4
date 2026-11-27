package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"web_backend/internal/api"
	databasehandler "web_backend/internal/database_handler"
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
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			sqlDB.SetMaxOpenConns(envInt("DB_MAX_OPEN_CONNS", 20))
			sqlDB.SetMaxIdleConns(envInt("DB_MAX_IDLE_CONNS", 10))
			sqlDB.SetConnMaxLifetime(time.Duration(envInt("DB_CONN_MAX_LIFETIME_MINUTES", 30)) * time.Minute)
		}
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
	env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	production := env == "production" || env == "prod"
	var connService *targetdb.Service
	var eng *engine.Engine
	if db != nil {
		connService = targetdb.NewService(db, connCipher, targetdb.NewPoolManager())
		eng, err = engine.New(db, connService, engine.Options{
			StatementTimeout:    time.Duration(envInt("MIGRATION_TIMEOUT_SECONDS", 300)) * time.Second,
			DenySuperuserTarget: production || os.Getenv("DENY_SUPERUSER_TARGET") == "true",
		})
		if err != nil {
			log.Fatalf("Gagal membuat migration engine: %v", err)
		}
		if workers := envInt("WORKER_COUNT", 2); workers > 0 {
			eng.Start(workers)
		}
		dbHandler := databasehandler.NewHandlerWithEngine(db, connService, eng)
		coordinator := dbHandler.NewBatchCoordinator()
		if recovered, err := coordinator.Recover(context.Background()); err != nil {
			log.Printf("[WARN] Gagal memulihkan batch migration: %v", err)
		} else if recovered > 0 {
			log.Printf("Memulihkan %d batch migration yang terhenti", recovered)
		}
		if sent, err := dbHandler.RecoverApprovals(context.Background()); err != nil {
			log.Printf("[WARN] Gagal mengirim ulang change request yang sudah disetujui: %v", err)
		} else if sent > 0 {
			log.Printf("Mengirim ulang %d change request yang sudah disetujui ke queue", sent)
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
		if production {
			log.Fatal("DEV_AUTH tidak boleh aktif di production")
		}
		log.Println("[WARN] DEV_AUTH aktif: identitas user diambil dari header X-Debug-User. Jangan dipakai di production")
	}
	if production {
		if os.Getenv("ADMIN_PASSWORD") == "admin-password" || os.Getenv("ADMIN_PASSWORD") == "" {
			log.Fatal("ADMIN_PASSWORD default/kosong tidak boleh dipakai di production")
		}
		if os.Getenv("DB_ENC_KEY") == "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" {
			log.Fatal("DB_ENC_KEY default compose tidak boleh dipakai di production")
		}
	}

	hashKey := []byte(os.Getenv("SESSION_HASH_KEY"))
	blockKey := []byte(os.Getenv("SESSION_BLOCK_KEY"))
	if len(hashKey) == 0 || len(blockKey) == 0 {
		log.Println("[WARN] SESSION_HASH_KEY atau SESSION_BLOCK_KEY belum diset. Cookie session memakai key acak dan tidak valid setelah server restart")
	}
	sessionService := userhandler.NewSessionService(db, hashKey, blockKey)
	if db != nil {
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				if err := sessionService.DeleteExpired(context.Background()); err != nil {
					log.Printf("[WARN] Gagal membersihkan session kedaluwarsa: %v", err)
				}
			}
		}()
	}
	secureCookies := production
	if !secureCookies {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("COOKIE_SECURE"))) {
		case "true", "1":
			secureCookies = true
		}
	}
	sessionService.SecureCookies = secureCookies

	app := fiber.New(fiber.Config{
		ProxyHeader:  "X-Real-IP",
		BodyLimit:    envInt("HTTP_BODY_LIMIT_BYTES", 1<<20),
		ReadTimeout:  time.Duration(envInt("HTTP_READ_TIMEOUT_SECONDS", 15)) * time.Second,
		WriteTimeout: time.Duration(envInt("HTTP_WRITE_TIMEOUT_SECONDS", 30)) * time.Second,
		IdleTimeout:  time.Duration(envInt("HTTP_IDLE_TIMEOUT_SECONDS", 60)) * time.Second,
	})
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
