package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	githubaudience "githubaudience"
)

func main() {
	tokensRaw := os.Getenv("GITHUB_TOKENS")
	if tokensRaw == "" {
		log.Fatal("GITHUB_TOKENS environment variable is required (comma-separated list of GitHub tokens)")
	}
	pool, err := githubaudience.NewTokenPool(strings.Split(tokensRaw, ","))
	if err != nil {
		log.Fatalf("failed to build token pool: %v", err)
	}
	log.Printf("token pool ready with %d token(s)", pool.Size())

	server := githubaudience.NewServer(pool)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/audience", server.HandleAudience)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
