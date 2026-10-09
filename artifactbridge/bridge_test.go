package artifactbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

// --- fixture ---------------------------------------------------------------

type env struct {
	t        *testing.T
	svc      *Service
	store    *piagent.Store
	sessions map[string]*session.Session
	now      time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, store: piagent.NewStore(filepath.Join(t.TempDir(), "state")), sessions: map[string]*session.Session{},
		now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	e.svc = New(Config{Read: true, Upload: true, AllowedProjects: []string{"proj", "other"}, StateDir: filepath.Join(t.TempDir(), "bridge")}, e.store)
	e.svc.Sessions = func() (map[string]*session.Session, error) { return e.sessions, nil }
	e.svc.Now = func() time.Time { return e.now }
	return e
}

// agent registers a managed agent bound to a fresh worktree.
func (e *env) agent(name, project string) (*piagent.Agent, *session.Session) {
	e.t.Helper()
	wt := filepath.Join(e.t.TempDir(), name)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		e.t.Fatal(err)
	}
	id := "pa_" + hex.EncodeToString([]byte(name + "______"))[:12]
	a := &piagent.Agent{ID: id, DevxSession: name, Project: project, Worktree: wt, PiSessionID: "x", CreatedAt: e.now}
	if err := e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(a) }); err != nil {
		e.t.Fatal(err)
	}
	s := &session.Session{Name: name, ProjectAlias: project, Path: wt, ManagedAgent: id}
	e.sessions[name] = s
	return a, s
}

func (e *env) register(s *session.Session, title, dest string, data []byte) artifactpkg.Artifact {
	e.t.Helper()
	a, err := artifactpkg.Add(s, artifactpkg.AddOptions{Reader: bytes.NewReader(data), Destination: dest, Title: title, Agent: "test", Now: e.now})
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func syntheticPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 13), uint8((x ^ y) * 3), 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func shaHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func codeOf(err error) string {
	if err == nil {
		return ""
	}
	return asError(err).Code
}

func (e *env) list(agentID string) []ArtifactInfo {
	e.t.Helper()
	out, err := e.svc.List(ListRequest{AgentID: agentID})
	if err != nil {
		e.t.Fatalf("list: %v", err)
	}
	return out["artifacts"].([]ArtifactInfo)
}

func find(items []ArtifactInfo, title string) ArtifactInfo {
	for _, it := range items {
		if it.Title == title {
			return it
		}
	}
	return ArtifactInfo{}
}

// readAll reads an artifact in chunks and reassembles it.
func (e *env) readAll(agentID, id, version, enc string, chunk int) ([]byte, int) {
	e.t.Helper()
	var out []byte
	var off int64
	n := 0
	for {
		r, err := e.svc.Read(ReadRequest{AgentID: agentID, ArtifactID: id, Version: version, Offset: off, MaxBytes: chunk, Encoding: enc})
		if err != nil {
			e.t.Fatalf("read at %d: %v", off, err)
		}
		var b []byte
		switch {
		case r.Text != nil:
			if !utf8.ValidString(*r.Text) {
				e.t.Fatalf("chunk at %d is not valid UTF-8", off)
			}
			b = []byte(*r.Text)
		case r.Base64 != nil:
			b, _ = base64.StdEncoding.DecodeString(*r.Base64)
		default:
			b = r.image
		}
		if shaHex(b) != r.ChunkSHA256 || len(b) != r.Length || r.Offset != off {
			e.t.Fatalf("chunk metadata mismatch at %d", off)
		}
		out = append(out, b...)
		n++
		off = r.NextOffset
		if r.EOF {
			return out, n
		}
		if n > 10000 {
			e.t.Fatal("read did not terminate")
		}
	}
}

// --- register, list, read ----------------------------------------------------

