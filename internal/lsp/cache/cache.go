package cache

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

type Session struct {
	id    string
	views []*View
	mu    sync.Mutex
}

func NewSession(id string) *Session {
	return &Session{
		id: id,
	}
}

func (s *Session) Views() []*View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.views
}

func (s *Session) View(id string) *View {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.views {
		if v.id == id {
			return v
		}
	}
	return nil
}

// ViewOf returns the view that contains the given file URI.
func (s *Session) ViewOf(uri string) *View {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	path := strings.TrimPrefix(uri, "file://")
	
	var best *View
	longest := -1
	
	for _, v := range s.views {
		// Simple prefix match. Ideally check path separators.
		if strings.HasPrefix(path, v.root) {
			if len(v.root) > longest {
				longest = len(v.root)
				best = v
			}
		}
	}
	
	if best != nil {
		return best
	}

	// Fallback to first view if no match (e.g. file outside workspace, or root "/")
	if len(s.views) > 0 {
		return s.views[0]
	}
	return nil
}

func (s *Session) CreateView(id, root string) *View {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	v := &View{
		id:      id,
		root:    root,
		session: s,
	}
	// Initialize empty snapshot
	v.snapshot.Store(&Snapshot{
		view:      v,
		tree:      index.NewProjectTree(),
		documents: make(map[string]string),
	})
	
	s.views = append(s.views, v)
	return v
}

type View struct {
	id       string
	root     string
	session  *Session
	snapshot atomic.Value // *Snapshot
}

func (v *View) Snapshot() *Snapshot {
	return v.snapshot.Load().(*Snapshot)
}

// SetSnapshot sets the new snapshot. 
// Ideally this is done via "Invalidate" which produces a new snapshot from the old one.
func (v *View) SetSnapshot(s *Snapshot) {
	v.snapshot.Store(s)
}

func (v *View) Root() string {
	return v.root
}

type Snapshot struct {
	view         *View
	tree         *index.ProjectTree
	schema       *schema.Schema
	documents    map[string]string
	refCount     sync.WaitGroup // To track active usages? (Simplified: Go GC handles memory, we just need consistency)
}

func (s *Snapshot) Tree() *index.ProjectTree {
	return s.tree
}

func (s *Snapshot) View() *View {
	return s.view
}

func (s *Snapshot) Documents() map[string]string {
	return s.documents
}

func (s *Snapshot) Schema() *schema.Schema {
	return s.schema
}

// Clone creates a deep copy of the snapshot (and the underlying tree).
// This is used when modifying the state.
func (s *Snapshot) Clone(ctx context.Context) *Snapshot {
	newSnap := &Snapshot{
		view:      s.view,
		tree:      s.tree.Clone(), // This is the heavy part
		schema:    s.schema,       // Schema is likely static or reloaded separately
		documents: make(map[string]string),
	}
	for k, v := range s.documents {
		newSnap.documents[k] = v
	}
	return newSnap
}
