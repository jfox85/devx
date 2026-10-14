package artifactbridge

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/piagent"
)

// --- (1) authorization: registration alone grants nothing --------------------

func TestRegisteredButUnexposedSessionIsDenied(t *testing.T) {
	e := newEnv(t)
	a1, s1 := e.agent("exposed", "proj")
	a2, s2 := e.agent("registered-only", "proj")
	e.register(s1, "One", "one.md", []byte("one"))
	e.register(s2, "Two", "two.md", []byte("two"))
	e.pol.Sessions = []string{"exposed"}

	// a2 is a valid, live, managed agent in an allowlisted project, but its
	// session was not exposed by the owner: every operation is denied.
	if _, err := e.svc.List(ListRequest{AgentID: a2.ID}); codeOf(err) != codeDenied {
		t.Fatalf("list: %v", err)
	}
	m2, _ := artifactpkg.LoadManifest(s2)
	id2 := opaqueID(a2.ID, "registered-only", m2.Artifacts[0].ID)
	if _, err := e.svc.Read(ReadRequest{AgentID: a2.ID, ArtifactID: id2}); codeOf(err) != codeDenied {
		t.Fatalf("read: %v", err)
	}
	if _, err := e.svc.Upload(uploadReq(a2.ID, "k", "n.txt", "text/plain", []byte("x"), 0, true)); codeOf(err) != codeDenied {
		t.Fatalf("upload: %v", err)
	}
	// Using the exposed agent's id with the other session's artifact id fails.
	if _, err := e.svc.Read(ReadRequest{AgentID: a1.ID, ArtifactID: id2}); codeOf(err) != codeNotFound {
		t.Fatalf("cross-agent id: %v", err)
	}
	// No exposure list at all: nothing is served.
	e.pol.Sessions = nil
	if _, err := e.svc.List(ListRequest{AgentID: a1.ID}); codeOf(err) != codeDenied {
		t.Fatalf("empty exposure list: %v", err)
	}
}

func TestAccessReductionTakesEffectOnNextCall(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.register(s, "One", "one.md", bytes.Repeat([]byte("x"), 40000))
	it := e.list(a.ID)[0]
	r, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		name   string
		reduce func()
		undo   func()
	}{
		{"session unexposed", func() { e.pol.Sessions = nil }, func() { e.pol.Sessions = []string{"s1"} }},
		{"read capability off", func() { e.pol.Read = false }, func() { e.pol.Read = true }},
		{"project de-allowlisted", func() { e.pol.AllowedProjects = []string{"other"} }, func() { e.pol.AllowedProjects = []string{"proj", "other"} }},
		{"session removed", func() { delete(e.sessions, "s1") }, func() { e.sessions["s1"] = s }},
	}
	for _, st := range steps {
		st.reduce()
		// Mid-transfer continuation is refused, not just new transfers.
		if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Version: it.Version, Offset: r.NextOffset}); codeOf(err) != codeDenied {
			t.Fatalf("%s: continuation not denied: %v", st.name, err)
		}
		st.undo()
	}
	// A second live agent claiming the same session makes it ambiguous.
	dup := &piagent.Agent{ID: "pa_dddddddddddd", DevxSession: "s1", Project: "proj", Worktree: a.Worktree, PiSessionID: "y", CreatedAt: e.now}
	_ = e.store.WithAgentLock(dup.ID, func() error { return e.store.SaveAgent(dup) })
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("duplicate claimant: %v", err)
	}
	if _, err := e.svc.List(ListRequest{AgentID: dup.ID}); codeOf(err) != codeDenied {
		t.Fatalf("duplicate claimant (other side): %v", err)
	}
}

// --- (2) path safety: manifest symlink, parent swap race, publish ----------

func TestSymlinkedManifestIsNotFollowed(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	_, s2 := e.agent("s2", "proj")
	e.register(s2, "Foreign", "f.md", []byte("foreign"))
	e.register(s, "Mine", "m.md", []byte("mine"))
	mp := filepath.Join(s.Path, ".artifacts", "manifest.json")
	// Point s1's manifest at s2's manifest (relative entries would then
	// resolve inside s1's .artifacts, but the manifest itself is foreign).
	_ = os.Remove(mp)
	_ = os.Symlink(filepath.Join(s2.Path, ".artifacts", "manifest.json"), mp)
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeUnavailable {
		t.Fatalf("symlinked manifest must be refused: %v", err)
	}
}

