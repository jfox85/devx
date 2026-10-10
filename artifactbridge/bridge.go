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
// The local scope is the owner's policy (policy.go), re-read from the owner's
// global DevX config and re-evaluated on every tools/list and tool call, so
// edits take effect on the next call with no restart:
//   - read (default on): every session available through the MCP path in
//     pi_mcp.allowed_projects, unless pi_mcp.artifacts.sessions is present,
//     in which case only those sessions; exclude_sessions/exclude_projects
//     always subtract;
//   - upload (default off): only sessions named in an explicit
//     pi_mcp.artifacts.sessions list, never the default-wide read scope;
//   - in every case the agent must be the unambiguous owner of a live
//     session (eligibleSession): not retired, session exists at the agent's
//     worktree with the same project, no marker naming another agent, no
//     duplicate claimant or shared worktree. A missing ownership marker is
//     not a denial (the MCP path does not require one); a contradictory one
//     always is.
//
// An unreadable or invalid config denies everything for that call.
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
	// CompletedRecordExpiry is how long a finished upload's completion record
	// (used to replay its result) is kept in DevX state.
	CompletedRecordExpiry = 30 * 24 * time.Hour
	// MaxSessionAttachments / MaxSessionAttachmentBytes bound the remote
	// attachments registered in one session, so a granted principal cannot
	// fill the worktree's disk with fresh idempotency keys.
	MaxSessionAttachments     = 200
	MaxSessionAttachmentBytes = 512 << 20
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

// Config holds the bridge's static settings.
type Config struct {
	// StateDir holds upload staging state (outside every worktree).
	StateDir string
}

// Service implements the bridge operations.
type Service struct {
	Cfg    Config
	Agents *piagent.Store
	// Sessions returns the current DevX session records.
	Sessions func() (map[string]*session.Session, error)
	// Policy returns the current owner policy; it is called for every
	// tools/list and tool call. An error disables the bridge for that call.
	Policy func() (Policy, error)
	Now    func() time.Time
}

// New returns a Service backed by the real DevX session metadata and the
// given policy source.
func New(cfg Config, agents *piagent.Store, policy func() (Policy, error)) *Service {
	return &Service{Cfg: cfg, Agents: agents, Policy: policy, Now: func() time.Time { return time.Now().UTC() },
		Sessions: func() (map[string]*session.Session, error) {
			st, err := session.LoadSessions()
			if err != nil {
				return nil, err
			}
			return st.Sessions, nil
		}}
}

// policy returns the current, normalized policy or errPolicy (fail closed).
// Platforms without the no-follow primitives never enable the bridge.
func (s *Service) policy() (Policy, error) {
	if !platformSupported || s.Policy == nil {
		return Policy{}, errPolicy
	}
	p, err := s.Policy()
	if err != nil {
		return Policy{}, errPolicy
	}
	if p.MaxUploadBytes <= 0 {
		p.MaxUploadBytes = DefaultMaxUploadBytes
	}
	if p.MaxUploadBytes > MaxUploadBytesCeiling {
		p.MaxUploadBytes = MaxUploadBytesCeiling
	}
	return p, nil
}

// scope is an authorized (agent, session) pair for one operation.
type scope struct {
	agent  *piagent.Agent
	sess   *session.Session
	policy Policy
}

type capability int

const (
	capRead capability = iota
	capUpload
)

// authorize re-reads the policy, the agent and its session on every call.
// Every denial is reported the same way whether the agent is unknown or
// merely not authorized, so callers cannot probe for other agents.
func (s *Service) authorize(agentID string, want capability) (*scope, error) {
	deny := errf(codeDenied, "agent %q is not available to this artifact bridge", agentID)
	if !piagent.ValidAgentID(agentID) {
		return nil, errf(codeInvalid, "agent_id must look like pa_<12 hex>")
	}
	p, err := s.policy()
	if err != nil {
		return nil, err
	}
	a, err := s.Agents.LoadAgent(agentID)
	if err != nil || a.RetiredAt != nil || a.ID != agentID || a.Project == "" {
		return nil, deny
	}
	switch want {
	case capRead:
		if !p.readEligible(a.DevxSession, a.Project) {
			return nil, deny
		}
	case capUpload:
		if !p.uploadEligible(a.DevxSession, a.Project) {
			return nil, deny
		}
	default:
		return nil, deny
	}
	sessions, err := s.Sessions()
	if err != nil {
		return nil, errf(codeFailed, "session metadata is unavailable")
	}
	agents, err := s.Agents.ListAgents()
	if err != nil {
		return nil, errf(codeFailed, "agent records are unavailable")
	}
	sess, reason := eligibleSession(a, sessions, agents)
	if reason != "" {
		return nil, deny
	}
	return &scope{agent: a, sess: sess, policy: p}, nil
}

// Session-binding denial reasons (internal; callers see one generic denial).
const (
	denyRetired          = "agent_retired_or_invalid"
	denyNoSession        = "session_missing"
	denyPathMismatch     = "worktree_mismatch"
	denyProjectMismatch  = "project_mismatch"
	denyForeignLocalOnly = "local_only_marker_names_other_agent"
	denyForeignManaged   = "managed_marker_names_other_agent"
	denyBothMarkers      = "contradictory_markers"
	denyContainerized    = "container_session"
	denyDuplicateClaim   = "another_live_agent_claims_session"
	denySharedWorktree   = "another_live_agent_or_session_uses_worktree"
	denyInstanceMismatch = "session_instance_mismatch"
	denyInstanceMissing  = "session_instance_id_missing"
	denyUnboundLegacy    = "legacy_agent_not_bound_to_session_instance"
)

