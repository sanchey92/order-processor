package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/sanchey92/order-processor/internal/app"
	"github.com/sanchey92/order-processor/internal/config"
)

func main() {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatalf("failed to load .env file: %v", err)
	}
	path := os.Getenv("CONFIG_PATH")

	cfg := config.MustLoad(path)

	a, err := app.New(cfg)
	if err != nil {
		log.Fatalf("failed to start: %v", err)
	}

	if err = a.Run(); err != nil {
		log.Fatalf("failed to run application")
	}
}
