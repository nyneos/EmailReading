package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"EmailService/internal/extract"
	"EmailService/internal/logger"
	"EmailService/internal/model"
	"EmailService/internal/parser"
	"EmailService/internal/s3store"
)

func RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", healthHandler)
	mux.HandleFunc("/v1/parse", parseHandler)
	mux.HandleFunc("/v1/parse/batch", parseBatchHandler)
	mux.HandleFunc("/v1/list-new", listNewHandler)
	mux.HandleFunc("/v1/extract", extractHandler)
	mux.HandleFunc("/v1/ses/rules/sync", sesSyncHandler)
	mux.HandleFunc("/v1/ses/rules/delete", sesDeleteRuleHandler)
	registerPollHandlers(mux)
	registerStorageHandlers(mux)
}

func requirePOST(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed — use POST", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "service": "email-service"})
	default:
		jsonErr(w, "method not allowed — use GET or POST", http.StatusMethodNotAllowed)
	}
}

func parseHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	var req model.ParseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.S3RawKey = strings.TrimSpace(req.S3RawKey)
	if req.S3RawKey == "" {
		jsonErr(w, "s3_raw_key is required", http.StatusBadRequest)
		return
	}

	logger.Info("parse: start key=%s", req.S3RawKey)
	parsed, err := parser.ParseFromS3(r.Context(), req.S3RawKey)
	if err != nil {
		logger.Error("parse: failed key=%s err=%v", req.S3RawKey, err)
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logger.Info("parse: ok key=%s subject=%q from=%q attachments=%d",
		req.S3RawKey, parsed.Envelope.Subject, parsed.Envelope.From, len(parsed.Attachments))

	writeJSON(w, parsed)
}

func parseBatchHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	var req model.ParseBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.S3RawKeys) == 0 {
		jsonErr(w, "s3_raw_keys is required", http.StatusBadRequest)
		return
	}

	resp := model.ParseBatchResponse{}
	logger.Info("parse/batch: start count=%d", len(req.S3RawKeys))
	for _, key := range req.S3RawKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		parsed, err := parser.ParseFromS3(r.Context(), key)
		if err != nil {
			logger.Warn("parse/batch: failed key=%s err=%v", key, err)
			resp.Errors = append(resp.Errors, key+": "+err.Error())
			continue
		}
		logger.Info("parse/batch: ok key=%s subject=%q", key, parsed.Envelope.Subject)
		resp.Results = append(resp.Results, parsed)
	}
	logger.Info("parse/batch: done ok=%d errors=%d", len(resp.Results), len(resp.Errors))

	writeJSON(w, resp)
}

func listNewHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	var req model.ListNewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10000
	}

	keys, err := s3store.ListNewRawKeys(r.Context(), strings.TrimSpace(req.After), limit)
	if err != nil {
		logger.Error("list-new: failed after=%q err=%v", req.After, err)
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logger.Info("list-new: after=%q found=%d prefix=%s", req.After, len(keys), s3store.RawPrefix())
	for _, k := range keys {
		logger.Info("list-new: key=%s", k)
	}

	writeJSON(w, map[string]interface{}{
		"prefix": s3store.RawPrefix(),
		"keys":   keys,
	})
}

func extractHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	var req model.ExtractRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.S3ParsedKey = strings.TrimSpace(req.S3ParsedKey)
	if req.S3ParsedKey == "" {
		jsonErr(w, "s3_parsed_key is required", http.StatusBadRequest)
		return
	}

	raw, err := s3store.GetObjectBytes(r.Context(), req.S3ParsedKey)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var parsed model.ParsedEmail
	if err := json.Unmarshal(raw, &parsed); err != nil {
		jsonErr(w, "invalid parsed json", http.StatusBadRequest)
		return
	}
	if err := extract.ValidateBody(parsed); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	intent, meta, confidence := extract.Run(req.Module, parsed)
	resp := model.ExtractResponse{
		Intent:            intent,
		ExtractedMetadata: meta,
		Confidence:        confidence,
	}

	writeJSON(w, resp)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(responseEnvelope{
		Success:    false,
		StatusCode: code,
		Message:    msg,
		Error: responseError{
			Code:    errorCodeForStatus(code),
			Details: msg,
		},
	})
}
