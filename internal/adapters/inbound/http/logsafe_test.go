package http

import "testing"

func TestSanitizeLogStripsNewlines(t *testing.T) {
	if got := sanitizeLog("a\r\nb\nc"); got != "abc" {
		t.Fatalf("sanitizeLog = %q, want abc", got)
	}
}
