package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGatepostLogsProxyURL(t *testing.T) {
	if got := gatepostLogsProxyURL("jf-better-ui"); got != "/api/gatepost/logs/proxy/jf-better-ui/" {
		t.Fatalf("unexpected proxy url: %q", got)
	}
	// Branch-style names with slashes must be percent-encoded into one segment.
	if got := gatepostLogsProxyURL("feat/logs"); got != "/api/gatepost/logs/proxy/feat%2Flogs/" {
		t.Fatalf("slash name not escaped: %q", got)
	}
}

func TestRewriteGatepostLogsHTMLPrefixesApiPaths(t *testing.T) {
	mount := "/api/gatepost/logs/proxy/jf-better-ui/"
	in := []byte(`
      state.config = await api("/api/config");
      window.open(` + "`/api/requests/${seq}/body/${kind}`" + `, "_blank");
      location.href = "/api/requests?" + p;
    `)
	out := string(rewriteGatepostLogsHTML(in, mount))

	for _, want := range []string{
		`api("/api/gatepost/logs/proxy/jf-better-ui/api/config")`,
		"`/api/gatepost/logs/proxy/jf-better-ui/api/requests/${seq}/body/${kind}`",
		`location.href = "/api/gatepost/logs/proxy/jf-better-ui/api/requests?"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten HTML missing %q\n got: %s", want, out)
		}
	}
}

func TestRewriteGatepostLogsHTMLScopedToDelimitedPaths(t *testing.T) {
	mount := "/api/gatepost/logs/proxy/s/"
	// A bare /api/ not preceded by a quote/backtick (e.g. a comment or unrelated
	// text) must not be rewritten; only string/template-literal paths are.
	in := []byte(`text /api/free and "/api/x"`)
	s := string(rewriteGatepostLogsHTML(in, mount))

	if !strings.Contains(s, `"/api/gatepost/logs/proxy/s/api/x"`) {
		t.Errorf("quoted path not rewritten: %s", s)
	}
	if !strings.Contains(s, "text /api/free") {
		t.Errorf("unquoted /api/ should be untouched: %s", s)
	}
}

func TestRewriteGatepostLogsResponseSkipsNonHTML(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"path":"/api/x"}`)),
	}
	if err := rewriteGatepostLogsResponse(resp, "/api/gatepost/logs/proxy/s/"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"path":"/api/x"}` {
		t.Errorf("JSON body should be untouched, got %s", body)
	}
}
