package artifactbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/piagent"
)

// Upload protocol
//
// A reference file is sent as one or more sequential chunks, all carrying the
// same idempotency_key and the same declared filename, mime_type, size and
// sha256. Each chunk names the byte offset it starts at; the server accepts
// it only at exactly the number of bytes received so far (a retried chunk
// that matches bytes already received is acknowledged without writing).
// Partial bytes are staged in DevX state, outside every worktree. When the
// last byte arrives the whole file is checked against the declared size,
// SHA-256 and type (magic bytes / UTF-8 / JSON), then registered through the
// normal DevX artifact store under .artifacts/attachments/ with tag
// remote-attachment. Nothing is ever overwritten: a name collision gets a
// numeric suffix. Repeating the call after completion returns the same
// artifact. A call with no data_base64 just reports progress, so an
// interrupted client can resume from next_offset.

var (
	filenameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,99}$`)
	sha256Re   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// UploadRequest is one upload call.
type UploadRequest struct {
	AgentID        string
	IdempotencyKey string
	Filename       string
	MIMEType       string
	Size           int64
	SHA256         string
	Title          string
	Summary        string
	Offset         int64
	DataBase64     *string
}

type uploadRecord struct {
	AgentID    string    `json:"agent_id"`
	Session    string    `json:"session"`
	KeyHash    string    `json:"key_hash"`
	Filename   string    `json:"filename"`
	MIMEType   string    `json:"mime_type"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary,omitempty"`
	Received   int64     `json:"received"`
	State      string    `json:"state"` // receiving | complete
	ManifestID string    `json:"manifest_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (r *uploadRecord) sameRequest(o *uploadRecord) bool {
	return r.Filename == o.Filename && r.MIMEType == o.MIMEType && r.Size == o.Size && r.SHA256 == o.SHA256 &&
		r.Title == o.Title && r.Summary == o.Summary && r.Session == o.Session
}

func (s *Service) uploadsDir(agentID string) string {
	return filepath.Join(s.Cfg.StateDir, "uploads", agentID)
}

// Upload processes one chunk (or a progress probe) of an attachment upload.
func (s *Service) Upload(req UploadRequest) (map[string]any, error) {
	if !s.Cfg.Upload {
		return nil, errf(codeDenied, "attachment upload is not enabled")
	}
	if s.Cfg.StateDir == "" || !filepath.IsAbs(s.Cfg.StateDir) {
		return nil, errf(codeFailed, "upload staging is not configured")
	}
	sc, err := s.authorize(req.AgentID)
	if err != nil {
		return nil, err
	}
	want, err := s.validateUpload(req)
	if err != nil {
		return nil, err
	}
	var chunk []byte
	if req.DataBase64 != nil {
		if base64.StdEncoding.DecodedLen(len(*req.DataBase64)) > MaxUploadChunk+3 {
			return nil, errf(codeTooLarge, "a chunk may carry at most %d bytes", MaxUploadChunk)
		}
		chunk, err = base64.StdEncoding.Strict().DecodeString(*req.DataBase64)
		if err != nil {
			return nil, errf(codeInvalid, "data_base64 is not valid standard base64")
		}
		if len(chunk) == 0 || len(chunk) > MaxUploadChunk {
			return nil, errf(codeTooLarge, "a chunk must carry 1..%d bytes", MaxUploadChunk)
		}
	}
	want.AgentID, want.Session = sc.agent.ID, sc.sess.Name
	want.KeyHash = keyHash(sc.agent.ID, req.IdempotencyKey)

	dir := s.uploadsDir(sc.agent.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errf(codeFailed, "upload staging is unavailable")
	}
	release, err := piagent.AcquireOwnedLock(filepath.Join(dir, ".lock"))
	if err != nil {
		return nil, errf(codeFailed, "upload staging is busy; retry")
	}
	defer release()
	return s.uploadLocked(sc, dir, want, req.Offset, chunk, req.DataBase64 != nil)
}

func keyHash(agentID, key string) string {
	h := sha256.Sum256([]byte("devx-upload|v1|" + agentID + "|" + key))
	return hex.EncodeToString(h[:16])
}

func (s *Service) validateUpload(req UploadRequest) (*uploadRecord, error) {
	k := req.IdempotencyKey
	if strings.TrimSpace(k) == "" || len(k) > 200 {
		return nil, errf(codeInvalid, "idempotency_key is required (max 200 chars)")
	}
	if !filenameRe.MatchString(req.Filename) || strings.Contains(req.Filename, "..") {
		return nil, errf(codeInvalid, "filename must be a plain name of letters, digits, '.', '_', '-' or space (max 100), with no directory part")
	}
	mt, err := normalizeUploadMIME(req.MIMEType)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(path.Ext(req.Filename))
	okExt := false
	for _, e := range uploadTypes[mt] {
		if e == ext {
			okExt = true
		}
	}
	if !okExt {
		return nil, errf(codeUnsupportedType, "filename extension %q does not match mime_type %s", ext, mt)
	}
	if req.Size <= 0 || req.Size > s.Cfg.MaxUploadBytes {
		return nil, errf(codeTooLarge, "size must be 1..%d bytes", s.Cfg.MaxUploadBytes)
	}
	if !sha256Re.MatchString(req.SHA256) {
		return nil, errf(codeInvalid, "sha256 must be 64 lowercase hex digits")
	}
	if req.Offset < 0 || req.Offset > req.Size {
		return nil, errf(codeInvalid, "offset must be within 0..size")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = req.Filename
	}
	if len(title) > 200 || len(req.Summary) > 2000 {
		return nil, errf(codeInvalid, "title max 200, summary max 2000 characters")
	}
	return &uploadRecord{Filename: req.Filename, MIMEType: mt, Size: req.Size, SHA256: req.SHA256, Title: title, Summary: req.Summary}, nil
}

func readRecord(p string) (*uploadRecord, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var r uploadRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func writeRecord(p string, r *uploadRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *Service) uploadLocked(sc *scope, dir string, want *uploadRecord, offset int64, chunk []byte, hasData bool) (map[string]any, error) {
	now := s.Now()
	s.expireStale(dir, now)
	recPath := filepath.Join(dir, want.KeyHash+".json")
	partPath := filepath.Join(dir, want.KeyHash+".part")
	rec, err := readRecord(recPath)
	switch {
	case err == nil:
		if !rec.sameRequest(want) {
			return nil, errf(codeConflict, "idempotency_key was already used for a different upload")
		}
	case errors.Is(err, os.ErrNotExist):
		if !hasData {
			return nil, errf(codeNotFound, "no upload in progress for this idempotency_key")
		}
		if n := s.pendingCount(dir); n >= MaxPendingUploads {
			return nil, errf(codeLimit, "too many incomplete uploads for this agent (%d); finish or let them expire", n)
		}
		rec = want
		rec.State, rec.CreatedAt, rec.UpdatedAt = "receiving", now, now
		_ = os.Remove(partPath)
	default:
		return nil, errf(codeFailed, "upload state is unreadable")
	}

	if rec.State == "complete" {
		return s.completedResult(sc, rec, true)
	}
	if rec.Received == rec.Size {
		// Every byte is staged but completion was never recorded (crash
		// during verification/registration): finish now.
		return s.finish(sc, rec, recPath, partPath)
	}
	if !hasData {
		return progress(rec), nil
	}
	if offset != rec.Received {
		if offset < rec.Received && offset+int64(len(chunk)) <= rec.Received {
			if same, _ := stagedEquals(partPath, offset, chunk); same {
				return progress(rec), nil // retried chunk already stored
			}
		}
		return nil, &Error{Code: codeOffset, Msg: "chunk offset does not match the bytes received so far",
			Extra: map[string]any{"next_offset": rec.Received, "received_bytes": rec.Received}}
	}
	if rec.Received+int64(len(chunk)) > rec.Size {
		return nil, errf(codeIntegrity, "chunk would exceed the declared size")
	}
	if err := appendAt(partPath, rec.Received, chunk); err != nil {
		return nil, errf(codeFailed, "could not stage the chunk; retry from next_offset")
	}
	rec.Received += int64(len(chunk))
	rec.UpdatedAt = now
	if err := writeRecord(recPath, rec); err != nil {
		return nil, errf(codeFailed, "could not record upload progress; retry")
	}
	if rec.Received < rec.Size {
		return progress(rec), nil
	}
	return s.finish(sc, rec, recPath, partPath)
}

func progress(rec *uploadRecord) map[string]any {
	return map[string]any{"state": rec.State, "received_bytes": rec.Received, "next_offset": rec.Received, "size": rec.Size}
}

func stagedEquals(partPath string, off int64, chunk []byte) (bool, error) {
	f, err := os.Open(partPath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, len(chunk))
	if _, err := f.ReadAt(buf, off); err != nil {
		return false, err
	}
	return bytes.Equal(buf, chunk), nil
}

// appendAt writes chunk at exactly off, truncating any torn tail left by an
// interrupted earlier write.
func appendAt(partPath string, off int64, chunk []byte) error {
	f, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(off); err != nil {
		return err
	}
	if _, err := f.WriteAt(chunk, off); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Service) pendingCount(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			n++
		}
	}
	return n
}

// expireStale drops staged bytes of incomplete uploads with no progress for
// UploadExpiry. Registered attachments are never touched here; they follow
// normal DevX artifact retention.
func (s *Service) expireStale(dir string, now time.Time) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		r, err := readRecord(p)
		if err != nil || r.State == "complete" || now.Sub(r.UpdatedAt) < UploadExpiry {
			continue
		}
		_ = os.Remove(strings.TrimSuffix(p, ".json") + ".part")
		_ = os.Remove(p)
	}
}

func attachmentManifestID(keyHash string) string { return "att-" + keyHash[:20] }

// finish verifies the staged file and registers it. It is safe to re-run
// after a crash at any point: an already-registered attachment with matching
// content is adopted instead of registered twice.
func (s *Service) finish(sc *scope, rec *uploadRecord, recPath, partPath string) (map[string]any, error) {
	fail := func(e *Error) (map[string]any, error) {
		_ = os.Remove(partPath)
		_ = os.Remove(recPath)
		return nil, e
	}
	data, err := os.ReadFile(partPath)
	if err != nil {
		return nil, errf(codeFailed, "staged upload is unreadable; restart the upload")
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != rec.Size || hex.EncodeToString(sum[:]) != rec.SHA256 {
		return fail(errf(codeIntegrity, "received bytes do not match the declared size and sha256; the upload was discarded"))
	}
	if !sniffMatches(rec.MIMEType, data) {
		return fail(errf(codeUnsupportedType, "file content is not valid %s; the upload was discarded", rec.MIMEType))
	}
	mid := attachmentManifestID(rec.KeyHash)
	if m, err := artifactpkg.LoadManifest(sc.sess); err == nil {
		if existing, _ := artifactpkg.Find(m, mid); existing != nil {
			// Registered by an earlier attempt that crashed before
			// recording completion. Adopt it only if the bytes match.
			if got, err := s.fileSHA(sc, existing.File); err != nil || got != rec.SHA256 {
				return nil, errf(codeConflict, "an attachment for this key exists with different content")
			}
			return s.markComplete(sc, rec, recPath, partPath, mid)
		}
	} else {
		return nil, errf(codeUnavailable, "the session's artifact manifest is unreadable")
	}
	_, err = artifactpkg.Add(sc.sess, artifactpkg.AddOptions{
		Reader:           bytes.NewReader(data),
		Destination:      rec.Filename,
		Folder:           AttachmentsFolder,
		ID:               mid,
		Type:             artifactpkg.DetectType(rec.Filename),
		Title:            rec.Title,
		Summary:          rec.Summary,
		Agent:            attachmentAgent,
		Tags:             []string{AttachmentTag},
		NoAssetDiscovery: true,
		SuffixOnConflict: true,
		Now:              s.Now(),
	})
	if err != nil {
		return nil, errf(codeFailed, "could not register the attachment; retry the last chunk")
	}
	m, err := artifactpkg.LoadManifest(sc.sess)
	if err != nil {
		return nil, errf(codeUnavailable, "the session's artifact manifest is unreadable")
	}
	a, _ := artifactpkg.Find(m, mid)
	if a == nil {
		return nil, errf(codeFailed, "attachment registration was not recorded; retry the last chunk")
	}
	if got, err := s.fileSHA(sc, a.File); err != nil || got != rec.SHA256 {
		return nil, errf(codeIntegrity, "stored attachment failed read-back verification")
	}
	return s.markComplete(sc, rec, recPath, partPath, mid)
}

func (s *Service) fileSHA(sc *scope, rel string) (string, error) {
	f, fi, err := openUnderArtifacts(sc.sess.Path, rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h, err := hashFile(f, fi, 0, 0)
	if err != nil {
		return "", err
	}
	return h.sha256, nil
}

func (s *Service) markComplete(sc *scope, rec *uploadRecord, recPath, partPath, mid string) (map[string]any, error) {
	rec.State, rec.ManifestID, rec.Received, rec.UpdatedAt = "complete", mid, rec.Size, s.Now()
	if err := writeRecord(recPath, rec); err != nil {
		return nil, errf(codeFailed, "could not record completion; retry the last chunk")
	}
	_ = os.Remove(partPath)
	return s.completedResult(sc, rec, false)
}

func (s *Service) completedResult(sc *scope, rec *uploadRecord, replayed bool) (map[string]any, error) {
	m, err := artifactpkg.LoadManifest(sc.sess)
	if err != nil {
		return nil, errf(codeUnavailable, "the session's artifact manifest is unreadable")
	}
	a, _ := artifactpkg.Find(m, rec.ManifestID)
	if a == nil {
		return nil, errf(codeNotFound, "this upload completed earlier but its attachment has since been removed from the session; upload again with a new idempotency_key")
	}
	info := s.info(sc, *a, true)
	return map[string]any{
		"state": "complete", "replayed": replayed, "received_bytes": rec.Size, "next_offset": rec.Size, "size": rec.Size,
		"artifact": info,
		// How the local Pi agent finds the file: by manifest id through the
		// DevX CLI, or at this worktree-relative path.
		"local": map[string]any{
			"manifest_id": a.ID,
			"path":        path.Join(artifactsDirName, a.File),
			"resolve":     "devx artifact url " + a.ID + " --local",
		},
	}, nil
}