func TestParentDirectorySwapRaceNeverEscapes(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	content := bytes.Repeat([]byte("inside "), 2000)
	e.register(s, "Doc", "docs/d.md", content)
	it := e.list(a.ID)[0]
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "d.md"), []byte("OUTSIDE-SECRET"), 0o644)
	docs := filepath.Join(s.Path, ".artifacts", "docs")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // flip docs/ between the real dir and a symlink to outside
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				_ = os.Remove(docs)
				_ = os.Rename(docs+".real", docs)
				return
			default:
			}
			_ = os.Rename(docs, docs+".real")
			_ = os.Symlink(outside, docs)
			_ = os.Remove(docs)
			_ = os.Rename(docs+".real", docs)
		}
	}()
	okReads := 0
	for i := 0; i < 2000; i++ {
		r, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Encoding: "text"})
		if err != nil {
			continue
		}
		if strings.Contains(*r.Text, "OUTSIDE") {
			close(stop)
			wg.Wait()
			t.Fatal("read followed a swapped-in symlink")
		}
		okReads++
	}
	close(stop)
	wg.Wait()
	t.Logf("swap race: %d/2000 reads succeeded, none escaped", okReads)
}

func TestPublishNeverOverwritesEvenUnderRace(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	att := filepath.Join(s.Path, ".artifacts", "attachments")
	_ = os.MkdirAll(att, 0o755)
	// Unregistered pre-existing files (and a symlink) at candidate names.
	_ = os.WriteFile(filepath.Join(att, "ref.txt"), []byte("keep 1"), 0o644)
	_ = os.Symlink("/etc/hosts", filepath.Join(att, "ref-2.txt"))
	res := e.uploadAll(a.ID, "k", "ref.txt", "text/plain", []byte("new bytes"))
	file := res["local"].(map[string]any)["path"].(string)
	if file != ".artifacts/attachments/ref-3.txt" {
		t.Fatalf("published as %s", file)
	}
	if b, _ := os.ReadFile(filepath.Join(att, "ref.txt")); string(b) != "keep 1" {
		t.Fatal("existing file overwritten")
	}
	if l, err := os.Readlink(filepath.Join(att, "ref-2.txt")); err != nil || l != "/etc/hosts" {
		t.Fatal("existing symlink replaced")
	}
	// No temp files left behind; published file has a single link.
	ents, _ := os.ReadDir(att)
	for _, en := range ents {
		if strings.HasPrefix(en.Name(), ".upload-") {
			t.Fatalf("leftover temp %s", en.Name())
		}
	}
	if back, _ := e.readAll(a.ID, res["artifact"].(ArtifactInfo).ID, "", "text", MaxReadChunk); string(back) != "new bytes" {
		t.Fatal("published attachment not readable (link count?)")
	}
}

// --- (4) concurrent idempotency / lost response / changed bytes -----------

func TestConcurrentSameKeyUploadsRegisterOnce(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	data := bytes.Repeat([]byte("concurrent reference\n"), 400) // 8400 B: 2 chunks
	var wg sync.WaitGroup
	results := make(chan map[string]any, 40)
	errs := make(chan error, 40)
	for w := 0; w < 20; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for off := int64(0); off < int64(len(data)); off += MaxUploadChunk {
				r, err := e.svc.Upload(uploadReq(a.ID, "same", "c.txt", "text/plain", data, off, true))
				if err != nil {
					if codeOf(err) != codeOffset { // racing past another worker is expected
						errs <- err
					}
					continue
				}
				results <- r
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatalf("unexpected error: %v", err)
	}
	ids := map[string]bool{}
	for r := range results {
		if r["state"] == "complete" {
			ids[r["artifact"].(ArtifactInfo).ID] = true
		}
	}
	m, _ := artifactpkg.LoadManifest(s)
	if len(m.Artifacts) != 1 || len(ids) != 1 {
		t.Fatalf("want exactly one artifact, manifest=%d distinct completions=%d", len(m.Artifacts), len(ids))
	}
	ents, _ := os.ReadDir(filepath.Join(s.Path, ".artifacts", "attachments"))
	if len(ents) != 1 {
		t.Fatalf("want one file in attachments, got %d", len(ents))
	}
}

