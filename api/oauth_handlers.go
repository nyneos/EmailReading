package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"EmailService/internal/gmailmail"
	"EmailService/internal/graphmail"
	"EmailService/internal/logger"
	"EmailService/internal/model"
	"EmailService/internal/oauthmail"
	"EmailService/internal/parser"
	"EmailService/internal/s3store"
)

func registerOAuthHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/v1/oauth/authorize-url", oauthAuthorizeURLHandler)
	mux.HandleFunc("/v1/oauth/exchange", oauthExchangeHandler)
	mux.HandleFunc("/v1/oauth/refresh", oauthRefreshHandler)
	mux.HandleFunc("/v1/oauth/test", oauthTestHandler)
	mux.HandleFunc("/v1/oauth/poll-page", oauthPollPageHandler)
}

func oauthAuthorizeURLHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.OAuthAuthorizeURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	authURL, err := oauthmail.AuthorizeURL(req.Provider, req.Transport, req.RedirectURI, req.State)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{"authorize_url": authURL, "provider": req.Provider})
}

func oauthExchangeHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.OAuthExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	tokens, err := oauthmail.Exchange(r.Context(), req.Provider, req.Transport, req.Code, req.RedirectURI)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	email, _ := oauthmail.Identity(r.Context(), req.Provider, tokens.AccessToken)
	writeJSON(w, model.OAuthExchangeResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresIn:    tokens.ExpiresIn,
		Scope:        tokens.Scope,
		Email:        email,
	})
}

func oauthRefreshHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.OAuthRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	tokens, err := oauthmail.Refresh(r.Context(), req.Provider, req.Transport, req.RefreshToken)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"expires_in":    tokens.ExpiresIn,
		"scope":         tokens.Scope,
	})
}

func oauthTestHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.OAuthTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := testOAuthConnection(r.Context(), req.Provider, req.AccessToken); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	email, _ := oauthmail.Identity(r.Context(), req.Provider, req.AccessToken)
	writeJSON(w, map[string]interface{}{"ok": true, "provider": req.Provider, "email": email})
}

func oauthPollPageHandler(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}
	var req model.OAuthPollPageRequest
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
		logger.Info("oauth/poll-page: init mailbox=%s sent=%v since=%s", req.MailboxAddress, req.SentFolder, now)
		writeJSON(w, model.OAuthPollPageResponse{Initialized: true, NewSince: now})
		return
	}
	since, err := time.Parse(time.RFC3339, sinceStr)
	if err != nil {
		jsonErr(w, "invalid since timestamp", http.StatusBadRequest)
		return
	}

	resp, err := pollOAuthPage(r.Context(), req, since, batch)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	logger.Info("oauth/poll-page: mailbox=%s provider=%s sent=%v fetched=%d messages=%d since=%s",
		req.MailboxAddress, req.Provider, req.SentFolder, resp.Fetched, len(resp.Messages), resp.NewSince)
	writeJSON(w, resp)
}

func testOAuthConnection(ctx context.Context, provider, accessToken string) error {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case oauthmail.ProviderMicrosoft:
		return graphmail.NewDelegatedClient(accessToken).TestConnection(ctx)
	case oauthmail.ProviderGoogle:
		return gmailmail.NewClient(accessToken).TestConnection(ctx)
	default:
		return fmt.Errorf("unsupported oauth provider %q", provider)
	}
}

func pollOAuthPage(ctx context.Context, req model.OAuthPollPageRequest, since time.Time, batch int) (model.OAuthPollPageResponse, error) {
	switch strings.ToLower(strings.TrimSpace(req.Provider)) {
	case oauthmail.ProviderMicrosoft:
		return pollMicrosoftOAuthPage(ctx, req, since, batch)
	case oauthmail.ProviderGoogle:
		return pollGoogleOAuthPage(ctx, req, since, batch)
	default:
		return model.OAuthPollPageResponse{}, fmt.Errorf("unsupported oauth provider %q", req.Provider)
	}
}

func pollMicrosoftOAuthPage(ctx context.Context, req model.OAuthPollPageRequest, since time.Time, batch int) (model.OAuthPollPageResponse, error) {
	client := graphmail.NewDelegatedClient(req.AccessToken)
	var listFn func(context.Context, time.Time, int) ([]graphmail.Message, error)
	if req.SentFolder {
		listFn = client.ListSentMessagesSince
	} else {
		listFn = client.ListInboxMessagesSince
	}
	msgs, err := listFn(ctx, since, batch)
	if err != nil {
		return model.OAuthPollPageResponse{}, err
	}
	return ingestOAuthGraphMessages(ctx, req, msgs)
}

