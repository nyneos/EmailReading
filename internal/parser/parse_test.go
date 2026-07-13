package parser

import "testing"

func TestSplitAddrsOutlookSemicolon(t *testing.T) {
	got := splitAddrs("Kanav Arora <kanav.arora@nyneos.com>; Bharat Shukla <bharat@cashinvoice.in>")
	want := []string{"kanav.arora@nyneos.com", "bharat@cashinvoice.in"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestSplitAddrsRFCComma(t *testing.T) {
	got := splitAddrs("kanav.arora@nyneos.com, bharat@cashinvoice.in")
	want := []string{"kanav.arora@nyneos.com", "bharat@cashinvoice.in"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
