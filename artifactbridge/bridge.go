// Package artifactbridge lets remote MCP clients exchange explicitly
// registered files with a managed Pi agent's DevX session, without any
// general filesystem access.
//
// Eligibility: only files registered in the session's existing DevX artifact
// manifest (<worktree>/.artifacts/manifest.json) can be listed or read, and
// uploads land only in <worktree>/.artifacts/attachments/ and are registered
// in that same manifest. There is no parallel registry: retention, archive
// and removal remain the existing `devx artifact` semantics.
//
// Trust boundary (read this before granting access)
//
// `devx mcp pi` receives NO caller identity. The Agent Shed gateway
// authenticates the calling principal (assistant), checks that principal's
// reviewed grant for the relay app (which tools), and passes only the tool
// name and arguments; the relay forwards them over stdio. The agent_id in the
// arguments is chosen by the caller. So this package cannot prove that a given
// caller may read a given session; it can only enforce what the local owner
// has opted in to. Concretely, any principal granted a bridge tool on the
// devx-pi app can use it on every session the owner exposed, and nothing more.
//
// The local scope therefore is an owner allowlist, re-checked on every call:
//   - the capability (read / upload) is enabled in the DevX config;
//   - the agent's session is listed in pi_mcp.artifacts.sessions (exact
//     DevX session names; empty = no session is exposed). Being a managed
//     agent, or being in pi_mcp.allowed_projects, is NOT enough;
//   - the agent exists, is not retired, its project is in
//     pi_mcp.allowed_projects, its DevX session record still points at the
//     agent's worktree with the same project, and no other agent owns it.
//
// Removing a session from the list, disabling a capability, retiring the
// agent or removing the session takes effect on the next call (config is read
// at process start; the relay starts a fresh `devx mcp pi` per local session,
// and the session record/agent record are re-read on every call).
//
// Artifact IDs are opaque and derived from (agent, session, manifest id), so
// an ID observed in one session does not resolve in any other. They are not
// secrets or capabilities: possession of an ID grants nothing.
package artifactbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

const (
	artifactsDirName = artifactpkg.DirName
	// AttachmentsFolder is the artifact folder that receives uploads.
	AttachmentsFolder = "attachments"
	// AttachmentTag marks manifest entries created by remote upload.
	AttachmentTag   = "remote-attachment"
	attachmentAgent = "remote-upload"

	// MaxReadableBytes bounds files that can be hashed or read.
	MaxReadableBytes = 32 << 20
	// MaxReadChunk bounds one read chunk (raw bytes).
	MaxReadChunk = 16 << 10
	// MaxInlineImageBytes bounds an image returned as an MCP image block.
	MaxInlineImageBytes = 40 << 10
	// MaxResultBytes keeps every tool result under the Agent Shed gateway's
	// 64 KiB result bound with headroom for the JSON-RPC envelope.
	MaxResultBytes = 60000
	// MaxUploadChunk bounds one decoded upload chunk so the base64 argument
	// stays under the gateway's 16 KiB argument bound.
	MaxUploadChunk = 8 << 10
	// DefaultMaxUploadBytes / MaxUploadBytesCeiling bound one attachment.
	DefaultMaxUploadBytes = 10 << 20
	MaxUploadBytesCeiling = 25 << 20
	// MaxPendingUploads bounds incomplete uploads per agent.
	MaxPendingUploads = 8
	// UploadExpiry is how long an incomplete upload's staged bytes are kept
	// without progress. Completed attachments are never expired here.
	UploadExpiry = 24 * time.Hour
	// MaxListPage bounds one list page.
	MaxListPage = 50
	// MaxTaskArtifacts bounds artifacts attached to a task status.
	MaxTaskArtifacts = 20
)

// Error codes returned to MCP clients.
const (
	codeDenied          = "permission_denied"
	codeNotFound        = "not_found"
	codeInvalid         = "invalid_arguments"
	codeTooLarge        = "too_large"
	codeUnsupportedType = "unsupported_type"
	codeVersionMismatch = "version_mismatch"
	codeConflict        = "conflict"
	codeIntegrity       = "integrity_failed"
	codeChanged         = "changed_during_read"
	codeUnavailable     = "unavailable"
	codeOffset          = "offset_mismatch"
	codeExpired         = "upload_expired"
	codeLimit           = "limit_exceeded"
	codeFailed          = "failed"
)

