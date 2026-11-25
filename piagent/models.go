package piagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Model selection for managed agents. A caller names a model as
// "provider/model-id" (or a bare model id that only one provider offers).
// It is validated against the models the owner's Pi reports as available
// (`pi --list-models`: models whose provider has auth configured), using the
// owner's own launch command and pi_args, so extension providers count and
// nothing is started on a model Pi would reject or silently replace.

const (
	// MaxModelRefBytes bounds a requested model reference.
	MaxModelRefBytes = 200
	// MaxValidModelsListed bounds valid_models in an invalid_model error.
	MaxValidModelsListed = 300
	modelCatalogTTL      = time.Minute
	modelCatalogTimeout  = 20 * time.Second
)

// ModelError reports a requested model that Pi does not offer (unknown, or
// its provider has no auth configured). Nothing was started or queued.
type ModelError struct {
	Requested   string
	Reason      string   // "unknown_or_unavailable" or "ambiguous"
	Candidates  []string // ambiguous: the matching provider/model refs
	Suggestions []string // close matches
	Valid       []string // every available model, sorted
}

func (e *ModelError) Error() string {
	if e.Reason == "ambiguous" {
		return fmt.Sprintf("model %q is offered by more than one provider (%s); pass it as provider/model. Nothing was started or queued",
			e.Requested, strings.Join(e.Candidates, ", "))
	}
	msg := fmt.Sprintf("model %q is unknown or unavailable (not listed by `pi --list-models`; its provider may have no auth configured). Nothing was started or queued. Pass one of valid_models as provider/model", e.Requested)
	if len(e.Suggestions) > 0 {
		msg += "; closest: " + strings.Join(e.Suggestions, ", ")
	}
	return msg
}

// ModelCatalogError means the available models could not be determined, so
// a requested model could not be validated. Nothing was started or queued.
type ModelCatalogError struct{ Err error }

func (e *ModelCatalogError) Error() string {
	return fmt.Sprintf("cannot validate the requested model: listing Pi's available models failed (%v). Nothing was started or queued; retry, or omit model to use the default", e.Err)
}

func (e *ModelCatalogError) Unwrap() error { return e.Err }

type modelCatalog struct {
	mu     sync.Mutex
	at     time.Time
	models []string
}

// AvailableModels returns the provider/model refs the owner's Pi can use,
// sorted. Successful listings are cached briefly; failures are not cached.
func (m *Manager) AvailableModels() ([]string, error) {
	m.models.mu.Lock()
	defer m.models.mu.Unlock()
	if m.models.models != nil && time.Since(m.models.at) < modelCatalogTTL {
		return m.models.models, nil
	}
	list := m.listModels
	if list == nil {
		list = m.piListModels
	}
	models, err := list()
	if err != nil {
		return nil, &ModelCatalogError{Err: err}
	}
	if len(models) == 0 {
		return nil, &ModelCatalogError{Err: fmt.Errorf("Pi reports no available models")}
	}
	models = append([]string(nil), models...)
	sort.Strings(models)
	m.models.models, m.models.at = models, time.Now()
	return models, nil
}

// piListModels runs the owner's Pi command (same executable, wrapper args
// and pi_args a launch uses) with --list-models and parses its table.
func (m *Manager) piListModels() ([]string, error) {
	if err := m.launchPolicyUsable(); err != nil {
		return nil, err
	}
	// A launch runs pi_command from the agent's worktree, which does not
	// exist yet when a start is validated. A relative path would name a
	// different file here than there, so it cannot be validated: refuse
	// rather than check one executable and launch another.
	if strings.Contains(m.Config.PiCommand, "/") && !filepath.IsAbs(m.Config.PiCommand) {
		return nil, fmt.Errorf("pi_mcp.pi_command %q is a relative path; use an absolute path or a command name on PATH to request a model", m.Config.PiCommand)
	}
	args := append(append(append([]string{}, m.Config.PiCommandArgs...), m.Config.PiArgs...), "--list-models")
	ctx, cancel := context.WithTimeout(context.Background(), modelCatalogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.Config.PiCommand, args...)
	cmd.Dir = os.TempDir()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail != "" {
			return nil, fmt.Errorf("list Pi models: %w: %s", err, Redact(detail))
		}
		return nil, err
	}
	return parseListModels(stdout.String()), nil
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// parseListModels reads `pi --list-models` output: a header row starting
// with "provider model", then one row per model. Anything else (warnings,
// "no models" notices) is ignored.
func parseListModels(out string) []string {
	var models []string
	header := false
	for _, line := range strings.Split(ansiRE.ReplaceAllString(out, ""), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if !header {
			header = f[0] == "provider" && f[1] == "model"
			continue
		}
		models = append(models, f[0]+"/"+f[1])
	}
	return models
}

