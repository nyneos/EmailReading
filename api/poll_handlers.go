package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"EmailService/internal/graphmail"
	"EmailService/internal/imapmail"
	"EmailService/internal/logger"
	"EmailService/internal/model"
	"EmailService/internal/parser"
	"EmailService/internal/s3store"
)

func registerPollHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/v1/imap/test", imapTestHandler)
	mux.HandleFunc("/v1/imap/poll-folder", imapPollFolderHandler)
	mux.HandleFunc("/v1/graph/test", graphTestHandler)
	mux.HandleFunc("/v1/graph/poll-page", graphPollPageHandler)
	registerOAuthHandlers(mux)
}

func imapTestHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.IMAPTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	cfg, err := imapConfigFromModel(req.IMAP, req.MailboxAddress)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := imapmail.NewClient().TestConnection(r.Context(), cfg, req.MailboxAddress); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "host": cfg.Host, "provider": cfg.Provider})
}

func imapPollFolderHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.IMAPPollFolderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	batch := req.Batch
	if batch <= 0 {
		batch = 25
	}
	cfg, err := imapConfigFromModel(req.IMAP, req.MailboxAddress)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	folder := strings.TrimSpace(req.Folder)
	if folder == "" {
		folder = cfg.InboxFolder
	}
	client := imapmail.NewClient()
	lastUID := req.LastUID

	if lastUID == 0 {
		maxUID, err := client.MaxUID(r.Context(), cfg, folder)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		initUID := maxUID
		if maxUID > 0 {
			// Leave cursor one behind max so the next poll ingests the newest message
			// instead of skipping it when initialization races with a just-sent mail.
			initUID = maxUID - 1
		}
		logger.Info("imap/poll-folder: init mailbox=%s folder=%s uid=%d (max=%d)", req.MailboxAddress, folder, initUID, maxUID)
		writeJSON(w, model.IMAPPollFolderResponse{Initialized: true, NewLastUID: initUID})
		return
	}

	messages, err := client.FetchSinceUID(r.Context(), cfg, folder, lastUID, batch)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := model.IMAPPollFolderResponse{NewLastUID: lastUID}
	for _, im := range messages {
		if im.UID > resp.NewLastUID {
			resp.NewLastUID = im.UID
		}
		imapKey := fmt.Sprintf("%s:%s:%d", req.InboxID, folder, im.UID)
		rawKey := imapRawS3Key(req.MailboxAddress, req.Direction, imapKey)
		if err := s3store.PutObject(r.Context(), rawKey, im.Raw, "message/rfc822"); err != nil {
			logger.Warn("imap/poll-folder: s3 upload %s: %v", rawKey, err)
			continue
		}
		parsed, err := parser.ParseFromS3(r.Context(), rawKey)
		if err != nil {
			logger.Warn("imap/poll-folder: parse %s: %v", rawKey, err)
			continue
		}
		resp.Messages = append(resp.Messages, model.IMAPPolledMessage{
			UID:            im.UID,
			IMAPMessageKey: imapKey,
			Parsed:         parsed,
		})
	}
	logger.Info("imap/poll-folder: mailbox=%s folder=%s messages=%d new_uid=%d", req.MailboxAddress, folder, len(resp.Messages), resp.NewLastUID)
	writeJSON(w, resp)
}

func graphTestHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.GraphTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	client, err := graphConfigFromModel(req.Graph)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := graphmail.NewClientWithConfig(client).TestConnection(r.Context()); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "tenant_id": req.Graph.TenantID})
}

func graphPollPageHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.GraphPollPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	batch := req.Batch
	if batch <= 0 {
		batch = 25
	}
	sinceStr := strings.TrimSpace(req.Since)
	if sinceStr == "" {
		now := time.Now().UTC().Format(time.RFC3339)
		logger.Info("graph/poll-page: init mailbox=%s sent=%v since=%s", req.MailboxAddress, req.SentFolder, now)
		writeJSON(w, model.GraphPollPageResponse{Initialized: true, NewSince: now})
		return
	}
	since, err := time.Parse(time.RFC3339, sinceStr)
	if err != nil {
		jsonErr(w, "invalid since timestamp", http.StatusBadRequest)
		return
	}

	client, err := graphConfigFromModel(req.Graph)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	graphClient := graphmail.NewClientWithConfig(client)
	var listFn func(context.Context, string, time.Time, int) ([]graphmail.Message, error)
	if req.SentFolder {
		listFn = graphClient.ListSentMessagesSince
	} else {
		listFn = graphClient.ListInboxMessagesSince
	}
	graphMessages, err := listFn(r.Context(), req.MailboxAddress, since.UTC(), batch)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := model.GraphPollPageResponse{NewSince: sinceStr, Fetched: len(graphMessages)}
	maxCursor := since.UTC()
	for _, gm := range graphMessages {
		ts := gm.CursorTime(req.SentFolder)
		if !ts.IsZero() && ts.After(maxCursor) {
			maxCursor = ts
		}
		if gm.ID == "" {
			continue
		}
		raw, err := graphClient.GetMessageMIME(r.Context(), req.MailboxAddress, gm.ID)
		if err != nil {
			logger.Warn("graph/poll-page: fetch raw %s: %v", gm.ID, err)
			continue
		}
		rawKey := graphRawS3Key(req.MailboxAddress, req.SentFolder, gm.ID)
		if err := s3store.PutObject(r.Context(), rawKey, raw, "message/rfc822"); err != nil {
			logger.Warn("graph/poll-page: s3 upload %s: %v", rawKey, err)
			continue
		}
		parsed, err := parser.ParseFromS3(r.Context(), rawKey)
		if err != nil {
			logger.Warn("graph/poll-page: parse %s: %v", rawKey, err)
			continue
		}
		resp.Messages = append(resp.Messages, model.GraphPolledMessage{
			GraphMessageID: gm.ID,
			CursorTime:     ts.UTC().Format(time.RFC3339),
			Parsed:         parsed,
		})
	}
	if maxCursor.After(since) {
		resp.NewSince = maxCursor.Format(time.RFC3339)
	}
	logger.Info("graph/poll-page: mailbox=%s sent=%v fetched=%d messages=%d since=%s", req.MailboxAddress, req.SentFolder, resp.Fetched, len(resp.Messages), resp.NewSince)
	writeJSON(w, resp)
}

func imapConfigFromModel(c model.IMAPConfig, mailbox string) (imapmail.Config, error) {
	port := c.Port
	if port <= 0 {
		port = 993
	}
	cfg := imapmail.Config{
		Provider:    c.Provider,
		Host:        c.Host,
		Port:        port,
		Username:    c.Username,
		Password:    c.Password,
		AuthMode:    c.AuthMode,
		AccessToken: c.AccessToken,
		UseTLS:      c.UseTLS,
		InboxFolder: c.InboxFolder,
		SentFolder:  c.SentFolder,
	}
	if err := cfg.Resolve(mailbox); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func graphConfigFromModel(c model.GraphConfig) (graphmail.Config, error) {
	cfg := graphmail.Config{
		Label:        c.TenantLabel,
		TenantID:     c.TenantID,
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func imapRawS3Key(mailbox, direction, imapKey string) string {
	safeMailbox := strings.ReplaceAll(strings.ToLower(mailbox), "@", "_at_")
	safeKey := strings.ReplaceAll(imapKey, ":", "_")
	prefix := strings.TrimSpace(os.Getenv("EMAIL_INBOUND_S3_PREFIX"))
	if prefix == "" {
		prefix = "email/inbound/raw/"
	}
	dir := "received"
	if strings.EqualFold(direction, "SENT") {
		dir = "sent"
	}
	return prefix + "imap/" + safeMailbox + "/" + dir + "/" + safeKey + ".eml"
}

func graphRawS3Key(mailbox string, sent bool, graphID string) string {
	safeMailbox := strings.ReplaceAll(strings.ToLower(mailbox), "@", "_at_")
	prefix := strings.TrimSpace(os.Getenv("EMAIL_INBOUND_S3_PREFIX"))
	if prefix == "" {
		prefix = "email/inbound/raw/"
	}
	dir := "received"
	if sent {
		dir = "sent"
	}
	safeID := strings.ReplaceAll(graphID, "/", "_")
	return prefix + "graph/" + safeMailbox + "/" + dir + "/" + safeID + ".eml"
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(responseEnvelope{
		Success:    true,
		StatusCode: http.StatusOK,
		Message:    "OK",
		Data:       v,
	})
}

type responseEnvelope struct {
	Success    bool        `json:"success"`
	StatusCode int         `json:"statusCode"`
	Message    string      `json:"message"`
	Data       interface{} `json:"data,omitempty"`
	Error      interface{} `json:"error,omitempty"`
}

type responseError struct {
	Code    string `json:"code"`
	Details string `json:"details"`
}

func errorCodeForStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "BAD_REQUEST"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusServiceUnavailable:
		return "SERVICE_UNAVAILABLE"
	default:
		return "INTERNAL_ERROR"
	}
}
