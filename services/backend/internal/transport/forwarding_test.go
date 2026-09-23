package transport

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestForwardedFilesKeepsTwoExactAttachmentsAndRejectsUnsupported(t *testing.T) {
	first := []byte("period,amount\n2026-09,42\n")
	second := []byte("%PDF-1.7\nsynthetic\n%%EOF")
	makeMail := func(name string, body []byte) []byte {
		return []byte(fmt.Sprintf("Subject: September support\r\nContent-Type: multipart/mixed; boundary=close\r\n\r\n--close\r\nContent-Type: text/plain\r\n\r\nPlease review.\r\n--close\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=ledger.csv\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--close\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"%s\"\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--close--\r\n", base64.StdEncoding.EncodeToString(first), name, base64.StdEncoding.EncodeToString(body)))
	}
	files, subject, err := forwardedFiles(makeMail("invoice.pdf", second))
	if err != nil || subject != "September support" || len(files) != 2 || string(files[0].Body) != string(first) || string(files[1].Body) != string(second) {
		t.Fatalf("two original attachments required: files=%d subject=%q err=%v", len(files), subject, err)
	}
	if _, _, err := forwardedFiles(makeMail("invoice.exe", second)); err == nil || !strings.Contains(err.Error(), "TXT/CSV") {
		t.Fatalf("unsupported attachment must reject the whole email: %v", err)
	}
	encoded := strings.Replace(string(makeMail("=?UTF-8?B?aW52b2ljZS5wZGY=?=", second)), "Subject: September support", "Subject: =?UTF-8?B?U2VwdGVtYmVyIHN1cHBvcnQ=?=", 1)
	files, subject, err = forwardedFiles([]byte(encoded))
	if err != nil || subject != "September support" || len(files) != 2 || files[1].Name != "invoice.pdf" {
		t.Fatalf("encoded email headers should retain the original attachment: files=%v subject=%q err=%v", files, subject, err)
	}
	nested := []byte(fmt.Sprintf("Subject: September support\r\nContent-Type: multipart/alternative; boundary=outer\r\n\r\n--outer\r\nContent-Type: text/plain\r\n\r\nPlease review.\r\n--outer\r\nContent-Type: multipart/mixed; boundary=inner\r\n\r\n--inner\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=invoice.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--inner--\r\n--outer--\r\n", base64.StdEncoding.EncodeToString(second)))
	files, _, err = forwardedFiles(nested)
	if err != nil || len(files) != 1 || string(files[0].Body) != string(second) {
		t.Fatalf("nested MIME attachment should be accepted: files=%v err=%v", files, err)
	}
}
