package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInjectTerminalCopyOnSelect(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:   io.NopCloser(strings.NewReader("<html><head></head><body>tty</body></html>")),
	}
	if err := injectTerminalCopyOnSelect(resp); err != nil {
		t.Fatalf("injectTerminalCopyOnSelect: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, "__devxCopyOnSelect") || !strings.Contains(got, "navigator.clipboard.writeText") {
		t.Fatalf("copy-on-select script missing from response: %s", got)
	}
	if !strings.Contains(got, `/nerd-font.css`) || !strings.Contains(got, "overscroll-behavior") {
		t.Fatalf("terminal head addons missing from response: %s", got)
	}
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		t.Fatal("content encoding should be cleared after body rewrite")
	}
}

func TestInjectTerminalCopyOnSelectDecodesGzip(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("<html><head></head><body>tty</body></html>"))
	_ = gz.Close()
	resp := &http.Response{
		Header: http.Header{
			"Content-Type":     []string{"text/html"},
			"Content-Encoding": []string{"gzip"},
		},
		Body: io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	if err := injectTerminalCopyOnSelect(resp); err != nil {
		t.Fatalf("injectTerminalCopyOnSelect: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "__devxCopyOnSelect") || strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		t.Fatalf("gzip html was not decoded and rewritten correctly: encoding=%q body=%q", resp.Header.Get("Content-Encoding"), body)
	}
}

func TestInjectTerminalCopyOnSelectLeavesUnknownEncodingUntouched(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Content-Type":     []string{"text/html"},
			"Content-Encoding": []string{"br"},
		},
		Body: io.NopCloser(strings.NewReader("compressed")),
	}
	if err := injectTerminalCopyOnSelect(resp); err != nil {
		t.Fatalf("injectTerminalCopyOnSelect: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "compressed" || resp.Header.Get("Content-Encoding") != "br" {
		t.Fatalf("unknown encoding should be untouched: encoding=%q body=%q", resp.Header.Get("Content-Encoding"), body)
	}
}

func TestInjectTerminalCopyOnSelectSkipsNonHTML(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/javascript"}},
		Body:   io.NopCloser(strings.NewReader("console.log('tty')")),
	}
	if err := injectTerminalCopyOnSelect(resp); err != nil {
		t.Fatalf("injectTerminalCopyOnSelect: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "__devxCopyOnSelect") {
		t.Fatalf("script should not be injected into non-html: %s", body)
	}
}