func TestScreenshotAndMarkdownRoundTrip(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	shot := syntheticPNG(t, 64, 48)
	// Markdown with multi-byte characters spanning chunk boundaries.
	md := []byte("# Report\n\n" + strings.Repeat("Résumé — naïve café ✓ 日本語テキスト 🚀\n", 900))
	e.register(s, "Screenshot", "shot.png", shot)
	e.register(s, "Report", "report.md", md)

	items := e.list(a.ID)
	if len(items) != 2 {
		t.Fatalf("want 2 artifacts, got %d", len(items))
	}
	si, ri := find(items, "Screenshot"), find(items, "Report")
	if si.MIMEType != "image/png" || ri.MIMEType != "text/markdown" {
		t.Fatalf("mime: %s %s", si.MIMEType, ri.MIMEType)
	}
	if si.Size != int64(len(shot)) || si.Checksums["sha256"] != shaHex(shot) || si.Version != "c_"+shaHex(shot)[:24] {
		t.Fatalf("screenshot metadata: %+v", si)
	}
	if ri.Size != int64(len(md)) || ri.Checksums["sha256"] != shaHex(md) {
		t.Fatalf("report metadata: %+v", ri)
	}
	if !strings.HasPrefix(si.ID, "dxa_") || strings.Contains(si.ID, "shot") {
		t.Fatalf("id should be opaque: %s", si.ID)
	}

	// Screenshot: inline image, exact bytes.
	r, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: si.ID, Version: si.Version})
	if err != nil || r.Encoding != "image" || !bytes.Equal(r.image, shot) || !r.EOF {
		t.Fatalf("image read: %v %+v", err, r)
	}
	// Also via base64 chunks (odd chunk size).
	if got, n := e.readAll(a.ID, si.ID, si.Version, "base64", len(shot)/3+1); !bytes.Equal(got, shot) || n != 3 {
		t.Fatalf("base64 reassembly: %d chunks equal=%v", n, bytes.Equal(got, shot))
	}
	// Markdown: text chunks, every chunk valid UTF-8, exact reassembly.
	for _, size := range []int{MaxReadChunk, 4097, 1001, 7} {
		got, n := e.readAll(a.ID, ri.ID, ri.Version, "text", size)
		if !bytes.Equal(got, md) {
			t.Fatalf("text reassembly (chunk %d) differs", size)
		}
		if size == MaxReadChunk && n < 3 {
			t.Fatalf("expected multiple chunks, got %d", n)
		}
	}
	// Offsets inside a multi-byte character are refused.
	idx := bytes.Index(md, []byte("é")) + 1
	if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: ri.ID, Offset: int64(idx), Encoding: "text"}); codeOf(err) != codeInvalid {
		t.Fatalf("mid-rune offset: %v", err)
	}
	// Offset past EOF.
	if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: ri.ID, Offset: int64(len(md) + 1)}); codeOf(err) != codeInvalid {
		t.Fatalf("offset past eof: %v", err)
	}
}

func TestMCPResultsStayWithinGatewayLimits(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	md := []byte(strings.Repeat("ü", 40000)) // 80 KB of 2-byte runes
	shot := syntheticPNG(t, 100, 100)
	if len(shot) > MaxInlineImageBytes {
		t.Fatalf("fixture image too large: %d", len(shot))
	}
	e.register(s, "Report", "r.md", md)
	e.register(s, "Shot", "s.png", shot)
	items := e.list(a.ID)
	for _, it := range items {
		for _, enc := range []string{"auto", "base64"} {
			args, _ := json.Marshal(map[string]any{"agent_id": a.ID, "artifact_id": it.ID, "encoding": enc})
			res, handled := e.svc.CallTool(ToolRead, args)
			if !handled || res["isError"] == true {
				t.Fatalf("%s %s: %v", it.Title, enc, res)
			}
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
			if len(b) > 64<<10 {
				t.Fatalf("%s %s result is %d bytes (> 64 KiB gateway limit)", it.Title, enc, len(b))
			}
		}
	}
	// An image read returns a real MCP image content block.
	args, _ := json.Marshal(map[string]any{"agent_id": a.ID, "artifact_id": find(items, "Shot").ID})
	res, _ := e.svc.CallTool(ToolRead, args)
	block := res["content"].([]map[string]any)[0]
	data, _ := base64.StdEncoding.DecodeString(block["data"].(string))
	if block["type"] != "image" || block["mimeType"] != "image/png" || !bytes.Equal(data, shot) {
		t.Fatalf("image content block: %v", block["type"])
	}
}

// --- versions and concurrent change ------------------------------------------

func TestAlteredVersionIsRefused(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.register(s, "Report", "r.md", []byte(strings.Repeat("a", 5000)))
	it := e.list(a.ID)[0]
	r1, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Version: it.Version, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// The file is replaced between chunk reads.
	if err := os.WriteFile(filepath.Join(s.Path, ".artifacts", "r.md"), []byte(strings.Repeat("b", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Version: it.Version, Offset: r1.NextOffset, MaxBytes: 1000})
	if codeOf(err) != codeVersionMismatch {
		t.Fatalf("want version_mismatch, got %v", err)
	}
	if cur := asError(err).Extra["current_version"]; cur == it.Version || cur == nil {
		t.Fatalf("current_version not reported: %v", cur)
	}
	// A forged version string is refused too.
	if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Version: "c_000000000000000000000000"}); codeOf(err) != codeVersionMismatch {
		t.Fatalf("forged version: %v", err)
	}
}

func TestHashFileDetectsChangeDuringRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(p)
	defer f.Close()
	before, _ := f.Stat()
	// Simulate a writer appending after the reader captured its stat.
	if err := os.WriteFile(p, bytes.Repeat([]byte("y"), 2000), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hashFile(f, before, 0, 0); codeOf(err) != codeChanged {
		t.Fatalf("want changed_during_read, got %v", err)
	}
}

// --- scope ---------------------------------------------------------------------

func TestCrossSessionAndProjectAccessDenied(t *testing.T) {
	e := newEnv(t)
	a1, s1 := e.agent("s1", "proj")
	a2, s2 := e.agent("s2", "proj")
	a3, s3 := e.agent("s3", "forbidden") // project not in the allowlist
	e.register(s1, "One", "one.md", []byte("one"))
	e.register(s2, "Two", "two.md", []byte("two"))
	e.register(s3, "Three", "three.md", []byte("three"))
	id1 := e.list(a1.ID)[0].ID

	// Agent 2 cannot read agent 1's artifact even with its exact id.
	if _, err := e.svc.Read(ReadRequest{AgentID: a2.ID, ArtifactID: id1}); codeOf(err) != codeNotFound {
		t.Fatalf("cross-session read: %v", err)
	}
	// Same manifest id in another session yields a different opaque id.
	if opaqueID(a1.ID, "s1", "x") == opaqueID(a2.ID, "s2", "x") {
		t.Fatal("ids must be scope-bound")
	}
	// Disallowed project: denied on list, read and upload.
	if _, err := e.svc.List(ListRequest{AgentID: a3.ID}); codeOf(err) != codeDenied {
		t.Fatalf("forbidden project list: %v", err)
	}
	if _, err := e.svc.Read(ReadRequest{AgentID: a3.ID, ArtifactID: id1}); codeOf(err) != codeDenied {
		t.Fatalf("forbidden project read: %v", err)
	}
	if _, err := e.svc.Upload(uploadReq(a3.ID, "k", "n.txt", "text/plain", []byte("hi"), 0, true)); codeOf(err) != codeDenied {
		t.Fatalf("forbidden project upload: %v", err)
	}
	// Unknown and malformed agents.
	if _, err := e.svc.List(ListRequest{AgentID: "pa_000000000000"}); codeOf(err) != codeDenied {
		t.Fatalf("unknown agent: %v", err)
	}
	if _, err := e.svc.List(ListRequest{AgentID: "../../etc"}); codeOf(err) != codeInvalid {
		t.Fatalf("malformed agent: %v", err)
	}
	// Retired agent.
	now := e.now
	a2.RetiredAt = &now
	_ = e.store.WithAgentLock(a2.ID, func() error { return e.store.SaveAgent(a2) })
	if _, err := e.svc.List(ListRequest{AgentID: a2.ID}); codeOf(err) != codeDenied {
		t.Fatalf("retired agent: %v", err)
	}
	// Session record now points to another worktree (unrelated worktree).
	s1.Path = s2.Path
	if _, err := e.svc.Read(ReadRequest{AgentID: a1.ID, ArtifactID: id1}); codeOf(err) != codeDenied {
		t.Fatalf("worktree mismatch: %v", err)
	}
	s1.Path = a1.Worktree
	// Session adopted by a different agent.
	s1.ManagedAgent = a2.ID
	if _, err := e.svc.List(ListRequest{AgentID: a1.ID}); codeOf(err) != codeDenied {
		t.Fatalf("foreign managed agent: %v", err)
	}
	s1.ManagedAgent = a1.ID
	// Session removed.
	delete(e.sessions, "s1")
	if _, err := e.svc.List(ListRequest{AgentID: a1.ID}); codeOf(err) != codeDenied {
		t.Fatalf("removed session: %v", err)
	}
}

func TestCapabilitiesAreSeparate(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.register(s, "One", "one.md", []byte("one"))
	names := func() map[string]bool {
		m := map[string]bool{}
		for _, tl := range e.svc.Tools() {
			m[tl.Name] = true
		}
		return m
	}
	e.svc.Cfg.Read, e.svc.Cfg.Upload = false, false
	if len(names()) != 0 || e.svc.Instructions() != "" {
		t.Fatal("no tools when both capabilities are off")
	}
	if _, handled := e.svc.CallTool(ToolList, json.RawMessage(`{"agent_id":"`+a.ID+`"}`)); handled {
		t.Fatal("disabled tool must not be handled")
	}
	e.svc.Cfg.Upload = true
	if n := names(); !n[ToolUpload] || n[ToolList] || n[ToolRead] {
		t.Fatalf("upload-only: %v", n)
	}
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatal("read must be denied when only upload is enabled")
	}
	e.svc.Cfg.Read, e.svc.Cfg.Upload = true, false
	if n := names(); n[ToolUpload] || !n[ToolList] || !n[ToolRead] {
		t.Fatalf("read-only: %v", n)
	}
	if _, err := e.svc.Upload(uploadReq(a.ID, "k", "n.txt", "text/plain", []byte("hi"), 0, true)); codeOf(err) != codeDenied {
		t.Fatal("upload must be denied when only read is enabled")
	}
}

