package target

import "os"

const defaultGatepostPolicy = `version: 1

defaults:
  unknown_action: smart
  fail_mode: closed

phases:
  install:
    allow:
      - host: "*.npmjs.org"
      - host: "registry.yarnpkg.com"
      - host: "*.yarnpkg.com"
      - host: "github.com"
      - host: "api.github.com"
      - host: "raw.githubusercontent.com"
      - host: "proxy.golang.org"
      - host: "sum.golang.org"
      - host: "storage.googleapis.com"
      - host: "pypi.org"
      - host: "files.pythonhosted.org"
      - host: "api.anthropic.com"
      - host: "platform.claude.com"
      - host: "chatgpt.com"
      - host: "auth.openai.com"
      - host: "api.openai.com"
      - host: "generativelanguage.googleapis.com"
      - host: "host.docker.internal"
    unknown_action: allow

  run:
    allow:
      - host: "api.anthropic.com"
      - host: "platform.claude.com"
      - host: "chatgpt.com"
      - host: "auth.openai.com"
      - host: "api.openai.com"
      - host: "generativelanguage.googleapis.com"
      - host: "host.docker.internal"
      - host: "github.com"
      - host: "api.github.com"
      - host: "raw.githubusercontent.com"
      - host: "*.npmjs.org"
      - host: "registry.yarnpkg.com"
      - host: "proxy.golang.org"
      - host: "sum.golang.org"
      - host: "storage.googleapis.com"
      - host: "pypi.org"
      - host: "files.pythonhosted.org"
      - host: "fonts.googleapis.com"
      - host: "fonts.gstatic.com"
      - host: "objects.githubusercontent.com"
      - host: "release-assets.githubusercontent.com"
      - host: "r.jina.ai"
      - host: "api.search.brave.com"
    unknown_action: smart
`

func writeGatepostPolicy(path string) error {
	return os.WriteFile(path, []byte(defaultGatepostPolicy), 0o600)
}
