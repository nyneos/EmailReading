package filter

import (
	"path"
	"strings"
)

// Filters mirrors inbox_config.filters_json from the main API.
type Filters struct {
	Senders          []string `json:"senders"`
	Recipients       []string `json:"recipients"`
	Domains          []string `json:"domains"`
	Subjects         []string `json:"subjects"`
	ExcludeSenders   []string `json:"exclude_senders"`
	HasAttachments   *bool    `json:"has_attachments"`
	AttachmentTypes  []string `json:"attachment_types"`
}

type MatchInput struct {
	From            string
	To              []string
	Subject         string
	HasAttachments  bool
	AttachmentNames []string
}

func Match(f Filters, in MatchInput) bool {
	from := strings.ToLower(strings.TrimSpace(in.From))
	subject := strings.TrimSpace(in.Subject)

	for _, pat := range f.ExcludeSenders {
		if globMatch(strings.ToLower(pat), from) {
			return false
		}
	}

	if len(f.Senders) > 0 {
		if !anyGlobMatch(f.Senders, from) {
			return false
		}
	}

	if len(f.Domains) > 0 {
		domain := extractDomain(from)
		if !anyGlobMatch(f.Domains, domain) {
			return false
		}
	}

	if len(f.Recipients) > 0 {
		matched := false
		for _, to := range in.To {
			if anyGlobMatch(f.Recipients, strings.ToLower(strings.TrimSpace(to))) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(f.Subjects) > 0 {
		if !anyGlobMatch(f.Subjects, subject) {
			return false
		}
	}

	if f.HasAttachments != nil {
		if *f.HasAttachments != in.HasAttachments {
			return false
		}
	}

	if len(f.AttachmentTypes) > 0 && in.HasAttachments {
		if !attachmentTypeMatch(f.AttachmentTypes, in.AttachmentNames) {
			return false
		}
	}

	return true
}

func anyGlobMatch(patterns []string, value string) bool {
	for _, p := range patterns {
		if globMatch(strings.ToLower(strings.TrimSpace(p)), strings.ToLower(value)) {
			return true
		}
	}
	return false
}

func globMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(value))
	return ok
}

func extractDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return email
	}
	return strings.ToLower(email[at+1:])
}

func attachmentTypeMatch(types []string, names []string) bool {
	for _, name := range names {
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
		for _, t := range types {
			t = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(t)), ".")
			if t != "" && t == ext {
				return true
			}
		}
	}
	return false
}