// --- traversal / symlinks / non-registered files ------------------------------

func TestOnlyRegisteredFilesAndNoSymlinkEscape(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	secret := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(secret, []byte("TOP SECRET"), 0o600)
	// A file exists in the worktree but is not registered.
	_ = os.WriteFile(filepath.Join(s.Path, "notes.md"), []byte("unregistered"), 0o644)
	reg := e.register(s, "Report", "docs/r.md", []byte("registered"))
	art := filepath.Join(s.Path, ".artifacts")

	// Hand-edit the manifest to add entries pointing outside / via symlinks.
	m, _ := artifactpkg.LoadManifest(s)
	add := func(id, file string) {
		m.Artifacts = append(m.Artifacts, artifactpkg.Artifact{ID: id, Type: "document", Title: id, File: file, Created: e.now})
	}
	_ = os.Symlink(secret, filepath.Join(art, "leaf-link.md"))
	add("leaf-link", "leaf-link.md")
	_ = os.Symlink(filepath.Dir(secret), filepath.Join(art, "dirlink"))
	add("dir-link", "dirlink/secret.txt")
	_ = os.Link(secret, filepath.Join(art, "hard.md"))
	add("hard-link", "hard.md")
	if err := artifactpkg.SaveManifest(s, m); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(artifactpkg.ManifestPath(s))
	items := e.list(a.ID)
	for _, it := range items {
		if it.ManifestID == reg.ID {
			if !it.Available {
				t.Fatalf("registered file should be available: %+v", it)
			}
			continue
		}
		if it.Available || it.Checksums != nil {
			t.Fatalf("%s must be unavailable: %+v", it.ManifestID, it)
		}
		if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID}); codeOf(err) != codeUnavailable {
			t.Fatalf("%s read: %v", it.ManifestID, err)
		}
		if strings.Contains(it.Unavailable, secret) || strings.Contains(it.Unavailable, s.Path) {
			t.Fatal("error leaks a path")
		}
	}
	// A hand-edited traversal entry makes the manifest invalid: nothing is
	// served from it (fail closed), and the error names no path.
	bad := strings.Replace(string(raw), `"file": "hard.md"`, `"file": "../../secret.txt"`, 1)
	_ = os.WriteFile(artifactpkg.ManifestPath(s), []byte(bad), 0o644)
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeUnavailable || strings.Contains(err.Error(), "secret") {
		t.Fatalf("traversal manifest: %v", err)
	}
	if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: opaqueID(a.ID, "s1", "hard-link")}); codeOf(err) != codeUnavailable {
		t.Fatalf("traversal manifest read: %v", err)
	}
	_ = os.WriteFile(artifactpkg.ManifestPath(s), raw, 0o644)
	// .artifacts itself replaced by a symlink to another worktree.
	other := filepath.Join(t.TempDir(), "other")
	_ = os.MkdirAll(filepath.Join(other, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(other, "docs", "r.md"), []byte("other worktree"), 0o644)
	_ = os.WriteFile(filepath.Join(other, "manifest.json"), raw, 0o644)
	_ = os.Rename(art, art+".moved")
	_ = os.Symlink(other, art)
	if _, _, err := openUnderArtifacts(s.Path, "docs/r.md"); codeOf(err) != codeUnavailable {
		t.Fatalf("symlinked .artifacts must not be followed: %v", err)
	}
	// Direct path validation.
	for _, p := range []string{"../x", "/etc/passwd", "a/../../x", "", "."} {
		if _, _, err := openUnderArtifacts(s.Path, p); err == nil {
			t.Fatalf("path %q should be rejected", p)
		}
	}
	// Malformed ids.
	for _, id := range []string{"", "dxa_", "../manifest.json", reg.ID, "dxa_" + strings.Repeat("z", 24)} {
		if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: id}); err == nil {
			t.Fatalf("id %q should not resolve", id)
		}
	}
}

func TestFIFOIsNotOpenedOrBlocking(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.register(s, "Report", "r.md", []byte("x"))
	p := filepath.Join(s.Path, ".artifacts", "r.md")
	_ = os.Remove(p)
	if err := mkfifo(p); err != nil {
		t.Skip("mkfifo unavailable")
	}
	done := make(chan struct{})
	go func() { e.list(a.ID); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("listing blocked on a FIFO")
	}
}

