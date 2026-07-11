// Package gmailmail fetches raw messages via the Gmail REST API using a
// delegated OAuth access token (scope gmail.readonly).
package gmailmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"

type Client struct {
	token string
	http  *http.Client
}

func NewClient(accessToken string) *Client {
	return &Client{
		token: accessToken,
		http:  &http.Client{Timeout: 45 * time.Second},
	}
}

// RawMessage is one fetched Gmail message.
type RawMessage struct {
	ID           string
	InternalDate time.Time
	Raw          []byte
}

func (c *Client) doGET(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gmailBase+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

// TestConnection verifies the token can read the Gmail profile.
func (c *Client) TestConnection(ctx context.Context) error {
	body, status, err := c.doGET(ctx, "/profile")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("gmail profile status=%d body=%s", status, truncate(string(body), 200))
	}
	return nil
}

// ListMessageIDsSince returns message IDs in INBOX or SENT newer than since.
// Gmail's `after:` filter has 1-second granularity, so callers must dedupe.
func (c *Client) ListMessageIDsSince(ctx context.Context, sent bool, since time.Time, max int) ([]string, error) {
	if max <= 0 {
		max = 25
	}
	if max > 100 {
		max = 100
	}
	label := "INBOX"
	if sent {
		label = "SENT"
	}
	q := url.Values{}
	q.Set("labelIds", label)
	q.Set("maxResults", strconv.Itoa(max))
	q.Set("q", fmt.Sprintf("after:%d", since.UTC().Unix()))

	body, status, err := c.doGET(ctx, "/messages?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("gmail list status=%d body=%s", status, truncate(string(body), 300))
	}
	var lr struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(lr.Messages))
	for _, m := range lr.Messages {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// GetRawMessage downloads one message in raw RFC822 form.
func (c *Client) GetRawMessage(ctx context.Context, id string) (RawMessage, error) {
	body, status, err := c.doGET(ctx, "/messages/"+url.PathEscape(id)+"?format=raw")
	if err != nil {
		return RawMessage{}, err
	}
	if status != http.StatusOK {
		return RawMessage{}, fmt.Errorf("gmail get raw status=%d body=%s", status, truncate(string(body), 200))
	}
	var mr struct {
		ID           string `json:"id"`
		Raw          string `json:"raw"`
		InternalDate string `json:"internalDate"` // ms since epoch as string
	}
	if err := json.Unmarshal(body, &mr); err != nil {
		return RawMessage{}, err
	}
	raw, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(strings.TrimRight(mr.Raw, "="))
	if err != nil {
		return RawMessage{}, fmt.Errorf("gmail raw decode: %w", err)
	}
	out := RawMessage{ID: mr.ID, Raw: raw}
	if ms, err := strconv.ParseInt(mr.InternalDate, 10, 64); err == nil && ms > 0 {
		out.InternalDate = time.UnixMilli(ms).UTC()
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
