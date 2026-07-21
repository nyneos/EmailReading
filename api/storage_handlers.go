package api

import (
	"encoding/json"
	"net/http"
	"strings"

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
