package skillruntime

import (
	"strings"
	"testing"
)

func TestDecodeExecutionResultRejectsTrailingJSON(t *testing.T) {
	if _, err := decodeExecutionResult([]byte(`{"content":"ok"} {"content":"extra"}`)); err == nil || !strings.Contains(err.Error(), "多个 JSON") {
		t.Fatalf("expected trailing JSON rejection, got %v", err)
	}
	result, err := decodeExecutionResult([]byte("{\"content\":\"ok\"}\n\t"))
	if err != nil || result.Content != "ok" {
		t.Fatalf("valid response rejected: %#v, %v", result, err)
	}
}

func TestDecodeExecutionResultRejectsUnknownFields(t *testing.T) {
	if _, err := decodeExecutionResult([]byte(`{"content":"ok","unexpected":true}`)); err == nil {
		t.Fatal("unknown response field should be rejected")
	}
}
