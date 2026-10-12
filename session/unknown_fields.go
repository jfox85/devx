package session

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// Forward-compatible persistence of sessions.json.
//
// Every sessions.json mutation is a full read-modify-write. A writer whose
// Session struct predates a field silently drops it from EVERY record on any
// write: this is how builds without the local-only marker stripped
// `local_only` (and `instance_id`) from records created by newer builds.
//
// From this version on, fields this build does not know are kept and written
// back unchanged at three levels: the top level of the store, each session
// record, and the local-only marker object (the ownership marker itself).
// Other nested objects (target, target.gatepost, review) are not covered.
// This only protects records from writers that include this code:
// a binary built before it (for example any build of origin/main before this
// change landed there) still drops unknown fields, and nothing a newer build
// writes can prevent that. See docs/session-marker-repair.md.

var knownFieldsCache sync.Map // reflect.Type -> map[string]bool

// retiredSessionFields are record members a DevX build deliberately
// removed. They are dropped on the next write instead of preserved; removing
// a field is an explicit decision, never a side effect of an older schema.
var retiredSessionFields = map[string]bool{
	"color": true, // session identity colors (feat!: remove session identity colors)
}

// knownJSONFields returns the JSON keys encoding/json maps to struct fields
// of t (top-level fields only; these structs embed none).
func knownJSONFields(t reflect.Type) []string {
	if v, ok := knownFieldsCache.Load(t); ok {
		return v.([]string)
	}
	var known []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			name = f.Name
		}
		known = append(known, name)
	}
	knownFieldsCache.Store(t, known)
	return known
}

// splitUnknown returns the members of the JSON object data whose keys are
// not fields of t, or nil if there are none.
func splitUnknown(data []byte, t reflect.Type, retired map[string]bool) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	known := knownJSONFields(t)
	var extra map[string]json.RawMessage
	for k, v := range all {
		if matchesFold(known, k) || retired[strings.ToLower(k)] {
			continue
		}
		if extra == nil {
			extra = map[string]json.RawMessage{}
		}
		extra[k] = append(json.RawMessage(nil), v...)
	}
	return extra, nil
}

// matchesFold reports whether encoding/json would decode key k into one of
// the known fields. encoding/json matches names case-insensitively with
// Unicode simple folding, which strings.EqualFold implements.
func matchesFold(known []string, k string) bool {
	for _, n := range known {
		if strings.EqualFold(n, k) {
			return true
		}
	}
	return false
}

// mergeUnknown adds the preserved unknown members to the encoded object
// base. Known fields always win (an unknown key never shadows one).
func mergeUnknown(base []byte, extra map[string]json.RawMessage) ([]byte, error) {
	if len(extra) == 0 {
		return base, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range extra {
		if _, exists := m[k]; exists {
			continue
		}
		// Compact so a value re-indented by MarshalIndent is still valid.
		var buf bytes.Buffer
		if err := json.Compact(&buf, v); err != nil {
			return nil, err
		}
		m[k] = buf.Bytes()
	}
	return json.Marshal(m)
}

type sessionNoMethods Session

// UnmarshalJSON decodes a session record and keeps every member this build
// does not know in Unknown, so it is written back unchanged.
func (s *Session) UnmarshalJSON(data []byte) error {
	var plain sessionNoMethods
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	extra, err := splitUnknown(data, reflect.TypeOf(plain), retiredSessionFields)
	if err != nil {
		return err
	}
	*s = Session(plain)
	s.Unknown = extra
	return nil
}

// MarshalJSON encodes the record plus any preserved unknown members.
func (s Session) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(sessionNoMethods(s))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(base, s.Unknown)
}

type localOnlyNoMethods LocalOnlyMeta

// UnmarshalJSON decodes a local-only marker and keeps unknown members, so a
// field a newer build adds to the marker survives this build's writes (and a
// marker restored from a backup keeps it).
func (m *LocalOnlyMeta) UnmarshalJSON(data []byte) error {
	var plain localOnlyNoMethods
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	extra, err := splitUnknown(data, reflect.TypeOf(plain), nil)
	if err != nil {
		return err
	}
	*m = LocalOnlyMeta(plain)
	m.Unknown = extra
	return nil
}

// MarshalJSON encodes the marker plus any preserved unknown members.
func (m LocalOnlyMeta) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(localOnlyNoMethods(m))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(base, m.Unknown)
}

type storeNoMethods SessionStore

// UnmarshalJSON decodes the store and keeps unknown top-level members.
func (s *SessionStore) UnmarshalJSON(data []byte) error {
	var plain storeNoMethods
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	extra, err := splitUnknown(data, reflect.TypeOf(plain), nil)
	if err != nil {
		return err
	}
	*s = SessionStore(plain)
	s.Unknown = extra
	return nil
}

// MarshalJSON encodes the store plus any preserved unknown top-level members.
func (s SessionStore) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(storeNoMethods(s))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(base, s.Unknown)
}
