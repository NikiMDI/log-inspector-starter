package main

import (
	"context"
	"log"
	"os"
	"time"

	"example.com/log-inspector/internal/migrations"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrations.Run(ctx, databaseURL, os.Getenv("MIGRATION_TARGET")); err != nil {
		log.Fatal(err)
	}
	log.Print("migrations applied")
}
