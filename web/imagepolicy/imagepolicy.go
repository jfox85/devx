// Package imagepolicy is the single source of truth for which image types the
// upload flow accepts and the maximum upload size.
//
// The accepted MIME/extension set is enforced in three places: the HTTP upload
// handlers (package web), the desktop file-drop/clipboard bridge (the Wails
// shell), and the SPA. Keeping the values here means a type/ext policy change is
// one edit; the frontend mirror in web/app/src/lib/imagePolicy.js is kept in
// lockstep by a drift test (frontend_drift_test.go).
//
// MaxUploadBytes is authoritative but enforced only server-side (and in the
// desktop host, which fails fast before bridging). The SPA deliberately does
// not mirror or pre-check the size cap — the server is the boundary that rejects
// oversize uploads — so MaxUploadBytes intentionally has no imagePolicy.js
// counterpart and is not part of the drift check.
package imagepolicy

// MaxUploadBytes is the maximum accepted image size. The server rejects larger
// uploads; the desktop bridge fails fast before base64-encoding instead of
// handing the API a payload it will reject.
const MaxUploadBytes = 20 << 20 // 20 MiB

// MIMEToExt maps every accepted image MIME type to its canonical extension.
// The upload handler sniffs magic bytes and looks the result up here; an
// unknown type is rejected.
var MIMEToExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// ExtToMIME maps each accepted file extension (lowercase, dot-prefixed) to a
// MIME type. The desktop file-drop path keys off extension because OS drops
// carry paths, not MIME types. ".jpeg" is an accepted alias for ".jpg".
var ExtToMIME = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}
