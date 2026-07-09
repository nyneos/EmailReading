package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
)

const serviceKeyField = "service_key"

// Middleware validates EMAIL_SERVICE_KEY via Bearer header or JSON service_key field.
// No cookies. All routes except none — every route requires the key.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Render/platform probes use GET /v1/health without a service key.
		if r.URL.Path == "/v1/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			next.ServeHTTP(w, r)
			return
		}

		expected := strings.TrimSpace(os.Getenv("EMAIL_SERVICE_KEY"))
		if expected == "" {
			deny(w)
			return
		}

		if token := bearerToken(r); token == expected {
			next.ServeHTTP(w, r)
			return
		}

		if key := bodyServiceKey(r); key == expected {
			next.ServeHTTP(w, r)
			return
		}

		deny(w)
	})
}

func bearerToken(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

// bodyServiceKey reads service_key from JSON body without consuming the body.
func bodyServiceKey(r *http.Request) string {
	if r.Body == nil || r.Method != http.MethodPost {
		return ""
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))

	var partial map[string]interface{}
	if err := json.Unmarshal(raw, &partial); err != nil {
		return ""
	}
	if v, ok := partial[serviceKeyField].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func deny(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
