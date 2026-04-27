package artifact

import (
	"os"
	"path/filepath"

	"github.com/jfox85/devx/session"
)

const defaultThemeCSS = `:root {
  color-scheme: light dark;
  --bg: #0f1117;
  --text: #e5e7eb;
  --muted: #8b91a8;
  --accent: #67e8f9;
  --surface: #171b2a;
  --border: #2d3654;
}
body {
  margin: 0;
  padding: 2rem;
  background: var(--bg);
  color: var(--text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  line-height: 1.6;
}
main, body > header { max-width: 960px; margin: 0 auto; }
a { color: var(--accent); }
img, video { max-width: 100%; height: auto; border-radius: 8px; }
pre, code { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
pre { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 1rem; overflow-x: auto; }
.meta { color: var(--muted); }
.badge-success, .badge-warning, .badge-info { border-radius: 999px; padding: .15rem .5rem; font-size: .8rem; font-weight: 600; }
.badge-success { background: rgba(74,222,128,.15); color: #4ade80; }
.badge-warning { background: rgba(251,191,36,.15); color: #fbbf24; }
.badge-info { background: rgba(103,232,249,.15); color: #67e8f9; }
.diff-add { color: #4ade80; }
.diff-del { color: #f87171; }
@media (prefers-color-scheme: light) {
  :root { --bg: #ffffff; --text: #111827; --muted: #6b7280; --surface: #f8fafc; --border: #e5e7eb; --accent: #0891b2; }
}
`

func EnsureTheme(sess *session.Session) error {
	dir := DirForSession(sess)
	if err := EnsureArtifactDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, "theme.css")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(defaultThemeCSS), 0o644)
}
