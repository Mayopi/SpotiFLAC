package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	Port      string
	OutputDir string
	APIKey    string
}

func DefaultConfig() Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	outputDir := os.Getenv("OUTPUT_DIR")
	if outputDir == "" {
		outputDir = "/downloads"
	}
	apiKey := os.Getenv("API_KEY")

	return Config{
		Port:      port,
		OutputDir: outputDir,
		APIKey:    apiKey,
	}
}

type Server struct {
	config  Config
	handler *Handler
	jobs    *JobManager
	mux     *http.ServeMux
}

func NewServer(cfg Config) *Server {
	jobs := NewJobManager()
	handler := NewHandler(jobs, cfg.OutputDir)

	s := &Server{
		config:  cfg,
		handler: handler,
		jobs:    jobs,
		mux:     http.NewServeMux(),
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// Health
	s.mux.HandleFunc("/health", s.handler.Health)
	s.mux.HandleFunc("/api/v1/health", s.handler.Health)

	// Metadata
	s.mux.HandleFunc("/api/v1/metadata", s.methodRouter(map[string]http.HandlerFunc{
		http.MethodPost: s.handler.GetMetadata,
	}))

	// Downloads
	s.mux.HandleFunc("/api/v1/download", s.methodRouter(map[string]http.HandlerFunc{
		http.MethodPost: s.handler.StartDownload,
	}))

	// Jobs
	s.mux.HandleFunc("/api/v1/jobs", s.methodRouter(map[string]http.HandlerFunc{
		http.MethodGet: s.handler.ListJobs,
	}))

	// Job detail / cancel / delete — use path prefix matching
	s.mux.HandleFunc("/api/v1/jobs/", s.jobRouter)

	// Availability
	s.mux.HandleFunc("/api/v1/availability", s.methodRouter(map[string]http.HandlerFunc{
		http.MethodGet: s.handler.CheckAvailability,
	}))

	// Cleanup
	s.mux.HandleFunc("/api/v1/cleanup", s.methodRouter(map[string]http.HandlerFunc{
		http.MethodPost: s.handler.CleanupJobs,
	}))
}

func (s *Server) jobRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	if strings.HasSuffix(path, "/cancel") && r.Method == http.MethodPost {
		s.handler.CancelJob(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handler.GetJob(w, r)
	case http.MethodDelete:
		s.handler.DeleteJob(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) methodRouter(methods map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		handler, ok := methods[r.Method]
		if !ok {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		handler(w, r)
	}
}

func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = APIKeyMiddleware(s.config.APIKey)(h)
	h = CORSMiddleware(h)
	h = LoggingMiddleware(h)
	return h
}

func (s *Server) Run() error {
	// Ensure output directory exists
	if err := os.MkdirAll(s.config.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	srv := &http.Server{
		Addr:         ":" + s.config.Port,
		Handler:      s.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Background cleanup: remove completed jobs older than 24h every hour
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if removed := s.jobs.Cleanup(24 * time.Hour); removed > 0 {
				log.Printf("Cleaned up %d old jobs", removed)
			}
		}
	}()

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("SpotiFLAC API server starting on :%s", s.config.Port)
		log.Printf("Output directory: %s", s.config.OutputDir)
		if s.config.APIKey != "" {
			log.Println("API key authentication enabled")
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
