package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"emby115/internal/app"
	"emby115/internal/config"
)

func main() {
	configPath := flag.String("config", "configs/bootstrap.example.yaml", "path to bootstrap config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("create app: %v", err)
	}
	defer func() {
		if closeErr := application.Close(); closeErr != nil {
			log.Printf("close app: %v", closeErr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := application.Run(ctx); err != nil {
		log.Fatalf("run app: %v", err)
	}
}
