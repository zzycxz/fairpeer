package builtin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestResolveAddrs(t *testing.T) {
	// Array form.
	arr, _ := json.Marshal([]string{"a@x.com", "b@y.com"})
	got, err := resolveAddrs(arr)
	if err != nil || len(got) != 2 || got[0] != "a@x.com" || got[1] != "b@y.com" {
		t.Fatalf("array form: got %v err %v", got, err)
	}
	// Comma string form (with whitespace to trim).
	got, err = resolveAddrs([]byte(`"a@x.com, b@y.com , c@z.com"`))
	if err != nil || len(got) != 3 {
		t.Fatalf("string form: got %v err %v", got, err)
	}
	// Empty.
	got, err = resolveAddrs(nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: got %v err %v", got, err)
	}
	// Invalid (number).
	_, err = resolveAddrs([]byte(`123`))
	if err == nil {
		t.Fatal("invalid should error")
	}
}

func TestBuildMessageNoAttachments(t *testing.T) {
	msg, err := buildMessage("from@x.com", []string{"to@x.com"}, nil, nil, "Hello 世界", "Body text", "text", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	// Headers present.
	for _, want := range []string{"From: from@x.com", "To: to@x.com", "Subject:", "Date:", "MIME-Version: 1.0", "Content-Type: text/text; charset=UTF-8"} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing %q\n---\n%s", want, s)
		}
	}
	// Body present.
	if !strings.Contains(s, "Body text") {
		t.Error("body missing")
	}
	// UTF-8 subject is encoded (the raw 中文 shouldn't appear unencoded in the header).
	if strings.Contains(strings.SplitN(s, "\r\n\r\n", 2)[0], "世界") {
		t.Error("non-ASCII subject should be MIME-encoded, not raw")
	}
}

func TestBuildMessageWithAttachment(t *testing.T) {
	// Create a temp file to attach.
	tmp := t.TempDir() + "/report.txt"
	content := []byte("report content")
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := buildMessage("from@x.com", []string{"to@x.com"}, nil, nil, "Report", "See attached", "text", []string{tmp})
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	if !strings.Contains(s, "multipart/mixed") {
		t.Error("should be multipart with attachments")
	}
	if !strings.Contains(s, `filename="report.txt"`) {
		t.Error("attachment filename header missing")
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Error("attachment should be base64-encoded")
	}
}

func TestBuildMessageHTML(t *testing.T) {
	msg, err := buildMessage("from@x.com", []string{"to@x.com"}, nil, nil, "S", "<b>hi</b>", "html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(msg), "Content-Type: text/html; charset=UTF-8") {
		t.Error("html format should set text/html content type")
	}
}

// TestBuildMessageRejectsHeaderInjection guards the audit case: To/Cc/Bcc were
// joined raw into headers, so an address carrying CR/LF could inject arbitrary
// header lines (e.g. a hidden Bcc) into every recipient's copy. buildMessage
// must reject such addresses (and subjects) with an error naming the offender.
func TestBuildMessageRejectsHeaderInjection(t *testing.T) {
	bad := []string{
		"a@x.com\r\nBcc: attacker@evil.com",
		"a@x.com\nBcc: attacker@evil.com",
		"a@x.com\rBcc: attacker@evil.com",
		"a@x.com\x00",
		"bad local@x.com", // space in the local part (no display-name form)
	}
	for _, addr := range bad {
		if _, err := buildMessage("from@x.com", []string{addr}, nil, nil, "S", "b", "text", nil); err == nil {
			t.Errorf("To %q should be rejected", addr)
		}
		if _, err := buildMessage("from@x.com", nil, []string{addr}, nil, "S", "b", "text", nil); err == nil {
			t.Errorf("Cc %q should be rejected", addr)
		}
		if _, err := buildMessage("from@x.com", nil, nil, []string{addr}, "S", "b", "text", nil); err == nil {
			t.Errorf("Bcc %q should be rejected", addr)
		}
	}
	// The subject is emitted as a raw header line — CRLF injects there too.
	if _, err := buildMessage("from@x.com", []string{"a@x.com"}, nil, nil, "Hi\r\nBcc: attacker@evil.com", "b", "text", nil); err == nil {
		t.Error("CRLF in subject should be rejected")
	}
	// Legitimate display-name addresses must keep working.
	if _, err := buildMessage("Zhang San <from@x.com>", []string{"Li Si <to@x.com>"}, nil, nil, "S", "b", "text", nil); err != nil {
		t.Errorf("display-name address wrongly rejected: %v", err)
	}
}
