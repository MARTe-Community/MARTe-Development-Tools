package integration

import (
	"testing"
	"time"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// newTestNode builds a child node wired into parent.
func newTestNode(parent *index.ProjectNode, name string) *index.ProjectNode {
	n := &index.ProjectNode{
		Name:     name,
		RealName: name,
		Children: make(map[string]*index.ProjectNode),
		Parent:   parent,
	}
	if parent.Children == nil {
		parent.Children = make(map[string]*index.ProjectNode)
	}
	parent.Children[name] = n
	return n
}

// objectFragment registers a fragment whose source span is start..end.
func objectFragment(node *index.ProjectNode, file string, startLine, endLine int) {
	node.Fragments = append(node.Fragments, &index.Fragment{
		File:      file,
		IsObject:  true,
		ObjectPos: parser.Position{Line: startLine, Column: 1},
		EndPos:    parser.Position{Line: endLine, Column: 1},
	})
}

// TestReferenceResolvesToInnermostScope pins the container lookup used during
// reference resolution: a name is resolved against the innermost node whose
// source span contains the reference. The lookup is indexed by file (walking
// the whole tree per reference made edits quadratic), so this guards the
// semantics that optimization had to preserve.
func TestReferenceResolvesToInnermostScope(t *testing.T) {
	pt := index.NewProjectTree()
	root := pt.Root

	const file = "a.marte"
	outer := newTestNode(root, "Outer")
	objectFragment(outer, file, 1, 60)
	outerX := newTestNode(outer, "X")

	inner := newTestNode(outer, "Inner")
	objectFragment(inner, file, 10, 40)
	innerX := newTestNode(inner, "X")

	// Reference at line 20: inside both spans, so the inner one wins.
	pt.IndexValue(file, &parser.ReferenceValue{
		Position: parser.Position{Line: 20, Column: 5},
		Value:    "X",
	})
	pt.ResolveReferences(nil)

	refs := pt.FileReferences[file]
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(refs))
	}
	if refs[0].Target != innerX {
		t.Errorf("reference resolved to %v, want the innermost X", refs[0].Target)
	}
	if refs[0].Target == outerX {
		t.Error("reference resolved to the outer shadowed X")
	}
}

// TestReferenceOutsideEveryObjectUsesFileScope covers the fallback: a
// reference that is not inside any object span resolves in the scope of the
// node owning the file-level fragment.
func TestReferenceOutsideEveryObjectUsesFileScope(t *testing.T) {
	pt := index.NewProjectTree()
	root := pt.Root

	const file = "b.marte"
	// Root owns the file-level fragment.
	root.Fragments = append(root.Fragments, &index.Fragment{File: file})
	top := newTestNode(root, "Top")
	objectFragment(top, file, 5, 10)
	topChild := newTestNode(top, "Y")

	// Line 1 is before the object's span (5..10): file scope applies, so the
	// reference resolves against Root's children.
	pt.IndexValue(file, &parser.ReferenceValue{
		Position: parser.Position{Line: 1, Column: 1},
		Value:    "Top",
	})
	pt.ResolveReferences(nil)

	refs := pt.FileReferences[file]
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(refs))
	}
	if refs[0].Target != top {
		t.Errorf("file-level reference resolved to %v, want Top", refs[0].Target)
	}

	// Inside the object's span the object itself is the container, so its own
	// child resolves.
	pt.IndexValue(file, &parser.ReferenceValue{
		Position: parser.Position{Line: 7, Column: 3},
		Value:    "Y",
	})
	pt.ResolveReferences(nil)
	refs = pt.FileReferences[file]
	if len(refs) != 2 {
		t.Fatalf("expected 2 references, got %d", len(refs))
	}
	if refs[1].Target != topChild {
		t.Errorf("nested reference resolved to %v, want Top.Y", refs[1].Target)
	}
}

// TestReferenceInIsolatedFileResolvesLocally covers the second root set: files
// excluded from the project scan are indexed in IsolatedFiles and must resolve
// against their own subtree.
func TestReferenceInIsolatedFileResolvesLocally(t *testing.T) {
	pt := index.NewProjectTree()

	const file = "loose.marte"
	isolated := &index.ProjectNode{
		Name:     "Loose",
		RealName: "Loose",
		Children: make(map[string]*index.ProjectNode),
		Parent:   nil,
	}
	objectFragment(isolated, file, 1, 20)
	local := newTestNode(isolated, "Local")
	pt.IsolatedFiles[file] = isolated

	pt.IndexValue(file, &parser.ReferenceValue{
		Position: parser.Position{Line: 5, Column: 2},
		Value:    "Local",
	})
	pt.ResolveReferences(nil)

	refs := pt.FileReferences[file]
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(refs))
	}
	if refs[0].Target != local {
		t.Errorf("isolated reference resolved to %v, want Local", refs[0].Target)
	}
}

// TestReferenceInConditionalBranchWithoutFilterStillResolves documents that
// resolution without an active-fragment filter keeps conditional references.
func TestReferenceInConditionalBranchWithoutFilterStillResolves(t *testing.T) {
	pt := index.NewProjectTree()
	root := pt.Root

	const file = "c.marte"
	root.Fragments = append(root.Fragments, &index.Fragment{File: file})
	cond := newTestNode(root, "Cond")
	objectFragment(cond, file, 3, 8)
	cond.Fragments[0].IsConditional = true
	target := newTestNode(cond, "Z")

	pt.IndexValue(file, &parser.ReferenceValue{
		Position: parser.Position{Line: 4, Column: 1},
		Value:    "Z",
	})
	pt.ResolveReferences(nil)

	refs := pt.FileReferences[file]
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(refs))
	}
	if refs[0].Target != target {
		t.Errorf("conditional reference resolved to %v, want Cond.Z", refs[0].Target)
	}
}

// BenchmarkResolveReferencesLargeProject measures resolution on the 36-file
// example project. Resolution used to walk the whole tree once per reference
// (~400ms per keystroke in the LSP); it should stay in the millisecond range.
func BenchmarkResolveReferencesLargeProject(b *testing.B) {
	pt := index.NewProjectTree()
	if err := pt.ScanDirectory("../examples/big_project"); err != nil {
		b.Fatalf("scan example project: %v", err)
	}
	pt.ResolveReferences(nil)
	if len(pt.References) == 0 {
		b.Fatal("no references indexed")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pt.ResolveReferences(nil)
	}
}

// TestResolveReferencesLargeProjectStaysFast guards against the quadratic
// container lookup returning: resolving every reference used to walk the whole
// tree, which cost ~400ms on this project and made each keystroke in the LSP
// feel stuck. The threshold is ~25x the observed cost, so it only trips on a
// real regression, not on a slow machine.
func TestResolveReferencesLargeProjectStaysFast(t *testing.T) {
	pt := index.NewProjectTree()
	if err := pt.ScanDirectory("../examples/big_project"); err != nil {
		t.Fatalf("scan example project: %v", err)
	}
	pt.ResolveReferences(nil) // warm-up
	if len(pt.References) == 0 {
		t.Fatal("no references indexed")
	}

	start := time.Now()
	pt.ResolveReferences(nil)
	elapsed := time.Since(start)
	if elapsed > 150*time.Millisecond {
		t.Errorf("ResolveReferences took %v for %d references; container lookup is likely walking the whole tree again", elapsed, len(pt.References))
	}
}
