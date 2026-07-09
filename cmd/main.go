package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"EmailService/api"
	"EmailService/internal/auth"
	"EmailService/internal/logger"

	"github.com/joho/godotenv"
)

func loadEnv() {
	candidates := []string{".env", filepath.Join("..", ".env")}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Overload(p)
			logger.Startup("loaded env from %s", p)
			return
		}
	}
	logger.Startup("no .env file found, using OS environment")
}

func main() {
	loadEnv()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8182"
	}

	if os.Getenv("EMAIL_SERVICE_KEY") == "" {
		logger.Startup("WARNING: EMAIL_SERVICE_KEY is not set — all requests will be rejected")
	}

	mux := http.NewServeMux()
	api.RegisterHandlers(mux)

	handler := logger.HTTPMiddleware(auth.Middleware(mux))
	logger.Startup("starting on :%s (pipeline_log=%v)", port, logger.Enabled())
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}