// ResolveModel validates a requested model reference and returns its
// canonical "provider/model-id" form. An empty request means "Pi's
// default" and returns "" without consulting Pi.
func (m *Manager) ResolveModel(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", nil
	}
	if err := checkModelSyntax(requested); err != nil {
		return "", err
	}
	valid, err := m.AvailableModels()
	if err != nil {
		return "", err
	}
	return resolveModelRef(requested, valid)
}

// checkModelSyntax rejects malformed references without consulting Pi.
func checkModelSyntax(requested string) error {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil
	}
	if len(requested) > MaxModelRefBytes || strings.IndexFunc(requested, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("model must be a provider/model reference without spaces (max %d bytes)", MaxModelRefBytes)
	}
	if strings.HasPrefix(requested, "/") {
		return fmt.Errorf("model %q looks like a slash command; pass a provider/model reference such as anthropic/claude-sonnet-4-5", requested)
	}
	if strings.HasPrefix(requested, "-") {
		return fmt.Errorf("model %q must not start with '-'", requested)
	}
	return nil
}

// sameModelRequest reports whether a retried request's model names the
// recorded (canonical) model, without consulting Pi, in either form the
// resolver accepts: the full reference, or the model id (everything after
// the provider, which may itself contain '/').
func sameModelRequest(requested, recorded string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" || recorded == "" {
		return requested == recorded
	}
	if strings.EqualFold(requested, recorded) {
		return true
	}
	_, id, _ := strings.Cut(recorded, "/")
	return strings.EqualFold(requested, id)
}

func resolveModelRef(requested string, valid []string) (string, error) {
	var bare []string
	for _, v := range valid {
		if strings.EqualFold(v, requested) {
			return v, nil
		}
		if _, id, ok := strings.Cut(v, "/"); ok && strings.EqualFold(id, requested) {
			bare = append(bare, v)
		}
	}
	if len(bare) == 1 {
		return bare[0], nil
	}
	if len(bare) > 1 {
		return "", &ModelError{Requested: requested, Reason: "ambiguous", Candidates: bare, Valid: valid}
	}
	return "", &ModelError{Requested: requested, Reason: "unknown_or_unavailable", Suggestions: suggestModels(requested, valid), Valid: valid}
}

// suggestModels returns up to five refs containing the request's model part
// (case-insensitive), or sharing its longest prefix.
func suggestModels(requested string, valid []string) []string {
	q := strings.ToLower(requested)
	if _, id, ok := strings.Cut(q, "/"); ok {
		q = id
	}
	var out []string
	for _, v := range valid {
		if q != "" && strings.Contains(strings.ToLower(v), q) {
			out = append(out, v)
			if len(out) == 5 {
				return out
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	for n := len(q) - 1; n >= 3 && len(out) == 0; n-- {
		for _, v := range valid {
			if _, id, _ := strings.Cut(strings.ToLower(v), "/"); strings.HasPrefix(id, q[:n]) {
				out = append(out, v)
				if len(out) == 5 {
					break
				}
			}
		}
	}
	return out
}

// ModelSwitchUnsupportedError: a model switch cannot be queued for this
// agent because its current Pi was not launched with a bridge that can
// switch models (Protocol >= 0), or that bridge has not reported since the
// latest launch (Protocol == -1). Nothing was queued.
type ModelSwitchUnsupportedError struct {
	AgentID  string
	Protocol int
}

func (e *ModelSwitchUnsupportedError) Error() string {
	if e.Protocol < 0 {
		return fmt.Sprintf("cannot queue a model switch for agent %s: its Pi bridge has not reported since the latest launch, so DevX cannot confirm it can switch models. "+
			"Nothing was queued; retry in a few seconds (check pi_list_sessions bridge_online)", e.AgentID)
	}
	return fmt.Sprintf("agent %s runs DevX bridge protocol %d, which cannot switch models; nothing was queued. Send without model, "+
		"or have a human relaunch the agent (devx agent relaunch %s) to load bridge protocol %d", e.AgentID, e.Protocol, e.AgentID, BridgeModelProtocol)
}

// requireModelSwitchLocked checks that the Pi of the agent's latest launch
// runs a bridge that applies per-task model switches. Callers hold the
// agent lock. Freshness is not required: the protocol is fixed per launch.
func (m *Manager) requireModelSwitchLocked(a *Agent) error {
	b, err := m.Store.LoadBridge(a.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if b == nil || a.LaunchNonce == "" || b.Nonce != a.LaunchNonce {
		return &ModelSwitchUnsupportedError{AgentID: a.ID, Protocol: -1}
	}
	if b.Protocol < BridgeModelProtocol {
		return &ModelSwitchUnsupportedError{AgentID: a.ID, Protocol: b.Protocol}
	}
	return nil
}

// reportedModel is the model the agent's current Pi last reported, or "" if
// unknown (no heartbeat from the latest launch, or an older bridge).
func reportedModel(a *Agent, b *Bridge) string {
	if b == nil || a.LaunchNonce == "" || b.Nonce != a.LaunchNonce {
		return ""
	}
	return b.Model
}
