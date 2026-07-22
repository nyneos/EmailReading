package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"EmailService/internal/gmaildwd"
	"EmailService/internal/logger"
	"EmailService/internal/model"
	"EmailService/internal/parser"
	"EmailService/internal/pollcursor"
	"EmailService/internal/s3store"
)

func registerGmailDWDHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/v1/gmail-dwd/test", gmailDWDTestHandler)
	mux.HandleFunc("/v1/gmail-dwd/poll-page", gmailDWDPollPageHandler)
}

func gmailDWDConfigFromModel(cfg model.GmailDWDConfig) gmaildwd.ServiceAccountConfig {
	return gmaildwd.ServiceAccountConfig{
		ServiceAccountEmail: strings.TrimSpace(cfg.ServiceAccountEmail),
		PrivateKey:          strings.TrimSpace(cfg.PrivateKey),
		ClientID:            strings.TrimSpace(cfg.ClientID),
	}
}

func gmailDWDTestHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.GmailDWDTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	mailbox := strings.TrimSpace(strings.ToLower(req.MailboxAddress))
	if mailbox == "" {
		jsonErr(w, "mailbox_address is required", http.StatusBadRequest)
		return
	}
	cfg := gmailDWDConfigFromModel(req.GmailDWD)
	token, err := gmaildwd.AccessToken(r.Context(), cfg, mailbox)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := gmaildwd.NewClient(mailbox, token).TestConnection(r.Context()); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{
		"ok":                    true,
		"service_account_email": cfg.ServiceAccountEmail,
		"mailbox_address":       mailbox,
		"message":               "Gmail API (domain-wide delegation) connection successful",
	})
}

func gmailDWDPollPageHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.GmailDWDPollPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	batch := req.Batch
	if batch <= 0 {
		batch = 25
	}
	mailbox := strings.TrimSpace(strings.ToLower(req.MailboxAddress))
	sinceStr := strings.TrimSpace(req.Since)
	if sinceStr == "" {
		now := pollcursor.FormatStored(time.Now().UTC())
		logger.Info("gmail-dwd/poll-page: init mailbox=%s sent=%v since=%s", mailbox, req.SentFolder, now)
		writeJSON(w, model.GmailDWDPollPageResponse{Initialized: true, NewSince: now})
		return
	}
	since, err := pollcursor.ParseStored(sinceStr)
	if err != nil {
		jsonErr(w, "invalid since timestamp", http.StatusBadRequest)
		return
	}
	cfg := gmailDWDConfigFromModel(req.GmailDWD)
	token, err := gmaildwd.AccessToken(r.Context(), cfg, mailbox)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	client := gmaildwd.NewClient(mailbox, token)
	ids, err := client.ListMessageIDsSince(r.Context(), req.SentFolder, since.UTC(), batch)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	skip := pollcursor.NewSkipSet(req.SkipMessageIDs)
	resp := model.GmailDWDPollPageResponse{NewSince: sinceStr, Fetched: len(ids)}
	maxCursor := since.UTC()
	skippedKnown := 0
	for _, id := range ids {
		if skip.Has(id) {
			skippedKnown++
			continue
		}
		rawMsg, err := client.GetRawMessage(r.Context(), id)
		if err != nil {
			logger.Warn("gmail-dwd/poll-page: fetch raw %s: %v", id, err)
			continue
		}
		if !rawMsg.InternalDate.IsZero() && rawMsg.InternalDate.After(maxCursor) {
			maxCursor = rawMsg.InternalDate
		}
		rawKey := gmailDWDRawS3Key(mailbox, req.SentFolder, id)
		if err := s3store.PutObject(r.Context(), rawKey, rawMsg.Raw, "message/rfc822"); err != nil {
			logger.Warn("gmail-dwd/poll-page: s3 upload %s: %v", rawKey, err)
			continue
		}
		parsed, err := parser.ParseFromS3(r.Context(), rawKey)
		if err != nil {
			logger.Warn("gmail-dwd/poll-page: parse %s: %v", rawKey, err)
			continue
		}
		cursor := rawMsg.InternalDate.UTC()
		if cursor.IsZero() {
			cursor = maxCursor
		}
		resp.Messages = append(resp.Messages, model.GmailDWDPolledMessage{
			GraphMessageID: id,
			CursorTime:     pollcursor.FormatStored(cursor),
			Parsed:         parser.ForPollTransport(parsed),
		})
	}
	resp.NewSince = pollcursor.FormatStored(pollcursor.ResolveNewSince(since, maxCursor, len(ids), skippedKnown))
	logger.Info("gmail-dwd/poll-page: mailbox=%s sent=%v fetched=%d messages=%d skipped_known=%d new_since=%s",
		mailbox, req.SentFolder, resp.Fetched, len(resp.Messages), skippedKnown, resp.NewSince)
	writeJSON(w, resp)
}

func gmailDWDRawS3Key(mailbox string, sent bool, messageID string) string {
	dir := "received"
	if sent {
		dir = "sent"
	}
	safeMailbox := strings.NewReplacer("@", "_at_", ".", "_").Replace(strings.ToLower(mailbox))
	return fmt.Sprintf("email/inbound/raw/gmail-dwd/%s/%s/%s.eml", safeMailbox, dir, messageID)
}