func TestOversizedArtifactIsNotRead(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.register(s, "Big", "big.log", []byte("x"))
	f, _ := os.OpenFile(filepath.Join(s.Path, ".artifacts", "big.log"), os.O_WRONLY, 0)
	_ = f.Truncate(MaxReadableBytes + 1)
	_ = f.Close()
	it := e.list(a.ID)[0]
	if it.Available {
		t.Fatal("oversized artifact must not be readable")
	}
	if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID}); codeOf(err) != codeTooLarge {
		t.Fatalf("oversized read: %v", err)
	}
}

// --- uploads ---------------------------------------------------------------------

func uploadReq(agent, key, name, mt string, data []byte, off int64, chunkAll bool) UploadRequest {
	r := UploadRequest{AgentID: agent, IdempotencyKey: key, Filename: name, MIMEType: mt, Size: int64(len(data)), SHA256: shaHex(data), Offset: off}
	if chunkAll {
		end := off + MaxUploadChunk
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		b := base64.StdEncoding.EncodeToString(data[off:end])
		r.DataBase64 = &b
	}
	return r
}

func (e *env) uploadAll(agent, key, name, mt string, data []byte) map[string]any {
	e.t.Helper()
	var out map[string]any
	for off := int64(0); off < int64(len(data)); off += MaxUploadChunk {
		var err error
		out, err = e.svc.Upload(uploadReq(agent, key, name, mt, data, off, true))
		if err != nil {
			e.t.Fatalf("upload at %d: %v", off, err)
		}
	}
	return out
}

func TestUploadRoundTripAndLocalResolution(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	ref := syntheticPNG(t, 120, 90)
	if len(ref) <= MaxUploadChunk {
		ref = append(ref, bytes.Repeat([]byte{0}, 0)...)
	}
	res := e.uploadAll(a.ID, "ref-1", "mockup.png", "image/png", ref)
	if res["state"] != "complete" {
		t.Fatalf("upload: %v", res)
	}
	info := res["artifact"].(ArtifactInfo)
	local := res["local"].(map[string]any)
	if !info.Attachment || info.Checksums["sha256"] != shaHex(ref) || info.Size != int64(len(ref)) {
		t.Fatalf("attachment info: %+v", info)
	}
	// Local code resolves the manifest id exactly as the Pi agent would.
	m, _ := artifactpkg.LoadManifest(s)
	reg, _ := artifactpkg.Find(m, local["manifest_id"].(string))
	if reg == nil || reg.Folder != AttachmentsFolder || !strings.HasPrefix(reg.File, "attachments/") || len(reg.Assets) != 0 {
		t.Fatalf("manifest entry: %+v", reg)
	}
	abs, err := artifactpkg.SecureExistingPath(artifactpkg.DirForSession(s), reg.File)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(abs)
	if !bytes.Equal(got, ref) || local["path"] != ".artifacts/"+reg.File {
		t.Fatal("local read-back differs")
	}
	// And remotely, through list/read.
	if back, _ := e.readAll(a.ID, info.ID, info.Version, "base64", MaxReadChunk); !bytes.Equal(back, ref) {
		t.Fatal("remote read-back differs")
	}
	// Staging is cleaned up and was never inside the worktree.
	if _, err := os.Stat(filepath.Join(e.svc.uploadsDir(a.ID), keyHash(a.ID, "ref-1")+".part")); !os.IsNotExist(err) {
		t.Fatal("staged bytes should be removed after completion")
	}
}