func pollGoogleOAuthPage(ctx context.Context, req model.OAuthPollPageRequest, since time.Time, batch int) (model.OAuthPollPageResponse, error) {
	client := gmailmail.NewClient(req.AccessToken)
	ids, err := client.ListMessageIDsSince(ctx, req.SentFolder, since, batch)
	if err != nil {
		return model.OAuthPollPageResponse{}, err
	}
	resp := model.OAuthPollPageResponse{NewSince: since.UTC().Format(time.RFC3339), Fetched: len(ids)}
	maxCursor := since.UTC()
	for _, id := range ids {
		raw, err := client.GetRawMessage(ctx, id)
		if err != nil {
			logger.Warn("oauth/poll-page: gmail fetch %s: %v", id, err)
			continue
		}
		if !raw.InternalDate.IsZero() && raw.InternalDate.After(maxCursor) {
			maxCursor = raw.InternalDate
		}
		rawKey := oauthRawS3Key(req.MailboxAddress, req.SentFolder, req.Provider, id)
		if err := s3store.PutObject(ctx, rawKey, raw.Raw, "message/rfc822"); err != nil {
			logger.Warn("oauth/poll-page: s3 upload %s: %v", rawKey, err)
			continue
		}
		parsed, err := parser.ParseFromS3(ctx, rawKey)
		if err != nil {
			logger.Warn("oauth/poll-page: parse %s: %v", rawKey, err)
			continue
		}
		cursor := maxCursor
		if !raw.InternalDate.IsZero() {
			cursor = raw.InternalDate
		}
		resp.Messages = append(resp.Messages, model.OAuthPolledMessage{
			ProviderMessageID: id,
			CursorTime:        cursor.UTC().Format(time.RFC3339),
			Parsed:            parsed,
		})
	}
	if maxCursor.After(since) {
		resp.NewSince = maxCursor.Format(time.RFC3339)
	}
	return resp, nil
}

func ingestOAuthGraphMessages(ctx context.Context, req model.OAuthPollPageRequest, msgs []graphmail.Message) (model.OAuthPollPageResponse, error) {
	resp := model.OAuthPollPageResponse{NewSince: req.Since, Fetched: len(msgs)}
	maxCursor, _ := time.Parse(time.RFC3339, req.Since)
	client := graphmail.NewDelegatedClient(req.AccessToken)
	for _, gm := range msgs {
		ts := gm.CursorTime(req.SentFolder)
		if !ts.IsZero() && ts.After(maxCursor) {
			maxCursor = ts
		}
		if gm.ID == "" {
			continue
		}
		raw, err := client.GetMessageMIME(ctx, gm.ID)
		if err != nil {
			logger.Warn("oauth/poll-page: fetch raw %s: %v", gm.ID, err)
			continue
		}
		rawKey := oauthRawS3Key(req.MailboxAddress, req.SentFolder, req.Provider, gm.ID)
		if err := s3store.PutObject(ctx, rawKey, raw, "message/rfc822"); err != nil {
			logger.Warn("oauth/poll-page: s3 upload %s: %v", rawKey, err)
			continue
		}
		parsed, err := parser.ParseFromS3(ctx, rawKey)
		if err != nil {
			logger.Warn("oauth/poll-page: parse %s: %v", rawKey, err)
			continue
		}
		resp.Messages = append(resp.Messages, model.OAuthPolledMessage{
			ProviderMessageID: gm.ID,
			CursorTime:        ts.UTC().Format(time.RFC3339),
			Parsed:            parsed,
		})
	}
	if maxCursor.After(mustParseRFC3339(req.Since)) {
		resp.NewSince = maxCursor.Format(time.RFC3339)
	}
	return resp, nil
}

func oauthRawS3Key(mailbox string, sent bool, provider, messageID string) string {
	safeMailbox := strings.ReplaceAll(strings.ToLower(mailbox), "@", "_at_")
	prefix := strings.TrimSpace(os.Getenv("EMAIL_INBOUND_S3_PREFIX"))
	if prefix == "" {
		prefix = "email/inbound/raw/"
	}
	dir := "received"
	if sent {
		dir = "sent"
	}
	safeID := strings.ReplaceAll(messageID, "/", "_")
	return prefix + "oauth/" + strings.ToLower(provider) + "/" + safeMailbox + "/" + dir + "/" + safeID + ".eml"
}

func mustParseRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
