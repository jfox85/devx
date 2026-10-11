package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/piagent"
)

// instanceRunPrefix names the per-run backup directory --apply creates.
const instanceRunPrefix = "session-instances-"

// instanceRunsBase is where --apply creates its per-run backup directories.
func instanceRunsBase() string {
	return filepath.Join(filepath.Dir(config.GetSessionsPath()), "backups")
}

// trustedInstanceRun is a verified --apply run directory: its plan is
// intact, names the directory's hash, and every journal entry is a write in
// that plan.
type trustedInstanceRun struct {
	Dir     string
	Plan    *piagent.InstancePlan
	Entries []piagent.JournalEntry
}

// checkRunDir refuses a path that is not a real directory (no symlink)
// owned by the current user and not writable by group or others.
func checkRunDir(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory (symlinks are refused)", path)
	}
	if err := checkRunPerms(fi); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// readRunFile opens path without following a final symlink, checks the
// opened file is a regular file owned by the current user and not writable
// by group or others, and returns its contents read from that same handle.
func readRunFile(path string) ([]byte, error) {
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file (symlinks are refused)")
	}
	f, err := openRunFileNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if err := checkRunPerms(fi); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// instanceRunHash returns the plan hash embedded in a run directory name
// (session-instances-<time>-<hash>-<random>).
func instanceRunHash(name string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(name, instanceRunPrefix), "-")
	if !strings.HasPrefix(name, instanceRunPrefix) || len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	return parts[1], true
}

// parseInstanceJournal parses newline-delimited entries. Apply writes each
// entry and its newline in one write, so only a final line WITHOUT a
// trailing newline can be torn by a crash mid-append (that write never
// happened, because the journal precedes it); it is dropped. Any other
// malformed line means the journal was altered, and is refused.
func parseInstanceJournal(data []byte) ([]piagent.JournalEntry, error) {
	lines := bytes.Split(data, []byte("\n"))
	last := len(lines) - 1
	for last >= 0 && len(bytes.TrimSpace(lines[last])) == 0 {
		last--
	}
	tornTail := !bytes.HasSuffix(bytes.TrimRight(data, " \t\r"), []byte("\n"))
	var entries []piagent.JournalEntry
	for i := 0; i <= last; i++ {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var e piagent.JournalEntry
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil || dec.More() {
			if i == last && tornTail {
				break
			}
			return nil, fmt.Errorf("journal line %d is malformed (only an unterminated final line is a torn append)", i+1)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// loadTrustedInstanceRun accepts only a run directory --apply created: a
// session-instances-* directory directly inside the owner's backups
// directory, both real directories owned by the current user and not
// writable by others, holding a trusted plan.json whose verified hash is
// the one in the directory name, and a trusted journal.jsonl whose every
// entry is a write in that plan. Anything else is refused before any
// record is read or written.
func loadTrustedInstanceRun(dir string) (*trustedInstanceRun, error) {
	if config.GetSessionsPath() == "" {
		return nil, errors.New("cannot determine the home directory, so the backups directory is unknown")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	base, err := filepath.Abs(instanceRunsBase())
	if err != nil {
		return nil, err
	}
	// Lexical match only: a symlinked path to a run elsewhere is refused
	// by checkRunDir, and a symlinked base by the base check.
	if filepath.Dir(abs) != filepath.Clean(base) {
		return nil, fmt.Errorf("%s is not a run directory directly under %s", dir, base)
	}
	runHash, ok := instanceRunHash(filepath.Base(abs))
	if !ok {
		return nil, fmt.Errorf("%s is not a %s* run directory created by --apply", dir, instanceRunPrefix)
	}
	if err := checkRunDir(base); err != nil {
		return nil, fmt.Errorf("backups directory: %w", err)
	}
	if err := checkRunDir(abs); err != nil {
		return nil, fmt.Errorf("run directory: %w", err)
	}
	planData, err := readRunFile(filepath.Join(abs, "plan.json"))
	if err != nil {
		return nil, fmt.Errorf("plan.json in %s: %w", dir, err)
	}
	var plan piagent.InstancePlan
	if err := json.Unmarshal(planData, &plan); err != nil || !plan.VerifyHash() {
		return nil, fmt.Errorf("plan.json in %s is not an intact instance plan", dir)
	}
	if plan.Hash != runHash {
		return nil, fmt.Errorf("plan.json in %s is plan %s, but the directory belongs to plan %s", dir, plan.Hash, runHash)
	}
	journalData, err := readRunFile(filepath.Join(abs, "journal.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("journal in %s: %w", dir, err)
	}
	entries, err := parseInstanceJournal(journalData)
	if err != nil {
		return nil, fmt.Errorf("journal in %s: %w", dir, err)
	}
	if err := piagent.ValidateInstanceJournal(&plan, entries); err != nil {
		if errors.Is(err, piagent.ErrJournalNotInPlan) {
			return nil, fmt.Errorf("journal in %s does not match its plan %s; nothing rolled back: %w", dir, plan.Hash, err)
		}
		return nil, err
	}
	return &trustedInstanceRun{Dir: abs, Plan: &plan, Entries: entries}, nil
}