func TestUploadDuplicatesAndIdempotency(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	data := []byte(strings.Repeat("line of reference text\n", 1000)) // ~23 KB, 3 chunks
	r1 := e.uploadAll(a.ID, "k1", "notes.txt", "text/plain", data)
	// Retrying the final call and a middle chunk after completion: same artifact.
	r2, err := e.svc.Upload(uploadReq(a.ID, "k1", "notes.txt", "text/plain", data, MaxUploadChunk, true))
	if err != nil || r2["replayed"] != true || r2["artifact"].(ArtifactInfo).ID != r1["artifact"].(ArtifactInfo).ID {
		t.Fatalf("replay: %v %v", err, r2)
	}
	// Same key, different file: conflict.
	other := append([]byte(nil), data...)
	other[0] = 'L'
	if _, err := e.svc.Upload(uploadReq(a.ID, "k1", "notes.txt", "text/plain", other, 0, true)); codeOf(err) != codeConflict {
		t.Fatalf("key reuse: %v", err)
	}
	// Same file name under a new key: a second attachment, never an overwrite.
	r3 := e.uploadAll(a.ID, "k2", "notes.txt", "text/plain", other)
	m, _ := artifactpkg.LoadManifest(s)
	a1, _ := artifactpkg.Find(m, r1["local"].(map[string]any)["manifest_id"].(string))
	a3, _ := artifactpkg.Find(m, r3["local"].(map[string]any)["manifest_id"].(string))
	if a1 == nil || a3 == nil || a1.File == a3.File {
		t.Fatalf("second upload must get a distinct file: %v %v", a1, a3)
	}
	b1, _ := os.ReadFile(filepath.Join(s.Path, ".artifacts", a1.File))
	if !bytes.Equal(b1, data) {
		t.Fatal("first attachment was modified")
	}
	// A retried chunk (same bytes at an earlier offset) mid-upload is acknowledged.
	big := bytes.Repeat([]byte("0123456789"), 3000)
	if _, err := e.svc.Upload(uploadReq(a.ID, "k3", "big.txt", "text/plain", big, 0, true)); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.Upload(uploadReq(a.ID, "k3", "big.txt", "text/plain", big, 0, true))
	if err != nil || p["next_offset"] != int64(MaxUploadChunk) {
		t.Fatalf("retried chunk: %v %v", err, p)
	}
}

