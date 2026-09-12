package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"example.com/log-inspector/internal/service"
	"example.com/log-inspector/internal/store"
)

type config struct {
	HTTPAddr        string
	DiagnosticAddr  string
	DatabaseURL     string
	UploadDir       string
	Workers         int
	MaxUploadBytes  int64
	MaxLineBytes    int
	DBMaxOpen       int
	DBMaxIdle       int
	DBTimeout       time.Duration
	PollInterval    time.Duration
	ProcessingDelay time.Duration
	LogLevel        string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	jobStore, err := store.Open(cfg.DatabaseURL, cfg.DBMaxOpen, cfg.DBMaxIdle, cfg.DBTimeout)
	if err != nil {
		log.Fatal(err)
	}
	defer jobStore.Close()
	if err := jobStore.FailInterrupted(context.Background()); err != nil {
		log.Fatalf("mark interrupted jobs: %v", err)
	}
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		log.Fatalf("LOG_LEVEL must be debug, info, warn or error: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	app, err := service.New(jobStore, cfg.UploadDir, cfg.MaxUploadBytes, cfg.MaxLineBytes, cfg.PollInterval, cfg.ProcessingDelay, logger)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workers := app.RunWorkers(ctx, cfg.Workers)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 2)
	go func() { errCh <- server.ListenAndServe() }()
	if cfg.DiagnosticAddr != "" {
		diagnostics := http.NewServeMux()
		diagnostics.Handle("/debug/pprof/", http.DefaultServeMux)
		diagnostics.HandleFunc("/debug/db-stats", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jobStore.Stats())
		})
		diagnosticServer := &http.Server{Addr: cfg.DiagnosticAddr, Handler: diagnostics, ReadHeaderTimeout: 5 * time.Second}
		go func() { errCh <- diagnosticServer.ListenAndServe() }()
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = diagnosticServer.Shutdown(shutdownCtx)
		}()
	}
	logger.Info("server listening", "address", cfg.HTTPAddr, "workers", cfg.Workers)
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
		}
		stop()
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	workers.Wait()
}

func loadConfig() (config, error) {
	result := config{
		HTTPAddr: env("HTTP_ADDR", ":8080"), DiagnosticAddr: env("DIAGNOSTIC_ADDR", ":6060"),
		DatabaseURL: os.Getenv("DATABASE_URL"), UploadDir: env("UPLOAD_DIR", "./data/uploads"),
		LogLevel: env("LOG_LEVEL", "info"),
	}
	if result.DatabaseURL == "" {
		return config{}, fmt.Errorf("DATABASE_URL is required")
	}
	var err error
	if result.Workers, err = envInt("WORKERS", 2); err != nil || result.Workers < 1 {
		return config{}, fmt.Errorf("WORKERS must be a positive integer")
	}
	if result.MaxUploadBytes, err = envInt64("MAX_UPLOAD_BYTES", 20*1024*1024); err != nil || result.MaxUploadBytes < 1 {
		return config{}, fmt.Errorf("MAX_UPLOAD_BYTES must be a positive integer")
	}
	if result.MaxLineBytes, err = envInt("MAX_LINE_BYTES", 1024*1024); err != nil || result.MaxLineBytes < 1 {
		return config{}, fmt.Errorf("MAX_LINE_BYTES must be a positive integer")
	}
	if result.DBMaxOpen, err = envInt("DB_MAX_OPEN", 4); err != nil || result.DBMaxOpen < 1 {
		return config{}, fmt.Errorf("DB_MAX_OPEN must be a positive integer")
	}
	if result.DBMaxIdle, err = envInt("DB_MAX_IDLE", 2); err != nil || result.DBMaxIdle < 0 || result.DBMaxIdle > result.DBMaxOpen {
		return config{}, fmt.Errorf("DB_MAX_IDLE must be between 0 and DB_MAX_OPEN")
	}
	if result.DBTimeout, err = envDuration("DB_TIMEOUT", 3*time.Second); err != nil || result.DBTimeout <= 0 {
		return config{}, fmt.Errorf("DB_TIMEOUT must be a positive duration")
	}
	if result.PollInterval, err = envDuration("POLL_INTERVAL", 500*time.Millisecond); err != nil || result.PollInterval <= 0 {
		return config{}, fmt.Errorf("POLL_INTERVAL must be a positive duration")
	}
	if result.ProcessingDelay, err = envDuration("PROCESSING_DELAY", 0); err != nil || result.ProcessingDelay < 0 {
		return config{}, fmt.Errorf("PROCESSING_DELAY must be a nonnegative duration")
	}
	return result, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	return strconv.Atoi(env(name, strconv.Itoa(fallback)))
}

func envInt64(name string, fallback int64) (int64, error) {
	return strconv.ParseInt(env(name, strconv.FormatInt(fallback, 10)), 10, 64)
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	return time.ParseDuration(env(name, fallback.String()))
}
