package artifactbridge

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

// extMIME maps registered artifact extensions to the MIME type reported to
// remote clients. Anything else is application/octet-stream. The type is
// descriptive only: content is always returned as text or base64 data and is
// never rendered by DevX; only sniff-verified raster images are offered as
// MCP image blocks.
var extMIME = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".svg": "image/svg+xml",
	".md":  "text/markdown", ".markdown": "text/markdown", ".txt": "text/plain", ".log": "text/plain",
	".diff": "text/x-diff", ".patch": "text/x-diff", ".csv": "text/csv", ".json": "application/json",
	".html": "text/html", ".htm": "text/html", ".css": "text/css",
	".pdf": "application/pdf",
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
}

func mimeForFile(name string) string {
	if m, ok := extMIME[strings.ToLower(path.Ext(name))]; ok {
		return m
	}
	return "application/octet-stream"
}

// isTextMIME reports types whose bytes are returned as UTF-8 text when valid.
func isTextMIME(m string) bool {
	return strings.HasPrefix(m, "text/") || m == "application/json" || m == "image/svg+xml"
}

// inlineImageMIME are the only types ever returned as MCP image content, and
// only when the bytes sniff as that same type. SVG (scriptable) never is.
var inlineImageMIME = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

func sniffMatches(mimeType string, data []byte) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return http.DetectContentType(data) == mimeType
	case "application/pdf":
		return bytes.HasPrefix(data, []byte("%PDF-"))
	case "application/json":
		return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 && json.Valid(data)
	case "text/plain", "text/markdown", "text/csv":
		return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
	}
	return false
}

// uploadTypes is the closed allowlist of reference-file types accepted by
// devx_attachment_upload, with the extensions each may carry. No HTML, SVG,
// scripts, archives or executables.
var uploadTypes = map[string][]string{
	"image/png":        {".png"},
	"image/jpeg":       {".jpg", ".jpeg"},
	"image/gif":        {".gif"},
	"image/webp":       {".webp"},
	"application/pdf":  {".pdf"},
	"text/plain":       {".txt", ".log"},
	"text/markdown":    {".md", ".markdown"},
	"text/csv":         {".csv"},
	"application/json": {".json"},
}

// normalizeUploadMIME parses a declared MIME type strictly. Only an optional
// charset=utf-8 parameter on text types is accepted.
func normalizeUploadMIME(declared string) (string, error) {
	if declared == "" || len(declared) > 100 {
		return "", errf(codeInvalid, "mime_type is required (max 100 chars)")
	}
	mt, params, err := mime.ParseMediaType(declared)
	if err != nil {
		return "", errf(codeInvalid, "mime_type is malformed")
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") || !isTextMIME(mt) {
			return "", errf(codeInvalid, "mime_type parameters are not allowed (only charset=utf-8 on text types)")
		}
	}
	if _, ok := uploadTypes[mt]; !ok {
		return "", errf(codeUnsupportedType, "mime_type %q is not accepted for attachments", mt)
	}
	return mt, nil
}
