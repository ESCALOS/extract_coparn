package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"extract_coparn/internal/alerts"
	"extract_coparn/internal/app"
	"extract_coparn/internal/client"
	"extract_coparn/internal/config"
	"extract_coparn/internal/notifier"
	"extract_coparn/internal/repo"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	repository, err := repo.New(ctx, cfg.DB.DSN)
	if err != nil {
		log.Fatalf("db error: %v", err)
	}
	defer repository.Close()

	apiClient := client.NewAPIClient(cfg.API)
	sftpClient := client.NewSFTPClient(cfg.SFTP)
	mail := notifier.New(cfg.Email)
	alertMonitor := alerts.NewMonitor(mail)

	orch := app.NewOrchestrator(cfg, apiClient, sftpClient, repository, alertMonitor)
	if err := orch.Run(ctx); err != nil {
		log.Fatalf("run error: %v", err)
	}
}
