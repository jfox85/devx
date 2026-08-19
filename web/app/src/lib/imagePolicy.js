// Single frontend definition of the image-upload policy. This mirrors the Go
// source of truth in web/imagepolicy/imagepolicy.go; a drift test
// (web/imagepolicy/frontend_drift_test.go) parses this file and fails CI if the
// two ever disagree, so a policy change stays a single coordinated edit.

// Accepted MIME types, matching imagepolicy.MIMEToExt keys.
export const ALLOWED_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']

// Accepted file extensions (lowercase, dot-prefixed), matching the keys of
// imagepolicy.ExtToMIME. Used as a fallback when a dropped/pasted file has a
// missing or generic MIME type (e.g. macOS Finder).
export const ALLOWED_IMAGE_EXTS = ['.png', '.jpg', '.jpeg', '.gif', '.webp']

// isImageFile reports whether a File is an accepted image, by MIME type or, as
// a fallback, by extension.
export function isImageFile(f) {
  if (ALLOWED_IMAGE_TYPES.includes(f.type)) return true
  const name = (f.name || '').toLowerCase()
  return ALLOWED_IMAGE_EXTS.some((ext) => name.endsWith(ext))
}
