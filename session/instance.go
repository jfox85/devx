package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

var instanceIDRe = regexp.MustCompile(`^si_[0-9a-f]{24}$`)

// NewInstanceID returns a fresh random session instance id ("si_" + 96 bits).
func NewInstanceID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return "si_" + hex.EncodeToString(b)
}

// ValidInstanceID reports whether id is a well-formed session instance id.
func ValidInstanceID(id string) bool { return instanceIDRe.MatchString(id) }

// MatchesBoundInstance reports whether record s is the session instance an
// agent was bound to (boundID, recorded with the record's created_at as
// boundCreatedAt). It is the single identity rule:
//   - the record carries an id: it must equal boundID;
//   - the record has no id (an older DevX writer dropped the field): its
//     created_at must equal boundCreatedAt to the nanosecond (older writers
//     preserve created_at; a recreated record gets a new one).
//
// An empty boundID never matches (unbound agents are not identified here).
func MatchesBoundInstance(s *Session, boundID string, boundCreatedAt time.Time) bool {
	if s == nil || boundID == "" {
		return false
	}
	if s.InstanceID != "" {
		return s.InstanceID == boundID
	}
	return !boundCreatedAt.IsZero() && s.CreatedAt.Equal(boundCreatedAt)
}

// DeriveInstanceID returns the instance id the migration assigns to an
// EXISTING record that has none. It is a deterministic hash of the record's
// identity (name, path, exact created_at), so a read-only dry run shows the
// exact ids and plan hash that --apply will write, with no key or other state.
// Instance ids are identifiers, not secrets or capabilities: no caller ever
// supplies one, they are only compared against DevX's own records. A record
// recreated under the same name and path has a different created_at (to the
// nanosecond), hence a different id. New records get random ids
// (NewInstanceID).
func DeriveInstanceID(name, path string, createdAt time.Time) string {
	h := sha256.Sum256([]byte("devx-session-instance|v1|" + name + "|" + path + "|" + createdAt.UTC().Format(time.RFC3339Nano)))
	return "si_" + hex.EncodeToString(h[:])[:24]
}
