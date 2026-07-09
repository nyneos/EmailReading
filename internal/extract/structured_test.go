package extract

import (
	"testing"

	"EmailService/internal/model"
)

func TestExtractHTMLTableAsRecords(t *testing.T) {
	html := `<table><tr><th>Bank</th><th>Rate</th></tr><tr><td>SBI</td><td>7.1%</td></tr></table>`
	_, meta, conf := Run("fd-rate-negotiation", model.ParsedEmail{
		Envelope: model.Envelope{Subject: "Offer", From: "a@b.com"},
		Body:     model.Body{TextHTML: html, Preferred: "html"},
	})
	tables, ok := meta["tables"].([]map[string]interface{})
	if !ok || len(tables) == 0 {
		t.Fatalf("expected tables, got %v", meta["tables"])
	}
	recs, ok := tables[0]["records"].([]map[string]string)
	if !ok || len(recs) != 1 {
		t.Fatalf("expected 1 record, got %v", tables[0])
	}
	if recs[0]["bank"] != "SBI" || recs[0]["rate"] != "7.1%" {
		t.Fatalf("unexpected record: %+v", recs[0])
	}
	if conf < 0.7 {
		t.Fatalf("expected higher confidence, got %v", conf)
	}
}

func TestExtractKeyValues(t *testing.T) {
	text := "Account Number: 1234567890\nIFSC Code: SBIN0001234\nAmount: INR 10,00,000"
	_, meta, _ := Run("", model.ParsedEmail{
		Envelope: model.Envelope{Subject: "stmt"},
		Body:     model.Body{TextPlain: text, Preferred: "text"},
	})
	kv, ok := meta["key_values"].(map[string]string)
	if !ok {
		t.Fatalf("expected key_values")
	}
	if kv["account_number"] != "1234567890" {
		t.Fatalf("account_number: %v", kv)
	}
	if kv["ifsc_code"] != "SBIN0001234" {
		t.Fatalf("ifsc_code: %v", kv)
	}
}
