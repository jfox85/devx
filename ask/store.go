package ask

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
)

type Store struct {
	dir string
}

var requestIDPattern = regexp.MustCompile(`^req_[0-9a-f]+$`)

func NewStore() *Store {
	return &Store{dir: filepath.Join(filepath.Dir(config.GetSessionsPath()), "asks")}
}

func NewStoreAt(dir string) *Store { return &Store{dir: dir} }

func (s *Store) Dir() string { return s.dir }

func (s *Store) Create(fromSession, toSession, fromPath, toPath, question string) (*Request, error) {
	if strings.TrimSpace(question) == "" {
		return nil, fmt.Errorf("question cannot be empty")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	req := &Request{
		ID:          newID(),
		FromSession: fromSession,
		ToSession:   toSession,
		FromPath:    fromPath,
		ToPath:      toPath,
		Question:    strings.TrimSpace(question),
		Status:      StatusPendingApproval,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return req, s.Save(req)
}

func (s *Store) Save(req *Request) error {
	if req == nil || req.ID == "" {
		return fmt.Errorf("request id is required")
	}
	if err := validateRequestID(req.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	req.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", s.path(req.ID), os.Getpid())
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(req.ID))
}

func (s *Store) ApproveAndExecute(ctx context.Context, id string, policy Policy) (*Request, error) {
	return s.approveAndExecute(ctx, id, policy, false)
}

func (s *Store) ApproveAlwaysAndExecute(ctx context.Context, id string, policy Policy) (*Request, error) {
	return s.approveAndExecute(ctx, id, policy, true)
}

func (s *Store) approveAndExecute(ctx context.Context, id string, policy Policy, allowFuture bool) (*Request, error) {
	var result *Request
	err := s.withRequestLock(id, func() error {
		req, err := s.Get(id)
		if err != nil {
			return err
		}
		if req.Status != StatusPendingApproval {
			return fmt.Errorf("request %s is %s, not pending approval", req.ID, req.Status)
		}
		sessions, err := session.LoadSessions()
		if err != nil {
			return err
		}
		target, ok := sessions.GetSession(req.ToSession)
		if !ok {
			return fmt.Errorf("session %q not found", req.ToSession)
		}
		updated, err := Execute(ctx, req, target, ExecuteOptions{Policy: policy, Store: s})
		if err == nil && allowFuture {
			if allowErr := s.AllowFuture(req.FromSession, req.ToSession, req.FromPath, req.ToPath); allowErr != nil {
				log.Printf("warning: remember ask approval %s failed: %v", req.ID, allowErr)
			}
		}
		result = updated
		return err
	})
	return result, err
}

func (s *Store) Deny(id string) (*Request, error) {
	var result *Request
	err := s.withRequestLock(id, func() error {
		req, err := s.Get(id)
		if err != nil {
			return err
		}
		if req.Status != StatusPendingApproval {
			return fmt.Errorf("request %s is %s, not pending approval", req.ID, req.Status)
		}
		req.Status = StatusDenied
		if err := s.Save(req); err != nil {
			return err
		}
		result = req
		return nil
	})
	return result, err
}

func (s *Store) withRequestLock(id string, fn func() error) error {
	if err := validateRequestID(id); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	lockPath := filepath.Join(s.dir, id+".lock")
	release, err := acquireFileLock(lockPath)
	if err != nil {
		return fmt.Errorf("request %s is locked: %w", id, err)
	}
	defer release()
	return fn()
}

func (s *Store) Get(id string) (*Request, error) {
	if err := validateRequestID(id); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, fmt.Errorf("read ask %s: %w", id, err)
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parse ask %s: %w", id, err)
	}
	return &req, nil
}

func (s *Store) List() ([]*Request, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Request
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read ask %s: %w", entry.Name(), err)
		}
		var req Request
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, fmt.Errorf("parse ask %s: %w", entry.Name(), err)
		}
		out = append(out, &req)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) Pending() ([]*Request, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var pending []*Request
	for _, req := range all {
		if req.Status == StatusPendingApproval {
			pending = append(pending, req)
		}
	}
	return pending, nil
}

func (s *Store) IsAllowed(fromSession, toSession, fromPath, toPath string) (bool, error) {
	approvals, err := s.loadApprovals()
	if err != nil {
		return false, err
	}
	for _, approval := range approvals.Approvals {
		if approval.FromSession == fromSession && approval.ToSession == toSession && approval.FromPath == fromPath && approval.ToPath == toPath {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) AllowFuture(fromSession, toSession, fromPath, toPath string) error {
	return s.withApprovalsLock(func() error {
		approvals, err := s.loadApprovals()
		if err != nil {
			return err
		}
		for _, approval := range approvals.Approvals {
			if approval.FromSession == fromSession && approval.ToSession == toSession && approval.FromPath == fromPath && approval.ToPath == toPath {
				return nil
			}
		}
		approvals.Approvals = append(approvals.Approvals, Approval{FromSession: fromSession, ToSession: toSession, FromPath: fromPath, ToPath: toPath, CreatedAt: time.Now().UTC()})
		return s.saveApprovals(approvals)
	})
}

func (s *Store) Approvals() (*ApprovalStore, error) { return s.loadApprovals() }

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

func validateRequestID(id string) error {
	if !requestIDPattern.MatchString(id) {
		return fmt.Errorf("invalid request id %q", id)
	}
	return nil
}

func (s *Store) approvalsPath() string {
	return filepath.Join(filepath.Dir(s.dir), "ask_approvals.json")
}

func (s *Store) loadApprovals() (*ApprovalStore, error) {
	data, err := os.ReadFile(s.approvalsPath())
	if os.IsNotExist(err) {
		return &ApprovalStore{}, nil
	}
	if err != nil {
		return nil, err
	}
	var approvals ApprovalStore
	if err := json.Unmarshal(data, &approvals); err != nil {
		return nil, err
	}
	return &approvals, nil
}

func (s *Store) saveApprovals(approvals *ApprovalStore) error {
	path := s.approvalsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(approvals, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) withApprovalsLock(fn func() error) error {
	path := s.approvalsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	release, err := acquireFileLock(lockPath)
	if err != nil {
		return fmt.Errorf("ask approvals are locked: %w", err)
	}
	defer release()
	return fn()
}

func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(b[:])
}
