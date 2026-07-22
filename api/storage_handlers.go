package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"EmailService/internal/logger"
	"EmailService/internal/model"
	"EmailService/internal/storage"
)

func registerStorageHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/v1/storage/put", storagePutHandler)
	mux.HandleFunc("/v1/storage/test-receive", storageTestReceiveHandler)
	mux.HandleFunc("/v1/storage/test-receive-2", storageTestReceive2Handler)
	mux.HandleFunc("/v1/storage/read-api-inbox", storageReadAPIInboxHandler)
}

func storagePutHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.StoragePutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ContentBase64) == "" {
		jsonErr(w, "content_base64 is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.DestinationType) == "" {
		req.DestinationType = storage.DestS3
	}

	logger.Info("storage/put: destination=%s prefix=%q", req.DestinationType, req.OutputNamePrefix)
	result, err := storage.Put(r.Context(), req)
	if err != nil {
		logger.Error("storage/put: failed dest=%s err=%v", req.DestinationType, err)
		msg := err.Error()
		code := http.StatusInternalServerError
		if strings.Contains(msg, "invalid ") || strings.Contains(msg, "required") ||
			strings.Contains(msg, "must start") || strings.Contains(msg, "empty") {
			code = http.StatusBadRequest
		}
		jsonErr(w, msg, code)
		return
	}
	logger.Info("storage/put: ok dest=%s file=%s location=%s",
		result.DestinationType, result.OutputFilename, result.OutputLocation)
	writeJSON(w, result)
}

// storageTestReceiveHandler is a dummy partner API for local testing.
// Point the transform rule API URL at:
//
//	http://localhost:8182/v1/storage/test-receive
func storageTestReceiveHandler(w http.ResponseWriter, r *http.Request) {
	storageTestReceiveToFolder(w, r, "api-inbox", "storage/test-receive")
}

// storageTestReceive2Handler is a second dummy partner API for testing.
//
//	http://localhost:8182/v1/storage/test-receive-2
func storageTestReceive2Handler(w http.ResponseWriter, r *http.Request) {
	storageTestReceiveToFolder(w, r, "api-inbox-2", "storage/test-receive-2")
}

func storageTestReceiveToFolder(w http.ResponseWriter, r *http.Request, subfolder, logLabel string) {
	if !requirePOST(w, r) {
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		jsonErr(w, "expected multipart form with field file: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, "multipart field 'file' is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	body, err := io.ReadAll(file)
	if err != nil {
		jsonErr(w, "failed to read upload: "+err.Error(), http.StatusInternalServerError)
		return
	}

	base := strings.TrimSpace(os.Getenv("EMAIL_TRANSFORMED_LOCAL_DIR"))
	if base == "" {
		base = "./transformed"
	}
	dir := filepath.Join(base, subfolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		jsonErr(w, "mkdir "+subfolder+": "+err.Error(), http.StatusInternalServerError)
		return
	}

	name := filepath.Base(strings.TrimSpace(hdr.Filename))
	if name == "" || name == "." {
		name = fmt.Sprintf("upload_%s.bin", time.Now().Format("20060102_150405"))
	}
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, body, 0o644); err != nil {
		jsonErr(w, "write file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	abs, _ := filepath.Abs(full)
	logger.Info("%s: saved %s (%d bytes)", logLabel, abs, len(body))
	writeJSON(w, map[string]interface{}{
		"ok":       true,
		"filename": name,
		"path":     abs,
		"bytes":    len(body),
		"folder":   subfolder,
	})
}

// storageReadAPIInboxHandler returns a file saved by test-receive / test-receive-2
// so the UI can preview API destination output.
func storageReadAPIInboxHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req struct {
		Filename string `json:"filename"`
		Folder   string `json:"folder"` // api-inbox | api-inbox-2
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	name := filepath.Base(strings.TrimSpace(req.Filename))
	if name == "" || name == "." {
		jsonErr(w, "filename is required", http.StatusBadRequest)
		return
	}
	subfolder := strings.TrimSpace(req.Folder)
	if subfolder == "" {
		subfolder = "api-inbox"
	}
	if subfolder != "api-inbox" && subfolder != "api-inbox-2" {
		jsonErr(w, "folder must be api-inbox or api-inbox-2", http.StatusBadRequest)
		return
	}

	base := strings.TrimSpace(os.Getenv("EMAIL_TRANSFORMED_LOCAL_DIR"))
	if base == "" {
		base = "./transformed"
	}
	full := filepath.Join(base, subfolder, name)
	abs, err := filepath.Abs(full)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rel, err := filepath.Rel(baseAbs, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		jsonErr(w, "invalid path", http.StatusBadRequest)
		return
	}

	raw, err := os.ReadFile(abs)
	if err != nil {
		jsonErr(w, "file not found: "+err.Error(), http.StatusNotFound)
		return
	}
	logger.Info("storage/read-api-inbox: %s (%d bytes)", abs, len(raw))
	writeJSON(w, map[string]interface{}{
		"filename":       name,
		"folder":         subfolder,
		"path":           abs,
		"content_base64": base64.StdEncoding.EncodeToString(raw),
		"byte_size":      len(raw),
	})
}
