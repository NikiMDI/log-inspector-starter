package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"example.com/log-inspector/internal/analyzer"
	"example.com/log-inspector/internal/store"
)

type Service struct {
	store           *store.Store
	uploadDir       string
	maxUploadBytes  int64
	maxLineBytes    int
	pollInterval    time.Duration
	processingDelay time.Duration
	logger          *slog.Logger
}

func New(jobStore *store.Store, uploadDir string, maxUploadBytes int64, maxLineBytes int, pollInterval, processingDelay time.Duration, logger *slog.Logger) (*Service, error) {
	if maxUploadBytes <= 0 || maxLineBytes <= 0 {
		return nil, fmt.Errorf("upload and line limits must be positive")
	}
	if err := os.MkdirAll(uploadDir, 0o750); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	return &Service{store: jobStore, uploadDir: uploadDir, maxUploadBytes: maxUploadBytes, maxLineBytes: maxLineBytes, pollInterval: pollInterval, processingDelay: processingDelay, logger: logger}, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /jobs", s.createJob)
	mux.HandleFunc("GET /jobs/{id}", s.getJob)
	mux.HandleFunc("GET /jobs/{id}/report", s.getReport)
	return mux
}

func (s *Service) RunWorkers(ctx context.Context, count int) *sync.WaitGroup {
	var workers sync.WaitGroup
	for workerNumber := 1; workerNumber <= count; workerNumber++ {
		workers.Add(1)
		go func(number int) {
			defer workers.Done()
			s.worker(ctx, number)
		}(workerNumber)
	}
	return &workers
}

func (s *Service) createJob(w http.ResponseWriter, r *http.Request) {
	if contentType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); contentType != "application/x-ndjson" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/x-ndjson")
		return
	}
	if r.ContentLength > s.maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "upload is too large")
		return
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot create job id")
		return
	}
	filePath := filepath.Join(s.uploadDir, id+".jsonl")
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot store upload")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadBytes)
	_, copyErr := io.Copy(file, r.Body)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(filePath)
		var tooLarge *http.MaxBytesError
		if errors.As(copyErr, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload is too large")
		} else {
			writeError(w, http.StatusBadRequest, "cannot read upload")
		}
		return
	}
	originalFilename := filepath.Base(r.Header.Get("X-Filename"))
	if originalFilename == "." {
		originalFilename = ""
	}
	if err := s.store.CreateJob(r.Context(), id, filePath, originalFilename); err != nil {
		_ = os.Remove(filePath)
		s.logger.Error("cannot register job", "job_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "cannot register job")
		return
	}
	s.logger.Info("job queued", "job_id", id)
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *Service) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.GetJob(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read job")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Service) getReport(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.GetJob(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read job")
		return
	}
	if job.Status != "done" {
		writeError(w, http.StatusConflict, "report is not ready")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(job.Report)
}

func (s *Service) worker(ctx context.Context, number int) {
	s.logger.Debug("worker started", "worker", number)
	for {
		job, err := s.store.ClaimJob(ctx)
		if errors.Is(err, store.ErrNotFound) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.pollInterval):
				continue
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Error("cannot claim job", "worker", number, "error", err)
			time.Sleep(s.pollInterval)
			continue
		}
		s.process(ctx, number, job)
	}
}

func (s *Service) process(ctx context.Context, workerNumber int, job store.Job) {
	s.logger.Info("job running", "job_id", job.ID, "worker", workerNumber)
	if s.processingDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.processingDelay):
		}
	}
	file, err := os.Open(job.FilePath)
	if err == nil {
		var report analyzer.Report
		report, err = analyzer.Analyze(file, s.maxLineBytes)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			var reportJSON []byte
			reportJSON, err = json.Marshal(report)
			if err == nil {
				err = s.store.MarkDone(context.Background(), job.ID, reportJSON)
			}
		}
	}
	if err != nil {
		s.logger.Error("job failed", "job_id", job.ID, "error", err)
		if storeErr := s.store.MarkFailed(context.Background(), job.ID, err.Error()); storeErr != nil {
			s.logger.Error("cannot save job failure", "job_id", job.ID, "error", storeErr)
		}
		return
	}
	s.logger.Info("job done", "job_id", job.ID)
}

func newID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
