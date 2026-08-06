package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"githubaudience/internal/api"
	"githubaudience/internal/github"
	"githubaudience/internal/jobs"

	"golang.org/x/time/rate"
)

func main() {
	rawTokens := os.Getenv("GITHUB_TOKENS")
	if rawTokens == "" {
		log.Fatal("GITHUB_TOKENS environment variable is required")
	}
	pool, err := github.NewTokenPool(strings.Split(rawTokens, ","))
	if err != nil {
		log.Fatalf("failed to build token pool: %v", err)
	}

	jobStore := jobs.NewJobStore(10)
	server := api.NewServer(pool, jobStore)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/audience/jobs", server.HandleCreateAudienceJob)
	mux.HandleFunc("GET /api/audience/jobs/{id}", server.HandleGetAudienceJob)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	allowedOrigins := strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",")
	handler := api.CORSMiddleware(allowedOrigins)(
		api.RateLimitMiddleware(rate.Limit(0.2), 3)(mux),
	)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}