func TestLostResponseAfterRegistrationAndChangedBytes(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	data := []byte("v1 reference")
	first := e.uploadAll(a.ID, "k", "r.txt", "text/plain", data)
	// Client never saw the response; it retries the final chunk, then probes.
	again, err := e.svc.Upload(uploadReq(a.ID, "k", "r.txt", "text/plain", data, 0, true))
	if err != nil || again["replayed"] != true || again["artifact"].(ArtifactInfo).ID != first["artifact"].(ArtifactInfo).ID {
		t.Fatalf("replay: %v %v", err, again)
	}
	// Same key, same name and size, different bytes -> conflict, no write.
	changed := []byte("v2 reference")
	if _, err := e.svc.Upload(uploadReq(a.ID, "k", "r.txt", "text/plain", changed, 0, true)); codeOf(err) != codeConflict {
		t.Fatalf("changed bytes: %v", err)
	}
	// Completion record lost after registration (crash before markComplete):
	// the retry adopts the registered file instead of publishing a second.
	recPath := filepath.Join(e.svc.uploadsDir(a.ID), keyHash(a.ID, "k2")+".json")
	e.uploadAll(a.ID, "k2", "r.txt", "text/plain", changed)
	rec, _ := readRecord(recPath)
	rec.State, rec.ManifestID = "receiving", ""
	_ = writeRecord(recPath, rec)
	_ = os.WriteFile(strings.TrimSuffix(recPath, ".json")+".part", changed, 0o600)
	if r, err := e.svc.Upload(uploadReq(a.ID, "k2", "r.txt", "text/plain", changed, 0, true)); err != nil || r["state"] != "complete" {
		t.Fatalf("adopt after lost record: %v %v", err, r)
	}
	// Registered file tampered with, then record lost: refused, not adopted.
	m, _ := artifactpkg.LoadManifest(s)
	k2, _ := artifactpkg.Find(m, attachmentManifestID(keyHash(a.ID, "k2")))
	_ = os.WriteFile(filepath.Join(s.Path, ".artifacts", k2.File), []byte("tampered!!!!"), 0o644)
	rec, _ = readRecord(recPath)
	rec.State, rec.ManifestID = "receiving", ""
	_ = writeRecord(recPath, rec)
	_ = os.WriteFile(strings.TrimSuffix(recPath, ".json")+".part", changed, 0o600)
	if _, err := e.svc.Upload(uploadReq(a.ID, "k2", "r.txt", "text/plain", changed, 0, true)); codeOf(err) != codeConflict {
		t.Fatalf("tampered adoption: %v", err)
	}
	m, _ = artifactpkg.LoadManifest(s)
	if len(m.Artifacts) != 2 {
		t.Fatalf("want 2 artifacts, got %d", len(m.Artifacts))
	}
}

// --- (3)/(5) envelope accounting and large transfers ----------------------

func noisyPNG(t *testing.T, target int) []byte {
	t.Helper()
	side := 64
	for {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		noise := make([]byte, side*side*3)
		_, _ = rand.Read(noise)
		for i := 0; i < side*side; i++ {
			img.Set(i%side, i/side, color.RGBA{noise[3*i], noise[3*i+1], noise[3*i+2], 255})
		}
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		if b.Len() >= target {
			return b.Bytes()
		}
		side += 32
	}
}

// noisyPNGUnder returns the largest square random-pixel PNG not above max.
func noisyPNGUnder(t *testing.T, max int) []byte {
	t.Helper()
	var best []byte
	for side := 16; ; side += 4 {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		_, _ = rand.Read(img.Pix)
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		if b.Len() > max {
			return best
		}
		best = b.Bytes()
	}
}

type transferStats struct {
	calls, maxLine int
	elapsed        time.Duration
}

// downloadViaMCP reads an artifact through CallTool the way a remote client
// would, measuring every encoded JSON-RPC response line.
func downloadViaMCP(t *testing.T, e *env, agent string, it ArtifactInfo) ([]byte, transferStats) {
	t.Helper()
	var out []byte
	var st transferStats
	start := time.Now()
	for off := int64(0); ; {
		args, _ := json.Marshal(map[string]any{"agent_id": agent, "artifact_id": it.ID, "version": it.Version, "offset": off, "encoding": "base64"})
		res, _ := e.svc.CallTool(ToolRead, args)
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 123456, "result": res})
		st.calls++
		if len(line) > st.maxLine {
			st.maxLine = len(line)
		}
		if res["isError"] == true {
			t.Fatalf("read error at %d: %v", off, res["structuredContent"])
		}
		sc := res["structuredContent"].(*ReadResult)
		b, _ := base64.StdEncoding.DecodeString(*sc.Base64)
		out = append(out, b...)
		off = sc.NextOffset
		if sc.EOF {
			break
		}
	}
	st.elapsed = time.Since(start)
	return out, st
}

