package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	tokensRaw := os.Getenv("GITHUB_TOKENS")
	if tokensRaw == "" {
		log.Println("Warning: GITHUB_TOKENS env var empty")
	}
	tokens := strings.Split(tokensRaw, ",")

	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Printf("Server listening on port %s...", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}