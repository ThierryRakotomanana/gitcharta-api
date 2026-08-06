package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"githubaudience/internal/api"
	"githubaudience/internal/github"
	"githubaudience/internal/jobs"
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

	jobStore := jobs.NewJobStore()
	server := api.NewServer(pool, jobStore)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/audience/jobs", server.HandleCreateAudienceJob)
	mux.HandleFunc("GET /api/audience/jobs/{id}", server.HandleGetAudienceJob)

	allowedOrigins := strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",")
	handler := api.CORSMiddleware(allowedOrigins)(mux)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("server listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}