func TestLargeScreenshotAndBinaryChunkedRetrieval(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	shot := noisyPNG(t, 250<<10)
	bin := make([]byte, 1<<20)
	_, _ = rand.Read(bin)
	e.register(s, "Big screenshot", "big.png", shot)
	e.register(s, "Binary", "blob.bin", bin)
	for _, it := range e.list(a.ID) {
		want := shot
		if it.Title == "Binary" {
			want = bin
		}
		got, st := downloadViaMCP(t, e, a.ID, it)
		if !bytes.Equal(got, want) || shaHex(got) != it.Checksums["sha256"] {
			t.Fatalf("%s: reassembled bytes differ", it.Title)
		}
		wantCalls := (len(want) + MaxReadChunk - 1) / MaxReadChunk
		if st.calls != wantCalls || st.maxLine > 60000 {
			t.Fatalf("%s: calls=%d (want %d) maxLine=%d", it.Title, st.calls, wantCalls, st.maxLine)
		}
		t.Logf("%s: %d bytes, %d calls, max response line %d bytes, %v local", it.Title, len(want), st.calls, st.maxLine, st.elapsed)
	}
	// auto encoding on a >40 KiB PNG falls back to base64, not image.
	args, _ := json.Marshal(map[string]any{"agent_id": a.ID, "artifact_id": find(e.list(a.ID), "Big screenshot").ID})
	res, _ := e.svc.CallTool(ToolRead, args)
	if res["structuredContent"].(*ReadResult).Encoding != "base64" {
		t.Fatal("large PNG must not be returned as an inline image")
	}
}

func TestEnvelopeAccountingWorstCases(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	// Worst-case JSON escaping for text: control chars escape to \u00XX (6x).
	ctrl := bytes.Repeat([]byte{0x01}, 20000)
	e.register(s, "Ctrl", "ctrl.txt", ctrl)
	// Near-largest inline image (random pixels do not compress).
	img := noisyPNGUnder(t, MaxInlineImageBytes)
	if len(img) < 30<<10 {
		t.Fatalf("fixture image only %d bytes", len(img))
	}
	e.register(s, "Img", "img.png", img)
	// Heavily escaping but valid text: '<' becomes \u003c.
	e.register(s, "Angle", "angle.md", bytes.Repeat([]byte("<"), 20000))
	for _, it := range e.list(a.ID) {
		args, _ := json.Marshal(map[string]any{"agent_id": a.ID, "artifact_id": it.ID})
		res, _ := e.svc.CallTool(ToolRead, args)
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		if len(line) > 64<<10 {
			t.Fatalf("%s: %d-byte response exceeds 64 KiB", it.Title, len(line))
		}
		sc, ok := res["structuredContent"].(*ReadResult)
		switch {
		case !ok:
			t.Fatalf("%s: default read failed: %v", it.Title, res["structuredContent"])
		case sc.Encoding == "image":
			if sc.Length != len(img) {
				t.Fatalf("image read returned %d of %d bytes", sc.Length, len(img))
			}
			if sc.Base64 != nil || sc.Text != nil {
				t.Fatalf("%s: image payload duplicated in structuredContent", it.Title)
			}
			t.Logf("%s: image %d bytes -> response %d bytes", it.Title, sc.Length, len(line))
		default:
			t.Logf("%s: %s %d bytes -> response %d bytes", it.Title, sc.Encoding, sc.Length, len(line))
		}
	}
}

