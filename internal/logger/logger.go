package logger

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	logMu     sync.RWMutex
	logActive = true
)

func init() {
	refreshEnabled()
}

func refreshEnabled() {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("EMAIL_SERVICE_LOG")))
	if v == "" {
		logActive = true
		return
	}
	logActive = v == "1" || v == "true" || v == "yes"
}

// Enabled reports whether pipeline logging is on (EMAIL_SERVICE_LOG, default true).
func Enabled() bool {
	logMu.RLock()
	defer logMu.RUnlock()
	return logActive
}

func Info(format string, args ...interface{}) {
	if !Enabled() {
		return
	}
	emit("INFO", format, args...)
}

func Warn(format string, args ...interface{}) {
	if !Enabled() {
		return
	}
	emit("WARN", format, args...)
}

func Error(format string, args ...interface{}) {
	emit("ERROR", format, args...)
}

func Startup(format string, args ...interface{}) {
	log.Printf("[email-service][%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func emit(level, format string, args ...interface{}) {
	log.Printf("[email-service][%s][%s] %s", level, time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// HTTPMiddleware logs each request path, status, and duration.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		Info("%s %s status=%d duration_ms=%d", r.Method, r.URL.Path, sw.code, time.Since(start).Milliseconds())
	})
}
