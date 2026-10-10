package artifactbridge

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	artifactpkg "github.com/jfox85/devx/artifact"
)

// Version semantics
//
// DevX artifacts are mutable files addressed by a stable manifest ID; DevX
// keeps no history of earlier contents. The bridge therefore exposes a
// content version: version = "c_" + the first 24 hex digits of the file's
// SHA-256, plus the full SHA-256 under checksums. A read may pin a version;
// if the file changed since, the read fails with version_mismatch (and
// reports the current version) instead of returning bytes from a different
// revision. Old contents are not retrievable once replaced.

// ArtifactInfo is the remote-facing description of a registered artifact.
type ArtifactInfo struct {
	ID         string            `json:"id"`
	ManifestID string            `json:"manifest_id"`
	Title      string            `json:"title"`
	Type       string            `json:"type"`
	MIMEType   string            `json:"mime_type"`
	Size       int64             `json:"size,omitempty"`
	Version    string            `json:"version,omitempty"`
	Checksums  map[string]string `json:"checksums,omitempty"`
	Created    time.Time         `json:"created"`
	Retention  string            `json:"retention"`
	Folder     string            `json:"folder,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Attachment bool              `json:"attachment,omitempty"`
	Available  bool              `json:"available"`
	// Unavailable explains why an entry cannot be read (missing file,
	// symlink, too large); it never contains a path.
	Unavailable string `json:"unavailable,omitempty"`
}

func versionOf(sum string) string { return "c_" + sum[:24] }

// hashCache avoids re-hashing unchanged files. Entries are keyed by path,
// device+inode, ctime, size and nanosecond mtime. Any write to the file
// changes ctime (which user space cannot set back), so a hit means the bytes
// are the ones that were hashed. Reads that hit the cache read only their
// window and still require identical fstat before and after the read.
type hashKey struct {
	file  string
	size  int64
	mtime int64
	sys   string
}

var hashCache = struct {
	sync.Mutex
	m map[hashKey]string
}{m: map[hashKey]string{}}

const hashCacheMax = 1024

// cacheMinAge: files changed this recently are never cached. Together with
// the coarse-timestamp rule below, a later same-size rewrite always changes
// the key: on filesystems whose timestamps have whole-second (or coarser)
// granularity, two writes within the same tick would otherwise share it.
const cacheMinAge = 2 * time.Second

func cacheKey(name string, fi os.FileInfo) hashKey {
	return hashKey{file: name, size: fi.Size(), mtime: fi.ModTime().UnixNano(), sys: fileIdentity(fi)}
}

// cacheable reports whether a hash for fi may be cached: never for a file
// whose mtime has no sub-second part (a coarse-granularity filesystem, where
// identical keys do not prove identical bytes) or that changed very recently.
func cacheable(fi os.FileInfo, now time.Time) bool {
	mt := fi.ModTime()
	return mt.Nanosecond() != 0 && now.Sub(mt) >= cacheMinAge
}

func cachedSum(k hashKey) (string, bool) {
	if k.sys == "" {
		return "", false
	}
	hashCache.Lock()
	defer hashCache.Unlock()
	s, ok := hashCache.m[k]
	return s, ok
}

func storeSum(k hashKey, sum string) {
	if k.sys == "" {
		return
	}
	hashCache.Lock()
	defer hashCache.Unlock()
	if len(hashCache.m) >= hashCacheMax {
		hashCache.m = map[hashKey]string{}
	}
	hashCache.m[k] = sum
}

// readWindow reads [off, off+n) with ReadAt plus the 512-byte head, then
// requires the file's identity (incl. ctime) to be unchanged, so the window
// belongs to the content whose hash was cached under that identity.
func readWindow(f *os.File, before os.FileInfo, off, n int64) (*hashed, error) {
	out := &hashed{size: before.Size()}
	if off < before.Size() && n > 0 {
		if off+n > before.Size() {
			n = before.Size() - off
		}
		out.chunk = make([]byte, n)
		if _, err := f.ReadAt(out.chunk, off); err != nil {
			return nil, errf(codeChanged, "artifact changed while it was being read; retry")
		}
	}
	head := minI64(512, before.Size())
	out.head = make([]byte, head)
	if _, err := f.ReadAt(out.head, 0); err != nil {
		return nil, errf(codeChanged, "artifact changed while it was being read; retry")
	}
	after, err := f.Stat()
	if err != nil || cacheKey("", after) != cacheKey("", before) {
		return nil, errf(codeChanged, "artifact changed while it was being read; retry")
	}
	return out, nil
}

// hashed is one consistent pass over an open artifact file.
type hashed struct {
	size   int64
	sha256 string
	chunk  []byte // bytes in [chunkOff, chunkOff+len) captured during the same pass
	head   []byte // first 512 bytes, for sniffing
}

// hashFile reads the whole open file once, computing SHA-256 and capturing
// the requested window and head from the same bytes. The file is fstat'ed
// before and after; any change in size or modification time means the read
// is not a consistent snapshot and is refused.
func hashFile(f *os.File, before os.FileInfo, chunkOff, chunkLen int64) (*hashed, error) {
	if before.Size() > MaxReadableBytes {
		return nil, errf(codeTooLarge, "artifact is %d bytes; the bridge reads at most %d", before.Size(), MaxReadableBytes)
	}
	h := sha256.New()
	out := &hashed{}
	buf := make([]byte, 64<<10)
	var pos int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			b := buf[:n]
			_, _ = h.Write(b)
			if len(out.head) < 512 {
				need := 512 - len(out.head)
				if need > n {
					need = n
				}
				out.head = append(out.head, b[:need]...)
			}
			if chunkLen > 0 {
				lo, hi := maxI64(chunkOff, pos), minI64(chunkOff+chunkLen, pos+int64(n))
				if lo < hi {
					out.chunk = append(out.chunk, b[lo-pos:hi-pos]...)
				}
			}
			pos += int64(n)
			if pos > MaxReadableBytes {
				return nil, errf(codeTooLarge, "artifact exceeds %d bytes", MaxReadableBytes)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errUnavailable
		}
	}
	after, err := f.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || pos != before.Size() ||
		fileIdentity(after) != fileIdentity(before) {
		return nil, errf(codeChanged, "artifact changed while it was being read; retry")
	}
	out.size = pos
	out.sha256 = hex.EncodeToString(h.Sum(nil))
	return out, nil
}

// maxTextEncodedBytes bounds the JSON-escaped size of one copy of a text
// chunk. Two copies plus metadata stay under MaxResultBytes.
const maxTextEncodedBytes = (MaxResultBytes - 6000) / 2

// fitEscaped returns the longest prefix length of valid UTF-8 data whose
// encoding/json string encoding (default HTML escaping) is <= budget bytes.
func fitEscaped(data []byte, budget int) int {
	n := 0
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		w := size
		switch {
		case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t':
			w = 2
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			w = 6
		}
		if n+w > budget {
			return i
		}
		n += w
		i += size
	}
	return len(data)
}

func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Service) info(sc *scope, a artifactpkg.Artifact, withHash bool) ArtifactInfo {
	it := ArtifactInfo{
		ID: opaqueID(sc.agent.ID, sc.sess.Name, a.ID), ManifestID: a.ID, Title: a.Title, Type: a.Type,
		MIMEType: mimeForFile(a.File), Created: a.Created, Retention: a.Retention, Folder: a.Folder, Tags: a.Tags,
	}
	if it.Retention == "" {
		it.Retention = artifactpkg.DefaultRetention
	}
	if a.Summary != nil {
		it.Summary = truncateRunes(*a.Summary, 500)
	}
	for _, t := range a.Tags {
		if t == AttachmentTag {
			it.Attachment = true
		}
	}
	f, fi, err := openUnderArtifacts(sc.sess.Path, a.File)
	if err != nil {
		it.Unavailable = asError(err).Msg
		return it
	}
	defer func() { _ = f.Close() }()
	it.Size = fi.Size()
	if fi.Size() > MaxReadableBytes {
		it.Unavailable = "artifact is larger than the bridge read limit"
		return it
	}
	it.Available = true
	if withHash {
		key := cacheKey(sc.sess.Path+"\x00"+a.File, fi)
		sum, ok := cachedSum(key)
		if !ok {
			h, err := hashFile(f, fi, 0, 0)
			if err != nil {
				it.Available, it.Unavailable = false, asError(err).Msg
				return it
			}
			sum = h.sha256
			if cacheable(fi, time.Now()) {
				storeSum(key, sum)
			}
		}
		it.Version = versionOf(sum)
		it.Checksums = map[string]string{"sha256": sum}
	}
	return it
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// ListRequest selects artifacts of one agent's session.
type ListRequest struct {
	AgentID string
	Type    string
	Search  string
	Cursor  int
	Limit   int
}

// List returns registered artifacts, newest first.
func (s *Service) List(req ListRequest) (map[string]any, error) {
	sc, err := s.authorize(req.AgentID, capRead)
	if err != nil {
		return nil, err
	}
	m, err := s.manifest(sc)
	if err != nil {
		return nil, errf(codeUnavailable, "the session's artifact manifest is unreadable")
	}
	items := artifactpkg.Filter(m.Artifacts, artifactpkg.FilterOptions{Type: req.Type, Search: req.Search})
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].Created.Equal(items[j].Created) {
			return items[i].Created.After(items[j].Created)
		}
		return items[i].ID < items[j].ID
	})
	limit := req.Limit
	if limit <= 0 || limit > MaxListPage {
		limit = MaxListPage
	}
	if req.Cursor < 0 || req.Cursor > len(items) {
		return nil, errf(codeInvalid, "cursor is out of range")
	}
	end := minInt(req.Cursor+limit, len(items))
	out := make([]ArtifactInfo, 0, end-req.Cursor)
	for _, a := range items[req.Cursor:end] {
		out = append(out, s.info(sc, a, true))
	}
	res := map[string]any{"agent_id": sc.agent.ID, "session": sc.sess.Name, "artifacts": out, "total": len(items)}
	if end < len(items) {
		res["next_cursor"] = end
	}
	return res, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// resolve maps an opaque ID to a manifest entry within the scope.
func (s *Service) resolve(sc *scope, id string) (artifactpkg.Artifact, error) {
	if !strings.HasPrefix(id, "dxa_") || len(id) != 28 {
		return artifactpkg.Artifact{}, errf(codeInvalid, "artifact_id must be an id returned by devx_artifact_list")
	}
	m, err := s.manifest(sc)
	if err != nil {
		return artifactpkg.Artifact{}, errf(codeUnavailable, "the session's artifact manifest is unreadable")
	}
	for _, a := range m.Artifacts {
		if opaqueID(sc.agent.ID, sc.sess.Name, a.ID) == id {
			return a, nil
		}
	}
	return artifactpkg.Artifact{}, errf(codeNotFound, "no artifact %s in this agent's session", id)
}

// ReadRequest reads part of one artifact.
type ReadRequest struct {
	AgentID    string
	ArtifactID string
	Version    string
	Offset     int64
	MaxBytes   int
	// Encoding: auto (default), text, base64 or image.
	Encoding string
}

// ReadResult is the metadata part of a read; the payload travels in Text,
// Base64 or Image.
type ReadResult struct {
	Artifact    ArtifactInfo `json:"artifact"`
	Encoding    string       `json:"encoding"`
	Offset      int64        `json:"offset"`
	Length      int          `json:"length"`
	NextOffset  int64        `json:"next_offset"`
	EOF         bool         `json:"eof"`
	ChunkSHA256 string       `json:"chunk_sha256"`
	Text        *string      `json:"text,omitempty"`
	Base64      *string      `json:"data_base64,omitempty"`
	image       []byte
}

// Read returns one bounded chunk of a registered artifact, verified against
// the requested version, plus a full-content checksum from the same pass.
func (s *Service) Read(req ReadRequest) (*ReadResult, error) {
	sc, err := s.authorize(req.AgentID, capRead)
	if err != nil {
		return nil, err
	}
	a, err := s.resolve(sc, req.ArtifactID)
	if err != nil {
		return nil, err
	}
	enc := req.Encoding
	if enc == "" {
		enc = "auto"
	}
	if enc != "auto" && enc != "text" && enc != "base64" && enc != "image" {
		return nil, errf(codeInvalid, "encoding must be auto, text, base64 or image")
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > MaxReadChunk {
		maxBytes = MaxReadChunk
	}
	if req.Offset < 0 {
		return nil, errf(codeInvalid, "offset must be >= 0")
	}
	f, fi, err := openUnderArtifacts(sc.sess.Path, a.File)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	mimeType := mimeForFile(a.File)
	if enc == "auto" {
		switch {
		case inlineImageMIME[mimeType] && req.Offset == 0 && fi.Size() <= MaxInlineImageBytes:
			enc = "image"
		case isTextMIME(mimeType):
			enc = "text"
		default:
			enc = "base64"
		}
	}
	window := int64(maxBytes)
	if enc == "image" {
		if !inlineImageMIME[mimeType] {
			return nil, errf(codeUnsupportedType, "only PNG, JPEG, GIF and WebP artifacts can be returned as images; use encoding=base64")
		}
		if req.Offset != 0 || fi.Size() > MaxInlineImageBytes {
			return nil, errf(codeTooLarge, "inline images are limited to %d bytes; read it with encoding=base64 in chunks", MaxInlineImageBytes)
		}
		window = fi.Size()
	}
	if enc == "text" && !isTextMIME(mimeType) {
		return nil, errf(codeUnsupportedType, "artifact is %s; use encoding=base64", mimeType)
	}
	// Text reads extend the window by up to 3 bytes so a chunk can end on a
	// rune boundary without a second pass.
	capture := window
	if enc == "text" {
		capture += 3
	}
	if fi.Size() > MaxReadableBytes {
		return nil, errf(codeTooLarge, "artifact is %d bytes; the bridge reads at most %d", fi.Size(), MaxReadableBytes)
	}
	key := cacheKey(sc.sess.Path+"\x00"+a.File, fi)
	var h *hashed
	if sum, ok := cachedSum(key); ok {
		h, err = readWindow(f, fi, req.Offset, capture)
		if err != nil {
			return nil, err
		}
		h.sha256 = sum
	} else {
		h, err = hashFile(f, fi, req.Offset, capture)
		if err != nil {
			return nil, err
		}
		if cacheable(fi, time.Now()) {
			storeSum(key, h.sha256)
		}
	}
	info := s.info(sc, a, false)
	info.Size, info.Version, info.Checksums, info.Available = h.size, versionOf(h.sha256), map[string]string{"sha256": h.sha256}, true
	if req.Version != "" && req.Version != info.Version {
		return nil, &Error{Code: codeVersionMismatch, Msg: "artifact content changed since that version; re-list and restart the read",
			Extra: map[string]any{"current_version": info.Version}}
	}
	if req.Offset > h.size {
		return nil, errf(codeInvalid, "offset %d is beyond the end of the artifact (%d bytes)", req.Offset, h.size)
	}
	if enc == "image" && !sniffMatches(mimeType, h.head) {
		return nil, errf(codeUnsupportedType, "artifact bytes are not a valid %s image", mimeType)
	}
	data := h.chunk
	if int64(len(data)) > window {
		data = data[:window]
	}
	res := &ReadResult{Artifact: info, Encoding: enc, Offset: req.Offset}
	switch enc {
	case "text":
		if len(h.chunk) > 0 && !utf8.RuneStart(h.chunk[0]) {
			return nil, errf(codeInvalid, "offset is not on a UTF-8 character boundary; continue from next_offset")
		}
		// When more bytes follow the window, trim back so the chunk ends
		// on a complete character; the next read starts at that boundary.
		end := len(data)
		if end < len(h.chunk) {
			for end > 0 && !utf8.RuneStart(h.chunk[end]) {
				end--
			}
		}
		data = h.chunk[:end]
		if len(data) == 0 && len(h.chunk) > 0 {
			return nil, errf(codeInvalid, "max_bytes is too small for the next character")
		}
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			return nil, errf(codeUnsupportedType, "artifact is not valid UTF-8 text; use encoding=base64")
		}
		// The text travels twice (content block and structuredContent.text),
		// JSON-escaped. Shorten the chunk on a character boundary so the
		// encoded result always fits the transport budget, even for text
		// that escapes heavily (control characters, <, >, &).
		data = data[:fitEscaped(data, maxTextEncodedBytes)]
		t := string(data)
		res.Text = &t
	case "base64":
		b := base64.StdEncoding.EncodeToString(data)
		res.Base64 = &b
	case "image":
		res.image = data
	}
	sum := sha256.Sum256(data)
	res.Length, res.ChunkSHA256 = len(data), hex.EncodeToString(sum[:])
	res.NextOffset = req.Offset + int64(len(data))
	res.EOF = res.NextOffset >= h.size
	return res, nil
}