// eligibleSession is the single session-binding predicate for artifact
// access. It decides whether agent a is the unambiguous owner of a live DevX
// session; the owner policy (projects, capability, explicit lists and
// exclusions) is applied separately in Policy. It returns the session and ""
// when eligible, or a denial reason.
//
// Identity is the session INSTANCE, not its name, path or timestamps:
//
//   - the agent exists and is not retired, with a project;
//   - the session named by the agent exists, has the same name, an absolute
//     path equal to the agent's worktree, and the same project (a session
//     without a project alias is accepted only by name+path);
//   - it is a host session (container targets are never adopted or created
//     by the MCP path);
//   - a local_only marker, if present, names this agent; a managed_agent
//     marker, if present, names this agent; never both;
//   - instance binding:
//   - an agent bound to a session instance (Agent.SessionInstanceID, set at
//     start, adoption, or by the reviewed `devx session instances`
//     migration) requires the session record's instance_id to equal it. A
//     session removed and recreated under the same name and path has a new
//     id and is denied, whatever its timestamps.
//   - if the record has no instance_id because an older DevX writer
//     dropped the field, the record's created_at must equal, to the
//     nanosecond, the created_at recorded on the agent at binding (old
//     writers preserve created_at; a recreated record has a new one). The
//     migration restores the id.
//   - an unbound legacy agent (from before instance ids, not yet migrated)
//     is eligible only while a marker on the session names it AND the
//     record predates the agent AND carries no instance id; otherwise the
//     owner binds it through the reviewed migration;
//   - no other non-retired agent claims the same session name, and no other
//     non-retired agent or other session uses the same worktree path.
//
// Agent state (idle, running, human_control, adopted_pending_relaunch, ...)
// is deliberately not part of the predicate: artifacts are files in the
// session, readable regardless of who currently drives the Pi.
func eligibleSession(a *piagent.Agent, sessions map[string]*session.Session, agents []*piagent.Agent) (*session.Session, string) {
	if a == nil || a.RetiredAt != nil || a.Project == "" || a.DevxSession == "" {
		return nil, denyRetired
	}
	sess, ok := sessions[a.DevxSession]
	if !ok || sess == nil || sess.Name != a.DevxSession {
		return nil, denyNoSession
	}
	wt := filepath.Clean(a.Worktree)
	if sess.Path == "" || !filepath.IsAbs(sess.Path) || !filepath.IsAbs(a.Worktree) || filepath.Clean(sess.Path) != wt {
		return nil, denyPathMismatch
	}
	if sess.ProjectAlias != "" && sess.ProjectAlias != a.Project {
		return nil, denyProjectMismatch
	}
	if sess.IsContainerized() {
		return nil, denyContainerized
	}
	if sess.LocalOnly != nil && sess.ManagedAgent != "" {
		return nil, denyBothMarkers
	}
	if sess.LocalOnly != nil && sess.LocalOnly.AgentID != a.ID {
		return nil, denyForeignLocalOnly
	}
	if sess.ManagedAgent != "" && sess.ManagedAgent != a.ID {
		return nil, denyForeignManaged
	}
	marked := (sess.LocalOnly != nil && sess.LocalOnly.AgentID == a.ID) || sess.ManagedAgent == a.ID
	switch {
	case a.SessionInstanceID != "":
		if !session.MatchesBoundInstance(sess, a.SessionInstanceID, a.SessionCreatedAt) {
			if sess.InstanceID != "" {
				return nil, denyInstanceMismatch
			}
			return nil, denyInstanceMissing
		}
	default:
		// Unbound legacy agent (not yet migrated). A marker alone is not
		// proof: before instance ids, relaunching an adopted agent re-marked
		// whatever session had the name. So also require the record to
		// predate the agent (every binding flow writes the session first)
		// and to carry no instance id (a record with one was created by an
		// instance-aware build, after this legacy agent).
		if !marked || sess.InstanceID != "" || sess.CreatedAt.IsZero() || a.CreatedAt.IsZero() || sess.CreatedAt.After(a.CreatedAt) {
			return nil, denyUnboundLegacy
		}
	}
	for _, o := range agents {
		if o == nil || o.ID == a.ID || o.RetiredAt != nil {
			continue
		}
		if o.DevxSession == a.DevxSession {
			return nil, denyDuplicateClaim
		}
		if filepath.Clean(o.Worktree) == wt {
			return nil, denySharedWorktree
		}
	}
	for name, other := range sessions {
		if other != nil && name != a.DevxSession && filepath.Clean(other.Path) == wt {
			return nil, denySharedWorktree
		}
	}
	return sess, ""
}

// EligibleRead reports, for every agent record, whether default artifact
// reads would be allowed now and why not. It is an inventory/diagnostic
// helper with the same predicate and policy as authorize; it serves no data.
type EligibleRead struct {
	AgentID string `json:"agent_id"`
	Session string `json:"session"`
	Project string `json:"project"`
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// ReadInventory evaluates read eligibility for every agent record.
func (s *Service) ReadInventory() ([]EligibleRead, error) {
	p, err := s.policy()
	if err != nil {
		return nil, err
	}
	sessions, err := s.Sessions()
	if err != nil {
		return nil, err
	}
	agents, err := s.Agents.ListAgents()
	if err != nil {
		return nil, err
	}
	out := make([]EligibleRead, 0, len(agents))
	for _, a := range agents {
		r := EligibleRead{AgentID: a.ID, Session: a.DevxSession, Project: a.Project}
		if _, reason := eligibleSession(a, sessions, agents); reason != "" {
			r.Reason = reason
		} else if !p.readEligible(a.DevxSession, a.Project) {
			r.Reason = "policy"
		} else {
			r.Allowed = true
		}
		out = append(out, r)
	}
	return out, nil
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