// Error is a client-facing error. Messages never contain absolute paths or
// file contents.
type Error struct {
	Code  string
	Msg   string
	Extra map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

var errUnavailable = errf(codeUnavailable, "artifact file is missing, not a regular file, or not reachable without following a link")

var errNotExist = errf(codeNotFound, "not found")

// Config controls the bridge. Both capabilities default to off, and no
// session is exposed unless listed in Sessions.
type Config struct {
	Read            bool
	Upload          bool
	AllowedProjects []string
	// Sessions are the DevX session names the owner exposes to the bridge.
	Sessions []string
	// StateDir holds upload staging state (outside every worktree).
	StateDir       string
	MaxUploadBytes int64
}

// Service implements the bridge operations.
type Service struct {
	Cfg    Config
	Agents *piagent.Store
	// Sessions returns the current DevX session records.
	Sessions func() (map[string]*session.Session, error)
	Now      func() time.Time
}

// New returns a Service backed by the real DevX session metadata.
func New(cfg Config, agents *piagent.Store) *Service {
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = DefaultMaxUploadBytes
	}
	if cfg.MaxUploadBytes > MaxUploadBytesCeiling {
		cfg.MaxUploadBytes = MaxUploadBytesCeiling
	}
	if !platformSupported {
		cfg.Read, cfg.Upload = false, false
	}
	return &Service{Cfg: cfg, Agents: agents, Now: func() time.Time { return time.Now().UTC() },
		Sessions: func() (map[string]*session.Session, error) {
			st, err := session.LoadSessions()
			if err != nil {
				return nil, err
			}
			return st.Sessions, nil
		}}
}

// scope is an authorized (agent, session) pair for one operation.
type scope struct {
	agent *piagent.Agent
	sess  *session.Session
}

// authorize re-checks the agent and its session on every call. Every denial
// is reported the same way whether the agent is unknown or merely not
// authorized, so callers cannot probe for other agents.
func (s *Service) authorize(agentID string) (*scope, error) {
	deny := errf(codeDenied, "agent %q is not available to this artifact bridge", agentID)
	if !piagent.ValidAgentID(agentID) {
		return nil, errf(codeInvalid, "agent_id must look like pa_<12 hex>")
	}
	a, err := s.Agents.LoadAgent(agentID)
	if err != nil || a.RetiredAt != nil || a.ID != agentID {
		return nil, deny
	}
	if !contains(s.Cfg.Sessions, a.DevxSession) {
		return nil, deny
	}
	if a.Project == "" || !contains(s.Cfg.AllowedProjects, a.Project) {
		return nil, deny
	}
	sessions, err := s.Sessions()
	if err != nil {
		return nil, errf(codeFailed, "session metadata is unavailable")
	}
	sess, ok := sessions[a.DevxSession]
	if !ok || sess == nil || sess.Name != a.DevxSession {
		return nil, deny
	}
	if sess.Path == "" || !filepath.IsAbs(sess.Path) || filepath.Clean(sess.Path) != filepath.Clean(a.Worktree) {
		return nil, deny
	}
	if sess.ProjectAlias != "" && sess.ProjectAlias != a.Project {
		return nil, deny
	}
	if sess.LocalOnly != nil && sess.LocalOnly.AgentID != a.ID {
		return nil, deny
	}
	if sess.ManagedAgent != "" && sess.ManagedAgent != a.ID {
		return nil, deny
	}
	// Exactly one live agent may claim the session; otherwise the scope is
	// ambiguous and nothing is served.
	agents, err := s.Agents.ListAgents()
	if err != nil {
		return nil, errf(codeFailed, "agent records are unavailable")
	}
	for _, o := range agents {
		if o.ID != a.ID && o.RetiredAt == nil && o.DevxSession == a.DevxSession {
			return nil, deny
		}
	}
	return &scope{agent: a, sess: sess}, nil
}

// manifest loads the session's artifact manifest without following any
// symlink (.artifacts or manifest.json), with the standard validation.
func (s *Service) manifest(sc *scope) (*artifactpkg.Manifest, error) {
	data, err := readManifestNoFollow(sc.sess.Path)
	if errors.Is(err, errNotExist) {
		return artifactpkg.NewManifest(sc.sess.Name), nil
	}
	if err != nil {
		return nil, errf(codeUnavailable, "the session's artifact manifest is unreadable")
	}
	m, err := artifactpkg.ParseManifest(data, sc.sess.Name)
	if err != nil {
		return nil, errf(codeUnavailable, "the session's artifact manifest is unreadable")
	}
	return m, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// opaqueID derives the remote artifact ID. It binds the agent and session so
// an ID is meaningless outside the scope that produced it.
func opaqueID(agentID, sessionName, manifestID string) string {
	h := sha256.Sum256([]byte("devx-artifact|v1|" + agentID + "|" + sessionName + "|" + manifestID))
	return "dxa_" + hex.EncodeToString(h[:12])
}

// splitRel validates a manifest-relative file path and returns its segments.
func splitRel(rel string) ([]string, error) {
	if err := artifactpkg.ValidateRelativePath(rel); err != nil {
		return nil, errf(codeUnavailable, "artifact path is invalid")
	}
	clean := path.Clean(filepath.ToSlash(rel))
	if strings.HasPrefix(clean, "/") || clean == "." {
		return nil, errf(codeUnavailable, "artifact path is invalid")
	}
	parts := strings.Split(clean, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, errf(codeUnavailable, "artifact path is invalid")
		}
	}
	return parts, nil
}

func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return errf(codeFailed, "internal error")
}
