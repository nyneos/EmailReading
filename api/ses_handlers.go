package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"EmailService/internal/logger"
	"EmailService/internal/ses"
)

func sesSyncHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	var req ses.SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	logger.Info("ses/sync: start rule_set=%s rules=%d bucket=%s prefix=%s",
		req.RuleSetName, len(req.Rules), req.S3Bucket, req.S3Prefix)
	for _, rule := range req.Rules {
		logger.Info("ses/sync: rule name=%s recipient=%s", rule.RuleName, rule.Recipient)
	}
	result, err := ses.SyncReceiptRules(r.Context(), req)
	if err != nil {
		logger.Error("ses/sync: failed err=%v", err)
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logger.Info("ses/sync: done synced=%d removed=%d errors=%d", result.Synced, result.Removed, len(result.Errors))
	for _, e := range result.Errors {
		logger.Warn("ses/sync: %s", e)
	}

	writeJSON(w, result)
}

func sesDeleteRuleHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	var req struct {
		RuleSetName string `json:"rule_set_name"`
		RuleName    string `json:"rule_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.RuleName = strings.TrimSpace(req.RuleName)
	if req.RuleName == "" {
		jsonErr(w, "rule_name is required", http.StatusBadRequest)
		return
	}

	if err := ses.DeleteReceiptRule(r.Context(), req.RuleSetName, req.RuleName); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "deleted"})
}
