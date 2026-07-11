package model

// IMAPPollFolderRequest — CIMPLR sends mailbox creds; service fetches, uploads S3, parses.
type IMAPPollFolderRequest struct {
	ServiceKey     string     `json:"service_key"`
	MailboxAddress string     `json:"mailbox_address"`
	InboxID        string     `json:"inbox_id"`
	Folder         string     `json:"folder"`
	Direction      string     `json:"direction"` // RECEIVED | SENT
	LastUID        uint32     `json:"last_uid"`
	Batch          int        `json:"batch"`
	IMAP           IMAPConfig `json:"imap"`
}

type IMAPConfig struct {
	Provider    string `json:"provider"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	AuthMode    string `json:"auth_mode,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
	InboxFolder string `json:"inbox_folder"`
	SentFolder  string `json:"sent_folder"`
	UseTLS      bool   `json:"use_tls"`
}

type IMAPPollFolderResponse struct {
	Initialized bool                `json:"initialized"`
	NewLastUID  uint32              `json:"new_last_uid"`
	Messages    []IMAPPolledMessage `json:"messages"`
}

type IMAPPolledMessage struct {
	UID            uint32      `json:"uid"`
	IMAPMessageKey string      `json:"imap_message_key"`
	Parsed         ParsedEmail `json:"parsed"`
}

type GraphPollPageRequest struct {
	ServiceKey     string      `json:"service_key"`
	MailboxAddress string      `json:"mailbox_address"`
	InboxID        string      `json:"inbox_id"`
	SentFolder     bool        `json:"sent_folder"`
	Since          string      `json:"since"` // RFC3339
	Batch          int         `json:"batch"`
	Graph          GraphConfig `json:"graph"`
}

type GraphConfig struct {
	TenantLabel  string `json:"tenant_label"`
	TenantID     string `json:"tenant_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type GraphPollPageResponse struct {
	Initialized bool                 `json:"initialized"`
	NewSince    string               `json:"new_since"`
	Fetched     int                  `json:"fetched"`
	Messages    []GraphPolledMessage `json:"messages"`
}

type GraphPolledMessage struct {
	GraphMessageID string      `json:"graph_message_id"`
	CursorTime     string      `json:"cursor_time"`
	Parsed         ParsedEmail `json:"parsed"`
}

type IMAPTestRequest struct {
	ServiceKey     string     `json:"service_key"`
	MailboxAddress string     `json:"mailbox_address"`
	IMAP           IMAPConfig `json:"imap"`
}

type GraphTestRequest struct {
	ServiceKey string      `json:"service_key"`
	Graph      GraphConfig `json:"graph"`
}
