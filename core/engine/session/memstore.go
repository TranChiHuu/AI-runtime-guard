package session

import (
	"sort"

	"github.com/airuntimeguard/core/domain"
)

// MemStore keeps live sessions in memory. SQLite is durability and history, not
// the working set (docs/ARCHITECTURE.md §5), so this is the store the decision
// path actually runs against.
//
// It carries no lock of its own: the engine serializes every access, and a
// second layer of locking would only invite the illusion that this is safe to
// use directly.
type MemStore struct {
	sessions map[string]*domain.Session
}

func NewMemStore() *MemStore {
	return &MemStore{sessions: map[string]*domain.Session{}}
}

func (m *MemStore) Load(id string) (*domain.Session, bool) {
	s, ok := m.sessions[id]
	return s, ok
}

func (m *MemStore) Save(s *domain.Session) { m.sessions[s.ID] = s }

func (m *MemStore) Delete(id string) { delete(m.sessions, id) }

// List returns sessions ordered by start time, then id. Deterministic order
// matters: `guard status` output and replay comparisons both depend on it.
func (m *MemStore) List() []*domain.Session {
	out := make([]*domain.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out
}
