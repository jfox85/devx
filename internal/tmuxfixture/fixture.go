// Package tmuxfixture gives tests a private tmux server that cannot reach a
// developer's real tmux server.
//
// Isolation rules, all enforced before any tmux process is started:
//
//   - Every command is run as `tmux -S <socket> ...`. The socket is an absolute
//     path inside a directory this fixture created; it never depends on
//     TMUX, TMUX_TMPDIR or -L name resolution.
//   - The child environment has TMUX, TMUX_PANE and TMUX_TMPDIR removed, so
//     nothing inherited from a surrounding tmux can select another server.
//   - Ownership is recorded on disk (owner.json with a random nonce) and is
//     re-verified before each command. The socket must live inside the owned
//     directory and must not be a default or inherited socket path.
//   - Callers cannot pass global options (-L, -S, -f, ...) or chain commands
//     with ";". kill-server is never allowed.
//   - Sessions are created only through NewSession, which records the name
//     before tmux runs. Cleanup kills exactly those recorded sessions with
//     `kill-session -t =<name>`; it never kills the server.
package tmuxfixture

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrRejected marks a command refused by the guard. A rejected command was
// never executed.
var ErrRejected = errors.New("tmuxfixture: rejected")

func rejectf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRejected, fmt.Sprintf(format, a...))
}

// Executor runs tmux with argv (excluding the "tmux" binary) and env.
type Executor func(argv []string, env []string) (string, error)