func TestUploadInterruptedResumeAndOffsetChecks(t *testing.T) {
	e := newEnv(t)
	a, _ := e.agent("s1", "proj")
	data := bytes.Repeat([]byte("abcdefgh"), 3000) // 24000 bytes
	if _, err := e.svc.Upload(uploadReq(a.ID, "r", "d.txt", "text/plain", data, 0, true)); err != nil {
		t.Fatal(err)
	}
	// Client lost track; probe progress without data.
	probe := uploadReq(a.ID, "r", "d.txt", "text/plain", data, 0, false)
	p, err := e.svc.Upload(probe)
	if err != nil || p["next_offset"] != int64(MaxUploadChunk) || p["state"] != "receiving" {
		t.Fatalf("probe: %v %v", err, p)
	}
	// A skipped-ahead chunk is refused with the expected offset.
	_, err = e.svc.Upload(uploadReq(a.ID, "r", "d.txt", "text/plain", data, 2*MaxUploadChunk, true))
	if codeOf(err) != codeOffset || asError(err).Extra["next_offset"] != int64(MaxUploadChunk) {
		t.Fatalf("gap: %v", err)
	}
	// A torn tail (crash mid-write) is truncated on the next chunk.
	part := filepath.Join(e.svc.uploadsDir(a.ID), keyHash(a.ID, "r")+".part")
	f, _ := os.OpenFile(part, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.Write([]byte("GARBAGE"))
	_ = f.Close()
	for off := int64(MaxUploadChunk); off < int64(len(data)); off += MaxUploadChunk {
		if _, err := e.svc.Upload(uploadReq(a.ID, "r", "d.txt", "text/plain", data, off, true)); err != nil {
			t.Fatalf("resume at %d: %v", off, err)
		}
	}
	res, err := e.svc.Upload(probe)
	if err != nil || res["state"] != "complete" || res["artifact"].(ArtifactInfo).Checksums["sha256"] != shaHex(data) {
		t.Fatalf("after resume: %v %v", err, res)
	}
	// Unknown key with no data.
	if _, err := e.svc.Upload(uploadReq(a.ID, "nope", "d.txt", "text/plain", data, 0, false)); codeOf(err) != codeNotFound {
		t.Fatalf("unknown probe: %v", err)
	}
}

func TestUploadCrashAfterRegistrationIsAdoptedNotDuplicated(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	data := []byte("reference")
	if _, err := e.svc.Upload(uploadReq(a.ID, "c", "c.txt", "text/plain", data, 0, true)); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after artifact.Add but before completion was recorded.
	recPath := filepath.Join(e.svc.uploadsDir(a.ID), keyHash(a.ID, "c")+".json")
	rec, _ := readRecord(recPath)
	rec.State, rec.ManifestID = "receiving", ""
	_ = writeRecord(recPath, rec)
	_ = os.WriteFile(strings.TrimSuffix(recPath, ".json")+".part", data, 0o600)
	res, err := e.svc.Upload(uploadReq(a.ID, "c", "c.txt", "text/plain", data, 0, true))
	if err != nil || res["state"] != "complete" {
		t.Fatalf("retry after crash: %v %v", err, res)
	}
	m, _ := artifactpkg.LoadManifest(s)
	if len(m.Artifacts) != 1 {
		t.Fatalf("attachment registered twice: %d", len(m.Artifacts))
	}
}

func TestUploadValidationAndLimits(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	png := syntheticPNG(t, 8, 8)
	cases := []struct {
		name string
		req  UploadRequest
		code string
	}{
		{"traversal filename", uploadReq(a.ID, "a", "../x.txt", "text/plain", []byte("x"), 0, true), codeInvalid},
		{"dir filename", uploadReq(a.ID, "a", "sub/x.txt", "text/plain", []byte("x"), 0, true), codeInvalid},
		{"hidden filename", uploadReq(a.ID, "a", ".env", "text/plain", []byte("x"), 0, true), codeInvalid},
		{"malformed mime", uploadReq(a.ID, "a", "x.txt", "text/plain; charset", []byte("x"), 0, true), codeInvalid},
		{"mime params", uploadReq(a.ID, "a", "x.png", "image/png; foo=bar", png, 0, true), codeInvalid},
		{"html", uploadReq(a.ID, "a", "x.html", "text/html", []byte("<script>"), 0, true), codeUnsupportedType},
		{"svg", uploadReq(a.ID, "a", "x.svg", "image/svg+xml", []byte("<svg/>"), 0, true), codeUnsupportedType},
		{"script", uploadReq(a.ID, "a", "x.sh", "application/x-sh", []byte("#!/bin/sh"), 0, true), codeUnsupportedType},
		{"ext mismatch", uploadReq(a.ID, "a", "x.png", "text/plain", []byte("x"), 0, true), codeUnsupportedType},
		{"no key", uploadReq(a.ID, "", "x.txt", "text/plain", []byte("x"), 0, true), codeInvalid},
	}
	for _, c := range cases {
		if _, err := e.svc.Upload(c.req); codeOf(err) != c.code {
			t.Errorf("%s: want %s got %v", c.name, c.code, err)
		}
	}
	// Declared size above the limit.
	e.svc.Cfg.MaxUploadBytes = 1000
	big := uploadReq(a.ID, "big", "x.txt", "text/plain", bytes.Repeat([]byte("x"), 1001), 0, true)
	if _, err := e.svc.Upload(big); codeOf(err) != codeTooLarge {
		t.Errorf("oversize: %v", err)
	}
	e.svc.Cfg.MaxUploadBytes = DefaultMaxUploadBytes
	// Oversized chunk.
	r := uploadReq(a.ID, "oc", "x.txt", "text/plain", bytes.Repeat([]byte("x"), 20000), 0, false)
	b := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), MaxUploadChunk+1))
	r.DataBase64 = &b
	if _, err := e.svc.Upload(r); codeOf(err) != codeTooLarge {
		t.Errorf("oversize chunk: %v", err)
	}
	// Bad base64.
	r = uploadReq(a.ID, "bb", "x.txt", "text/plain", []byte("x"), 0, false)
	bad := "!!!"
	r.DataBase64 = &bad
	if _, err := e.svc.Upload(r); codeOf(err) != codeInvalid {
		t.Errorf("bad base64: %v", err)
	}
	// Checksum mismatch: bytes differ from the declared sha256.
	r = uploadReq(a.ID, "sum", "x.txt", "text/plain", []byte("hello"), 0, true)
	r.SHA256 = shaHex([]byte("HELLO"))
	if _, err := e.svc.Upload(r); codeOf(err) != codeIntegrity {
		t.Errorf("checksum: %v", err)
	}
	// Declared PNG whose bytes are not a PNG.
	fake := []byte("not a png at all")
	if _, err := e.svc.Upload(uploadReq(a.ID, "fake", "x.png", "image/png", fake, 0, true)); codeOf(err) != codeUnsupportedType {
		t.Errorf("magic mismatch: %v", err)
	}
	// Text that is not UTF-8 / JSON that does not parse.
	if _, err := e.svc.Upload(uploadReq(a.ID, "bin", "x.txt", "text/plain", []byte{0xff, 0xfe, 0}, 0, true)); codeOf(err) != codeUnsupportedType {
		t.Errorf("binary as text: %v", err)
	}
	if _, err := e.svc.Upload(uploadReq(a.ID, "js", "x.json", "application/json", []byte("{nope"), 0, true)); codeOf(err) != codeUnsupportedType {
		t.Errorf("bad json: %v", err)
	}
	// None of the failures registered anything or wrote into the worktree.
	m, _ := artifactpkg.LoadManifest(s)
	if len(m.Artifacts) != 0 {
		t.Fatalf("failed uploads registered %d artifacts", len(m.Artifacts))
	}
	// Pending-upload cap.
	for i := 0; i < MaxPendingUploads; i++ {
		d := bytes.Repeat([]byte("p"), MaxUploadChunk+1)
		if _, err := e.svc.Upload(uploadReq(a.ID, "p"+string(rune('a'+i)), "p.txt", "text/plain", d, 0, true)); err != nil {
			t.Fatal(err)
		}
	}
	d := bytes.Repeat([]byte("q"), MaxUploadChunk+1)
	if _, err := e.svc.Upload(uploadReq(a.ID, "over", "q.txt", "text/plain", d, 0, true)); codeOf(err) != codeLimit {
		t.Errorf("pending cap: %v", err)
	}
	// Stale incomplete uploads expire after UploadExpiry and free the slots.
	e.now = e.now.Add(UploadExpiry + time.Minute)
	if _, err := e.svc.Upload(uploadReq(a.ID, "over", "q.txt", "text/plain", d, 0, true)); err != nil {
		t.Errorf("after expiry: %v", err)
	}
}

