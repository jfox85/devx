package session

import (
	"crypto/hmac"
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

// DeriveInstanceID returns an instance id for an EXISTING record that has
// none, derived with a secret random key so the same record state yields the
// same id (a reviewed migration plan stays valid until something changes)
// while ids are not guessable from names or paths. key must be random and
// kept private to the owner.
func DeriveInstanceID(key []byte, name, path string, createdAt time.Time) string {
	m := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(m, "devx-session-instance|v1|%s|%s|%s", name, path, createdAt.UTC().Format(time.RFC3339Nano))
	return "si_" + hex.EncodeToString(m.Sum(nil))[:24]
}
