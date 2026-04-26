package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jfox85/devx/session"
)

func TestGetSessionsReturnsJSON(t *testing.T) {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
}

func TestGetSettingsReturnsArtifactTriggerKey(t *testing.T) {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/settings", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := resp["artifact_trigger_key"]; !ok {
		t.Fatalf("artifact_trigger_key missing from response: %#v", resp)
	}
}

func TestGetHealthReturnsOK(t *testing.T) {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestPaneCaptureTargetUsesExactMatchForSlashSessionNames(t *testing.T) {
	origRun := execTmuxRun
	execTmuxRun = func(args ...string) error { return nil }
	defer func() { execTmuxRun = origRun }()

	target, err := paneCaptureTarget("feature/foo", "2")
	if err != nil {
		t.Fatalf("paneCaptureTarget returned error: %v", err)
	}
	if want := "=feature/foo-web:.2"; target != want {
		t.Fatalf("expected %q, got %q", want, target)
	}
}

func TestPaneCaptureTargetRejectsNonNumericPane(t *testing.T) {
	if _, err := paneCaptureTarget("feature/foo", "abc"); err == nil {
		t.Fatal("expected error for non-numeric pane")
	}
}

func TestPaneCaptureLinesMatchesTmuxHistoryLimit(t *testing.T) {
	if paneCaptureLines != session.DefaultTmuxHistoryLimit {
		t.Fatalf("pane capture lines = %d, want %d", paneCaptureLines, session.DefaultTmuxHistoryLimit)
	}
}

func TestActivePaneReturnsPaneIndex(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return errors.New("no web session") },
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "display-message -t =jf-add-web: -p #{pane_index}"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("2\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/active-pane?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body activePaneResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body.Pane != 2 {
		t.Fatalf("expected pane 2, got %d", body.Pane)
	}
}

func TestPaneContentEndpointsRequireAuth(t *testing.T) {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	handler := authMiddleware("test-secret", mux)

	req := httptest.NewRequest("GET", "/api/pane-content/view?name=jf-add-web", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestPaneContentJSONReturnsCapturedContent(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error {
			if len(args) >= 1 && args[0] == "has-session" {
				return nil
			}
			return nil
		},
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "capture-pane -t =jf-add-web-web: -p -S -50000"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("line 1\nline 2\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/pane-content?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var body paneContentResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body.Content != "line 1\nline 2" {
		t.Fatalf("unexpected content %q", body.Content)
	}
	if body.Target != "=jf-add-web-web:" {
		t.Fatalf("unexpected target %q", body.Target)
	}
}

func TestPaneContentJSONRejectsInvalidPane(t *testing.T) {
	resp := authedRequest(t, "GET", "/api/pane-content?name=jf-add-web&pane=abc", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "pane must be a numeric index") {
		t.Fatalf("unexpected body: %s", resp.Body.String())
	}
}

func TestListWindowsUsesExactTargetForSlashSessionNames(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return errors.New("no web session") },
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "list-windows -t =feature/foo -F #{window_index}\t#{window_name}\t#{window_active}"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("1\teditor\t1\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/windows?name=feature%2Ffoo", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestSwitchWindowUsesExactTargetForSlashSessionNames(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error {
			got := strings.Join(args, " ")
			switch got {
			case "has-session -t =feature/foo-web":
				return errors.New("no web session")
			case "select-window -t =feature/foo:3":
				return nil
			default:
				t.Fatalf("unexpected tmux args: %q", got)
				return nil
			}
		},
		func(args ...string) ([]byte, error) { return nil, errors.New("unexpected output call") })

	resp := authedRequest(t, "POST", "/api/switch-window?name=feature%2Ffoo&window=3", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestRefreshUsesExactTargetForSlashSessionNames(t *testing.T) {
	seenRefresh := false
	seenListClients := false
	seenResize := false
	withStubbedTmux(t,
		func(args ...string) error {
			got := strings.Join(args, " ")
			switch got {
			case "has-session -t =feature/foo-web":
				return errors.New("no web session")
			case "refresh-client -t =feature/foo":
				seenRefresh = true
				return nil
			case "resize-window -t =feature/foo -x 120 -y 42":
				seenResize = true
				return nil
			default:
				t.Fatalf("unexpected tmux run args: %q", got)
				return nil
			}
		},
		func(args ...string) ([]byte, error) {
			got := strings.Join(args, " ")
			if got != "list-clients -t =feature/foo -F #{client_activity} #{client_width} #{client_height}" {
				t.Fatalf("unexpected tmux output args: %q", got)
			}
			seenListClients = true
			return []byte("123 120 42\n"), nil
		})

	resp := authedRequest(t, "POST", "/api/refresh?name=feature%2Ffoo", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
	if !(seenRefresh && seenListClients && seenResize) {
		t.Fatalf("expected refresh, list-clients, and resize-window to run; got refresh=%v list=%v resize=%v", seenRefresh, seenListClients, seenResize)
	}
}

func TestSendLiteralUsesExactTargetForSlashSessionNames(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error {
			got := strings.Join(args, " ")
			switch got {
			case "has-session -t =feature/foo-web":
				return errors.New("no web session")
			case "send-keys -t =feature/foo: -l -- hello world":
				return nil
			default:
				t.Fatalf("unexpected tmux args: %q", got)
				return nil
			}
		},
		func(args ...string) ([]byte, error) { return nil, errors.New("unexpected output call") })

	resp := authedRequest(t, "POST", "/api/send-keys?mode=literal&name=feature%2Ffoo&keys=hello%20world", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestPaneContentTextReturnsPlainText(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return errors.New("no web session") },
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "capture-pane -t =jf-add-web: -p -S -50000"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("plain output\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/pane-content.txt?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
	if body := resp.Body.String(); body != "plain output" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestPaneContentViewEscapesHTML(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return nil },
		func(args ...string) ([]byte, error) {
			return []byte("<script>alert('xss')</script>\n& raw"), nil
		})

	resp := authedRequest(t, "GET", "/api/pane-content/view?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
	body := resp.Body.String()
	if strings.Contains(body, "<script>alert('xss')</script>") {
		t.Fatalf("response should escape pane content, got %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;") {
		t.Fatalf("expected escaped script tag in response, got %s", body)
	}
	if !strings.Contains(body, "requestAnimationFrame(scrollToBottom)") {
		t.Fatalf("expected auto-scroll script in html response")
	}
}

func TestPaneContentViewInvalidSessionReturnsPlainError(t *testing.T) {
	resp := authedRequest(t, "GET", "/api/pane-content/view?name=<bad>", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
	if !strings.Contains(resp.Body.String(), "invalid session name") {
		t.Fatalf("unexpected body: %s", resp.Body.String())
	}
}

func withStubbedTmux(t *testing.T, run func(args ...string) error, output func(args ...string) ([]byte, error)) {
	t.Helper()
	origRun := execTmuxRun
	origOutput := execTmuxOutput
	execTmuxRun = run
	execTmuxOutput = output
	t.Cleanup(func() {
		execTmuxRun = origRun
		execTmuxOutput = origOutput
	})
}

func authedRequest(t *testing.T, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	handler := authMiddleware("test-secret", mux)
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}
