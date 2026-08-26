package model

import "testing"

func TestNormalizeReasonCodes(t *testing.T) {
	got := NormalizeReasonCodes([]string{" insufficient_information ", "INSUFFICIENT_INFORMATION", "", "site_affected"})
	if len(got) != 2 || got[0] != "insufficient_information" || got[1] != "site_affected" {
		t.Fatalf("unexpected normalized codes: %#v", got)
	}
	if !HasReasonCode(got, " Insufficient_Information ") {
		t.Fatal("expected case-insensitive match")
	}
}