// ExecTmux is the real executor.
func ExecTmux(argv []string, env []string) (string, error) {
	cmd := exec.Command("tmux", argv...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Owner is the durable ownership record written to <dir>/owner.json.
type Owner struct {
	Nonce     string    `json:"nonce"`
	Socket    string    `json:"socket"`
	Dir       string    `json:"dir"`
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"created_at"`
	Sessions  []string  `json:"sessions"`
}

// Fixture is one private tmux server.
type Fixture struct {
	dir    string
	socket string
	nonce  string
	exec   Executor
	mu     sync.Mutex
}

// Options configures New.
type Options struct {
	// BaseDir is where the fixture directory is created. Default "/tmp"
	// (short, because macOS limits socket paths to 104 bytes).
	BaseDir string
	// Exec replaces the real tmux executor (tests of the guard itself).
	Exec Executor
}

const ownerFile = "owner.json"

// New creates a fresh fixture directory and ownership record. It does not
// start tmux; the server starts with the first NewSession.
func New(opts Options) (*Fixture, error) {
	base := opts.BaseDir
	if base == "" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "dxtf-")
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	f := &Fixture{dir: dir, socket: filepath.Join(dir, "tmux.sock"), nonce: randomHex(16), exec: opts.Exec}
	if f.exec == nil {
		f.exec = ExecTmux
	}
	if err := checkSocketPath(f.socket, f.dir); err != nil {
		return nil, err
	}
	o := Owner{Nonce: f.nonce, Socket: f.socket, Dir: f.dir, PID: os.Getpid(), CreatedAt: time.Now().UTC(), Sessions: []string{}}
	if err := writeOwner(f.dir, &o); err != nil {
		return nil, err
	}
	return f, nil
}

// Socket returns the fixture's absolute socket path.
func (f *Fixture) Socket() string { return f.socket }

// Dir returns the fixture-owned directory.
func (f *Fixture) Dir() string { return f.dir }

// Owner returns the current on-disk ownership record.
func (f *Fixture) Owner() (*Owner, error) { return readOwner(f.dir) }

// Env returns the scrubbed environment used for every tmux command.
func Env() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		k, _, _ := strings.Cut(kv, "=")
		if k == "TMUX" || k == "TMUX_PANE" || k == "TMUX_TMPDIR" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// commands a caller may run through Run. kill-server is absent on purpose;
// new-session and kill-session go through NewSession / Cleanup.
var allowedCommands = map[string]bool{
	"has-session": true, "display-message": true, "list-sessions": true, "list-windows": true,
	"list-panes": true, "send-keys": true, "capture-pane": true, "new-window": true,
	"respawn-pane": true, "kill-pane": true, "select-window": true, "load-buffer": true,
	"paste-buffer": true, "delete-buffer": true,
}

// Run executes an allowlisted tmux command on the fixture socket.
func (f *Fixture) Run(args ...string) (string, error) {
	if len(args) == 0 {
		return "", rejectf("empty command")
	}
	if !allowedCommands[args[0]] {
		return "", rejectf("command %q is not allowed for fixtures", args[0])
	}
	return f.run(args)
}

// NewSession records name as owned, then creates it detached.
func (f *Fixture) NewSession(name, dir string, command ...string) error {
	if !sessionNamePattern.MatchString(name) {
		return rejectf("invalid fixture session name %q", name)
	}
	f.mu.Lock()
	o, err := f.verifiedOwner()
	if err == nil {
		if !contains(o.Sessions, name) {
			// Record intent before tmux runs so cleanup still knows about
			// the session if this process dies mid-create.
			o.Sessions = append(o.Sessions, name)
			err = writeOwner(f.dir, o)
		}
	}
	f.mu.Unlock()
	if err != nil {
		return err
	}
	args := []string{"new-session", "-d", "-s", name, "-x", "200", "-y", "50"}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, command...)
	_, err = f.run(args)
	return err
}

// Owns reports whether name is a recorded session of this fixture.
func (f *Fixture) Owns(name string) bool {
	o, err := f.verifiedOwner()
	return err == nil && contains(o.Sessions, name)
}

// KillSession kills one fixture-owned session by exact name.
func (f *Fixture) KillSession(name string) error {
	if !f.Owns(name) {
		return rejectf("session %q is not owned by this fixture", name)
	}
	_, err := f.run([]string{"kill-session", "-t", "=" + name})
	return err
}

// Cleanup kills exactly the recorded sessions (never the server) and returns
// the names it attempted. A session that is already gone is not an error.
func (f *Fixture) Cleanup() ([]string, error) {
	o, err := f.verifiedOwner()
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, name := range o.Sessions {
		if _, err := f.run([]string{"has-session", "-t", "=" + name}); err != nil {
			if errors.Is(err, ErrRejected) {
				errs = append(errs, err)
			}
			continue // not running
		}
		if _, err := f.run([]string{"kill-session", "-t", "=" + name}); err != nil {
			errs = append(errs, err)
		}
	}
	return o.Sessions, errors.Join(errs...)
}

// run validates the full argv and executes it. It is the only path to the
// executor.
func (f *Fixture) run(args []string) (string, error) {
	if _, err := f.verifiedOwner(); err != nil {
		return "", err
	}
	if err := validateArgs(args); err != nil {
		return "", err
	}
	if args[0] == "kill-session" {
		if err := f.checkKillSession(args); err != nil {
			return "", err
		}
	}
	argv := append([]string{"-S", f.socket}, args...)
	if err := ValidateArgv(argv, f.socket, f.dir); err != nil {
		return "", err
	}
	f.audit(argv)
	return f.exec(argv, Env())
}

// CommandLogPath is the durable audit log of every argv this fixture
// executed (one JSON array per line).
func (f *Fixture) CommandLogPath() string { return filepath.Join(f.dir, "commands.log") }

func (f *Fixture) audit(argv []string) {
	b, _ := json.Marshal(argv)
	f.mu.Lock()
	defer f.mu.Unlock()
	fh, err := os.OpenFile(f.CommandLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = fh.Write(append(b, '\n'))
	_ = fh.Close()
}

func (f *Fixture) checkKillSession(args []string) error {
	if len(args) != 3 || args[1] != "-t" || !strings.HasPrefix(args[2], "=") {
		return rejectf("kill-session must be exactly: kill-session -t =<owned-session>")
	}
	o, err := readOwner(f.dir)
	if err != nil {
		return err
	}
	if !contains(o.Sessions, strings.TrimPrefix(args[2], "=")) {
		return rejectf("kill-session target %q is not owned by this fixture", args[2])
	}
	return nil
}

// validateArgs rejects caller-controlled arguments that could change which
// server is addressed or append other commands.
func validateArgs(args []string) error {
	if len(args) == 0 {
		return rejectf("empty command")
	}
	if len(args) == 1 && args[0] == "-V" {
		return nil // version query; contacts no server (libtmux probes it)
	}
	if strings.HasPrefix(args[0], "-") {
		return rejectf("global tmux option %q is not allowed (socket is fixed by the fixture)", args[0])
	}
	if args[0] == "kill-server" || args[0] == "start-server" || args[0] == "source-file" {
		return rejectf("%s is never allowed for fixtures", args[0])
	}
	for _, a := range args {
		// tmux treats an argv element ending in an unescaped ";" as a
		// command separator; that could chain e.g. kill-server.
		if strings.HasSuffix(a, ";") && !strings.HasSuffix(a, `\;`) {
			return rejectf("command separators are not allowed (%q)", a)
		}
	}
	return nil
}

// ValidateArgv checks a complete tmux argv just before execution: it must
// start with -S <socket> pointing inside the owned dir, carry no other
// global options, and never address a default/inherited server.
func ValidateArgv(argv []string, socket, dir string) error {
	if len(argv) < 3 || argv[0] != "-S" {
		return rejectf("tmux argv must start with -S <fixture socket>")
	}
	if argv[1] != socket {
		return rejectf("socket %q is not the fixture socket", argv[1])
	}
	if err := checkSocketPath(argv[1], dir); err != nil {
		return err
	}
	return validateArgs(argv[2:])
}

// checkSocketPath rejects empty, relative, default, inherited, or unowned
// socket paths.
func checkSocketPath(socket, dir string) error {
	if socket == "" {
		return rejectf("missing socket")
	}
	if !filepath.IsAbs(socket) {
		return rejectf("socket %q must be absolute", socket)
	}
	if dir == "" || filepath.Dir(socket) != filepath.Clean(dir) {
		return rejectf("socket %q is outside the fixture directory %q", socket, dir)
	}
	if filepath.Base(socket) == "default" {
		return rejectf("socket %q looks like a default tmux socket", socket)
	}
	for _, p := range protectedSockets() {
		if samePath(p, socket) {
			return rejectf("socket %q is a protected (default or inherited) tmux socket", socket)
		}
	}
	return nil
}

// protectedSockets lists the default server sockets for this user and the
// socket of any surrounding tmux (from $TMUX), resolved without running tmux.
func protectedSockets() []string {
	uid := fmt.Sprintf("tmux-%d", os.Getuid())
	var out []string
	for _, d := range []string{os.Getenv("TMUX_TMPDIR"), "/tmp", "/private/tmp", os.TempDir()} {
		if d != "" {
			out = append(out, filepath.Join(d, uid, "default"))
		}
	}
	if t := os.Getenv("TMUX"); t != "" {
		sock, _, _ := strings.Cut(t, ",")
		if sock != "" {
			out = append(out, sock)
		}
	}
	return out
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, ea := evalDir(a)
	rb, eb := evalDir(b)
	return ea == nil && eb == nil && ra == rb
}

// evalDir resolves symlinks in the parent directory (the socket itself may
// not exist yet).
func evalDir(p string) (string, error) {
	d, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return "", err
	}
	return filepath.Join(d, filepath.Base(p)), nil
}

func (f *Fixture) verifiedOwner() (*Owner, error) {
	o, err := readOwner(f.dir)
	if err != nil {
		return nil, rejectf("fixture ownership record unreadable: %v", err)
	}
	if o.Nonce != f.nonce || o.Socket != f.socket || o.Dir != f.dir {
		return nil, rejectf("fixture ownership record does not match this fixture")
	}
	return o, nil
}

func readOwner(dir string) (*Owner, error) {
	data, err := os.ReadFile(filepath.Join(dir, ownerFile))
	if err != nil {
		return nil, err
	}
	var o Owner
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

func writeOwner(dir string, o *Owner) error {
	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ownerFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, ownerFile))
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
