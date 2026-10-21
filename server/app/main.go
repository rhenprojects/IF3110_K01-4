package main

import (
	"log"

	fiber "github.com/gofiber/fiber/v2"
)

func main() {
	app := fiber.New()

	api := app.Group("/api/v1")

	log.Println("Server starting on http://localhost:8080")
	log.Fatal(app.Listen(":8080"))
}
