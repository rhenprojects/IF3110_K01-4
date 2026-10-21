package app

import (
	fiber "github.com/gofiber/fiber/v2"
)

func main() {
	app := fiber.New()
	app.Listen(":3000")
}
