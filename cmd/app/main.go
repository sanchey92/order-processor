package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/sanchey92/order-processor/internal/config"
)

func main() {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatalf("failed to load .env file: %v", err)
	}
	path := os.Getenv("CONFIG_PATH")

	cfg := config.MustLoad(path)

	fmt.Println(cfg)
}