func TestEscapeHeavyTextReassemblesExactly(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	var b bytes.Buffer
	for i := 0; i < 6000; i++ {
		b.WriteString("<&>\x01\"\\\n\u2028é🚀")
	}
	text := b.Bytes()
	e.register(s, "Esc", "esc.txt", text)
	it := e.list(a.ID)[0]
	var got []byte
	calls := 0
	for off := int64(0); ; {
		args, _ := json.Marshal(map[string]any{"agent_id": a.ID, "artifact_id": it.ID, "version": it.Version, "offset": off})
		res, _ := e.svc.CallTool(ToolRead, args)
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		if len(line) > MaxResultBytes {
			t.Fatalf("response %d bytes", len(line))
		}
		sc, ok := res["structuredContent"].(*ReadResult)
		if !ok {
			t.Fatalf("read failed at %d: %v", off, res["structuredContent"])
		}
		if res["content"].([]map[string]any)[0]["text"] != *sc.Text {
			t.Fatal("content and structuredContent text differ")
		}
		got = append(got, []byte(*sc.Text)...)
		calls++
		off = sc.NextOffset
		if sc.EOF {
			break
		}
	}
	if !bytes.Equal(got, text) || shaHex(got) != it.Checksums["sha256"] {
		t.Fatal("escape-heavy text did not reassemble exactly")
	}
	t.Logf("escape-heavy text: %d bytes in %d calls", len(text), calls)
}

func TestHashCacheNotFooledBySameSizeSameMtimeRewrite(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.register(s, "Doc", "d.md", bytes.Repeat([]byte("A"), 50000))
	p := filepath.Join(s.Path, ".artifacts", "d.md")
	// Old enough, with a sub-second mtime, so the hash is cached.
	old := time.Now().Add(-time.Hour).Add(123456789 * time.Nanosecond)
	_ = os.Chtimes(p, old, old)
	it := e.list(a.ID)[0] // populates the cache
	if _, ok := cachedSum(cacheKey(s.Path+"\x00d.md", mustStat(t, p))); !ok {
		t.Fatal("precondition: hash should be cached")
	}
	st, _ := os.Stat(p)
	// Rewrite in place with same size, then restore the old mtime.
	_ = os.WriteFile(p, bytes.Repeat([]byte("B"), 50000), 0o644)
	_ = os.Chtimes(p, st.ModTime(), st.ModTime())
	if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Version: it.Version, Offset: 20000}); codeOf(err) != codeVersionMismatch {
		t.Fatalf("stale cache served old version: %v", err)
	}
	if again := e.list(a.ID)[0]; again.Checksums["sha256"] != shaHex(bytes.Repeat([]byte("B"), 50000)) {
		t.Fatal("list served a stale checksum")
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// Review finding: on whole-second timestamp filesystems a same-size rewrite
// within one second keeps the same key, so such files are never cached; nor
// are files changed in the last cacheMinAge.
func TestHashCacheSkipsCoarseOrRecentTimestamps(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	_ = os.WriteFile(p, []byte("x"), 0o644)
	for name, c := range map[string]struct {
		mtime time.Time
		want  bool
	}{
		"whole-second mtime": {now.Add(-time.Hour).Truncate(time.Second), false},
		"recent mtime":       {now.Add(-500 * time.Millisecond), false},
		"old sub-second":     {now.Add(-time.Hour).Truncate(time.Second).Add(7 * time.Millisecond), true},
	} {
		_ = os.Chtimes(p, c.mtime, c.mtime)
		if got := cacheable(mustStat(t, p), now); got != c.want {
			t.Errorf("%s: cacheable=%v want %v", name, got, c.want)
		}
	}
}

// --- (5) 32 MiB timing ---------------------------------------------------------

func TestMaxSizeHashTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	big := make([]byte, MaxReadableBytes)
	_, _ = rand.Read(big)
	e.register(s, "Max", "max.bin", big)
	start := time.Now()
	it := e.list(a.ID)[0]
	listCold := time.Since(start)
	start = time.Now()
	_ = e.list(a.ID)
	listWarm := time.Since(start)
	start = time.Now()
	if _, err := e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Version: it.Version, Offset: MaxReadableBytes - 100}); err != nil {
		t.Fatal(err)
	}
	read := time.Since(start)
	start = time.Now()
	got, stats := downloadViaMCP(t, e, a.ID, it)
	full := time.Since(start)
	if shaHex(got) != it.Checksums["sha256"] {
		t.Fatal("32 MiB reassembly differs")
	}
	t.Logf("32 MiB: list cold %v, list warm %v, one chunk read %v; full download %d calls in %v (%v/call, local only)",
		listCold, listWarm, read, stats.calls, full, full/time.Duration(stats.calls))
	if read > 10*time.Second {
		t.Fatalf("single chunk read of a max-size file took %v", read)
	}
}