func TestUploadNeverOverwritesRepositoryOrArtifactFiles(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	// Pre-existing files at the would-be destination and in the repo.
	_ = os.MkdirAll(filepath.Join(s.Path, ".artifacts", "attachments"), 0o755)
	_ = os.WriteFile(filepath.Join(s.Path, ".artifacts", "attachments", "README.md"), []byte("keep me"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Path, "README.md"), []byte("repo file"), 0o644)
	e.uploadAll(a.ID, "u", "README.md", "text/markdown", []byte("# uploaded"))
	if b, _ := os.ReadFile(filepath.Join(s.Path, ".artifacts", "attachments", "README.md")); string(b) != "keep me" {
		t.Fatal("existing artifact file was overwritten")
	}
	if b, _ := os.ReadFile(filepath.Join(s.Path, "README.md")); string(b) != "repo file" {
		t.Fatal("repository file was touched")
	}
	// attachments/ replaced by a symlink to the repo root: upload is refused.
	_ = os.RemoveAll(filepath.Join(s.Path, ".artifacts", "attachments"))
	_ = os.Symlink(s.Path, filepath.Join(s.Path, ".artifacts", "attachments"))
	if _, err := e.svc.Upload(uploadReq(a.ID, "v", "x.md", "text/markdown", []byte("# x"), 0, true)); err == nil {
		t.Fatal("upload through a symlinked attachments dir must fail")
	}
	if _, err := os.Stat(filepath.Join(s.Path, "x.md")); !os.IsNotExist(err) {
		t.Fatal("upload escaped into the repository")
	}
}

func TestUploadedMarkdownReferencesAreNotRecordedAsAssets(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	// A pre-existing artifact the uploaded Markdown tries to claim as an asset.
	keep := e.register(s, "Keep", "keep.png", syntheticPNG(t, 4, 4))
	res := e.uploadAll(a.ID, "m", "evil.md", "text/markdown", []byte("![x](../keep.png)\n"))
	m, _ := artifactpkg.LoadManifest(s)
	up, _ := artifactpkg.Find(m, res["local"].(map[string]any)["manifest_id"].(string))
	if len(up.Assets) != 0 {
		t.Fatalf("untrusted upload recorded assets: %v", up.Assets)
	}
	// Removing the attachment must not delete the other artifact.
	if _, err := artifactpkg.Remove(s, up.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Path, ".artifacts", keep.File)); err != nil {
		t.Fatal("removing an upload deleted another artifact")
	}
}

// --- task linkage ----------------------------------------------------------------

func TestTaskArtifacts(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	delivered := e.now
	e.now = delivered.Add(-time.Hour)
	e.register(s, "Before", "before.md", []byte("old"))
	e.now = delivered.Add(time.Minute)
	e.register(s, "During", "during.md", []byte("new"))
	e.uploadAll(a.ID, "x", "ref.txt", "text/plain", []byte("ref"))
	finished := delivered.Add(2 * time.Minute)
	e.now = finished.Add(time.Hour)
	e.register(s, "After", "after.md", []byte("later"))
	tv := &piagent.TaskView{TaskID: "pt_0000000000000000", DeliveredAt: &delivered, FinishedAt: &finished}
	arts := e.svc.TaskArtifacts(a, tv)
	if len(arts) != 1 || arts[0]["title"] != "During" {
		t.Fatalf("task artifacts: %v", arts)
	}
	// Not delivered, or read disabled: nothing.
	if e.svc.TaskArtifacts(a, &piagent.TaskView{}) != nil {
		t.Fatal("undelivered task should have no artifacts")
	}
	e.svc.Cfg.Read = false
	if e.svc.TaskArtifacts(a, tv) != nil {
		t.Fatal("read disabled must not expose artifacts")
	}
}
