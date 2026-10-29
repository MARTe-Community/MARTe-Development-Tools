package validator

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/loader"
	"github.com/marte-community/marte-dev-tools/internal/parser"

	"github.com/marte-community/marte-dev-tools/internal/schema"
)

type DiagnosticLevel int

const (
	LevelError DiagnosticLevel = iota
	LevelWarning
)

type Diagnostic struct {
	Level    DiagnosticLevel
	Message  string
	Position parser.Position
	File     string
}

// withInstance records where a with-block was reached when its path could not
// be evaluated, so unresolved paths can be reported after activation.
type withInstance struct {
	ctx  *index.EvaluationContext
	file string
	node *index.ProjectNode
}

// assertInstance records where an assert was reached so it can be
// evaluated with the right scope and reported at its own position.
type assertInstance struct {
	ctx  *index.EvaluationContext
	file string
	node *index.ProjectNode
}

type Validator struct {
	Diagnostics []Diagnostic
	Tree        *index.ProjectTree
	// ProjectRoot is the workspace directory (from -P or the first file); it
	// anchors relative document paths when no git root or marte_project.json
	// marks the project.
	ProjectRoot string
	Schema      *schema.Schema
	// MissingFilesAreWarnings makes a failed `with` document load a warning
	// whose binding resolves to an empty document, instead of an error.
	// Editors and `mdt check` set it; `mdt build` keeps the strict default so
	// a missing file fails the build.
	MissingFilesAreWarnings bool

	reportedLoadFailures map[string]bool

	Overrides       map[string]parser.Value
	Variables       map[string]parser.Value
	RawOverrides    map[string]string
	mu              sync.Mutex
	ActiveNodes     map[*index.ProjectNode]bool
	ActiveFragments map[*index.Fragment]bool
	// pendingAsserts holds the assert statements reached by activation,
	// keyed by definition. They are evaluated after the final activation
	// pass, once every variable has settled (later passes overwrite the
	// evaluation context, so the last one wins).
	// sizeMismatch marks GAMs whose detailed input/output size error was
	// reported, so the generic CUE InputSize/OutputSize diagnostics are not
	// repeated for the same node.
	sizeMismatch map[*index.ProjectNode]bool
	// pendingWith collects with-blocks whose path could not be evaluated in
	// the current scope; reportDeferredWiths reports the ones that never
	// resolved, so a typo still fails while a loop body visited outside its
	// iteration does not.
	pendingWith     map[*parser.WithBlock]withInstance
	loadedWith      map[*parser.WithBlock]bool
	pendingAsserts  map[*parser.AssertStatement]assertInstance
	reportedAsserts map[*parser.AssertStatement]bool
	// visited tracks (child node -> source definition) pairs already
	// expanded by activation, keeping repeated passes idempotent.
	visited map[*index.ProjectNode]map[parser.Definition]bool
	// baseCtx is the outermost evaluation context of the current activation.
	// Unscoped (index-time) fragments belong to it; they must be evaluated
	// with it, not with whatever loop/with context a traversal happens to
	// carry (that both loses their fields and stamps wrong scopes).
	baseCtx *index.EvaluationContext
	// fileFragments indexes every fragment by file after activation, so
	// position-activity queries don't walk the whole tree per reference
	// (that was quadratic on projects with many references).
	fileFragments map[string][]*index.Fragment
	// expandedScopes records (node, scope) pairs already walked in this
	// activation pass. Contexts are recreated on every visit, so keying by
	// pointer (or relying on the per-call map only) re-expands the same
	// subtree with a fresh pointer indefinitely on projects with several
	// `with`/`foreach` blocks.
	expandedScopes map[*index.ProjectNode]map[string]bool
	// loadCache memoizes with-block document loads by absolute path.
	loadCache map[string]parser.Value
	muActive  sync.Mutex
}

func NewValidator(tree *index.ProjectTree, projectRoot string, overrides map[string]string) *Validator {
	v := &Validator{
		Tree:            tree,
		Schema:          schema.LoadFullSchema(projectRoot),
		Overrides:       make(map[string]parser.Value),
		Variables:       make(map[string]parser.Value),
		ActiveNodes:     make(map[*index.ProjectNode]bool),
		ActiveFragments: make(map[*index.Fragment]bool),
		pendingAsserts:  make(map[*parser.AssertStatement]assertInstance),
		reportedAsserts: make(map[*parser.AssertStatement]bool),
		visited:         make(map[*index.ProjectNode]map[parser.Definition]bool),
	}

	for name, valStr := range overrides {
		v.setOverrideValue(name, valStr, nil)
	}

	// Also collect variables from Tree
	tree.Walk(func(n *index.ProjectNode) {
		for k, varInfo := range n.Variables {
			if _, ok := v.Variables[k]; !ok || varInfo.Def.IsConst {
				v.Variables[k] = varInfo.Def.DefaultValue
			}

			if valStr, ok := overrides[k]; ok {
				v.setOverrideValue(k, valStr, varInfo.Def)
			}
		}
	})

	return v
}

// activateBranchChildren marks the fragments of the node's conditional
// children that belong to branchID. Signal shorthands live as child nodes
// (one per signal) rather than as definitions of the container, so the
// branch activation that walks container fragments would otherwise leave
// them inactive and validation could not tell which branch's signal is in
// effect.
func (v *Validator) activateBranchChildren(node *index.ProjectNode, branchID string) {
	v.muActive.Lock()
	defer v.muActive.Unlock()
	for _, child := range node.Children {
		for _, f := range child.Fragments {
			if f.IsConditional && f.BranchID == branchID {
				v.ActiveFragments[f] = true
			}
		}
	}
}

// nodeActive reports whether the node has a fragment activated by the
// current evaluation. Nodes without fragments (synthesized containers) and
// nodes whose fragments were never classified count as active.
func (v *Validator) nodeActive(node *index.ProjectNode) bool {
	if len(node.Fragments) == 0 {
		return true
	}
	v.muActive.Lock()
	defer v.muActive.Unlock()
	for _, f := range node.Fragments {
		if v.ActiveFragments[f] {
			return true
		}
	}
	return false
}

// isGAMSignalsContainer reports whether node is the InputSignals/OutputSignals
// container of a GAM, where inactive conditional children must not be
// validated (they belong to the other branch of the enclosing if/else).
func (v *Validator) isGAMSignalsContainer(node *index.ProjectNode) bool {
	if node.Parent == nil || !v.Tree.IsGAM(node.Parent) {
		return false
	}
	return node.RealName == "InputSignals" || node.RealName == "OutputSignals"
}

// pruneShadowedFragments removes index-time fragments (no scope) that are
// duplicated by a materialized copy of the same definition. The duplicates
// would otherwise be evaluated with whatever scope happens to be current,
// leaking one loop iteration's variables into another (cross-named signals).
type fragmentKey struct {
	src    parser.Definition
	scope  string
	branch string
}

func pruneShadowedFragments(node *index.ProjectNode, isActive func(*index.Fragment) bool, deactivate func(*index.Fragment)) {
	if node == nil {
		return
	}
	seen := make(map[fragmentKey]bool)
	scoped := make(map[parser.Definition]bool)
	for _, f := range node.Fragments {
		if f.Source != nil && f.EvalCtx != nil && f.EvalCtx.Scope != "" {
			scoped[f.Source] = true
		}
	}
	var kept []*index.Fragment
	changed := false
	for _, f := range node.Fragments {
		if !isActive(f) {
			kept = append(kept, f)
			continue
		}
		shadowed := f.Source != nil && scoped[f.Source] &&
			(f.EvalCtx == nil || f.EvalCtx.Scope == "")
		if shadowed {
			deactivate(f)
			changed = true
			continue
		}
		if f.Source != nil {
			scope := ""
			if f.EvalCtx != nil {
				scope = f.EvalCtx.Scope
			}
			key := fragmentKey{src: f.Source, scope: scope, branch: f.BranchID}
			if seen[key] {
				deactivate(f)
				changed = true
				continue
			}
			seen[key] = true
		}
		kept = append(kept, f)
	}
	// Only rewrite the slice when something was actually dropped: a no-op
	// rewrite would still be a write to the tree, racing with concurrent
	// readers of a shared snapshot.
	if changed {
		node.Fragments = kept
	}
	for _, c := range node.Children {
		pruneShadowedFragments(c, isActive, deactivate)
	}
}

func (v *Validator) collectActiveNodes(ctx context.Context, node *index.ProjectNode, evalCtx *index.EvaluationContext, forceUncond bool) {
	if ctx.Err() != nil {
		return
	}
	scopeKey := ""
	if evalCtx != nil {
		scopeKey = evalCtx.Scope
		if evalCtx.ScopeNode() == nil {
			// Anchor relative reference paths in conditions to this node.
			// The context is shared (base context) across nodes, so copy
			// it instead of mutating.
			scoped := *evalCtx
			scoped.Node = node
			evalCtx = &scoped
		}
	}
	if v.expandedScopes == nil {
		v.expandedScopes = make(map[*index.ProjectNode]map[string]bool)
	}
	scopes := v.expandedScopes[node]
	if scopes != nil && scopes[scopeKey] {
		return
	}
	if scopes == nil {
		scopes = make(map[string]bool)
		v.expandedScopes[node] = scopes
	}
	scopes[scopeKey] = true
	if v.visited == nil {
		v.visited = make(map[*index.ProjectNode]map[parser.Definition]bool)
	}
	if v.loadCache == nil {
		v.loadCache = make(map[string]parser.Value)
	}
	if ctx.Err() != nil {
		return
	}
	v.muActive.Lock()
	v.ActiveNodes[node] = true
	for _, frag := range node.Fragments {
		if !frag.IsConditional {
			v.ActiveFragments[frag] = true
		}
	}
	v.muActive.Unlock()

	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		v.muActive.Lock()
		active := v.ActiveFragments[frag]
		v.muActive.Unlock()
		if active && frag.IsConditional && frag.EvalCtx == nil {
			// Conditional fragment without a recorded scope: either a block
			// body (handled by its block expansion) or an index-time
			// placeholder for an object inside such a body. Evaluating it
			// here would use the unbound scope and leak loop variables.
			continue
		}
		if active {
			c := evalCtx
			if frag.EvalCtx != nil && frag.EvalCtx.Scope != "" {
				// Fragment materialized under loop/template/with
				// bindings: evaluate with those.
				c = frag.EvalCtx
			}
			evaluated = append(evaluated, v.Tree.EvaluateDefinitions(frag.Definitions, c, frag.File)...)
		}
	}

	written := make(map[string]bool)
	// expanded marks (child, evaluation context) pairs already walked: a
	// loop revisits the same child name each iteration with a fresh context,
	// and every iteration defines its own nested objects.
	type expandKey struct {
		name string
		ctx  *index.EvaluationContext
	}
	expanded := make(map[expandKey]bool)
	// forceUncond: definitions in this expansion are unconditional
	// (true inside `with` blocks, which always load their document).
	var processEval func([]index.EvaluatedDefinition, *index.ProjectNode, bool)
	processEval = func(evaluated []index.EvaluatedDefinition, node *index.ProjectNode, forceUncond bool) {
		for _, ed := range evaluated {
			// fmt.Printf("[DEBUG] Processing evaluated definition: %T\n", ed.Def)
			switch d := ed.Def.(type) {
			case *parser.ObjectNode:
				objName := v.ValueToString(d.Name, ed.Ctx)
				if objName == "" || strings.Contains(objName, "@") {
					// Unresolvable at this stage: the bound expansion
					// (loop iteration / with binding) creates the real
					// object; skip the raw residual here.
					continue
				}
				norm := index.NormalizeName(objName)

				// Find or create the child node
				v.muActive.Lock()
				child, ok := node.Children[norm]
				if !ok {
					child = &index.ProjectNode{
						Name:          norm,
						RealName:      objName,
						Children:      make(map[string]*index.ProjectNode),
						Parent:        node,
						Metadata:      make(map[string]string),
						Variables:     make(map[string]index.VariableInfo),
						Fields:        make(map[string][]index.EvaluatedField),
						IsConditional: !forceUncond, // It's active now
					}
					node.Children[norm] = child
					v.Tree.AddToNodeMap(child)
				}
				if forceUncond {
					// E.g. materialized inside a `with` block: the
					// block always loads, so the child is not
					// conditional even if an earlier pass marked it.
					child.IsConditional = false
				}

				// Ensure this fragment is present and active
				found := false
				for _, f := range child.Fragments {
					// The same AST node repeats on every loop iteration, so a
					// fragment only serves this expansion when its scope matches.
					if f.Source == d && index.SameEvalScope(f.EvalCtx, ed.Ctx) {
						v.ActiveFragments[f] = true
						if f.EvalCtx == nil {
							f.EvalCtx = ed.Ctx
						}
						found = true
						break
					}
				}
				if !found {
					before := len(child.Fragments)
					v.Tree.PopulateObjectFragment(child, ed.File, d, "", nil, nil, !forceUncond)
					// Only fragments appended by this call belong to this
					// expansion; an earlier placeholder with the same source
					// must keep its own (or no) scope.
					for _, f := range child.Fragments[before:] {
						if f.Source == d && f.EvalCtx == nil {
							v.ActiveFragments[f] = true
							f.EvalCtx = ed.Ctx
							break
						}
					}
				}

				v.muActive.Unlock()

				if k := (expandKey{norm, ed.Ctx}); !expanded[k] {
					expanded[k] = true
					written[norm] = true
					v.collectActiveNodes(ctx, child, ed.Ctx, forceUncond)
				}
			case *parser.IfBlock:
				cond := v.Tree.EvaluateValue(d.Condition, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				if v.Tree.IsTrue(cond) {
					// Activate fragments for 'Then' branch
					v.muActive.Lock()
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":then" {
							v.ActiveFragments[f] = true
						}
					}
					v.muActive.Unlock()
					v.activateBranchChildren(node, id+":then")
					processEval(v.Tree.EvaluateDefinitions(d.Then, ed.Ctx, ed.File), node, false)
				} else {
					matched := false
					for i, ei := range d.ElseIf {
						elseifCond := v.Tree.EvaluateValue(ei.Condition, ed.Ctx)
						if v.Tree.IsTrue(elseifCond) {
							branchID := fmt.Sprintf("%s:elseif%d", id, i)
							v.muActive.Lock()
							for _, f := range node.Fragments {
								if f.IsConditional && f.BranchID == branchID {
									v.ActiveFragments[f] = true
								}
							}
							v.muActive.Unlock()
							v.activateBranchChildren(node, branchID)
							processEval(v.Tree.EvaluateDefinitions(ei.Body, ed.Ctx, ed.File), node, false)
							matched = true
							break
						}
					}
					if !matched && len(d.Else) > 0 {
						v.muActive.Lock()
						for _, f := range node.Fragments {
							if f.IsConditional && f.BranchID == id+":else" {
								v.ActiveFragments[f] = true
							}
						}
						v.muActive.Unlock()
						v.activateBranchChildren(node, id+":else")
						processEval(v.Tree.EvaluateDefinitions(d.Else, ed.Ctx, ed.File), node, false)
					}
				}
			case *parser.ForeachBlock:
				iterable := v.Tree.EvaluateValue(d.Iterable, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				if arr, ok := iterable.(*parser.ArrayValue); ok {
					v.muActive.Lock()
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":body" {
							v.ActiveFragments[f] = true
						}
					}
					v.muActive.Unlock()
					for i, val := range arr.Elements {
						subCtx := &index.EvaluationContext{
							Variables: make(map[string]parser.Value),
							Parent:    ed.Ctx,
							Tree:      v.Tree,
							Scope:     index.ChildScope(ed.Ctx, fmt.Sprintf("%s#%d", id, i)),
						}
						if d.ValueVar != "" {
							subCtx.Variables[d.ValueVar] = val
						}
						if d.KeyVar != "" {
							// zip()/enumerate() produce two-element rows:
							// `foreach a, b in @zip(...)` destructures them.
							if row, ok := val.(*parser.ArrayValue); ok && len(row.Elements) == 2 {
								subCtx.Variables[d.KeyVar] = row.Elements[0]
								subCtx.Variables[d.ValueVar] = row.Elements[1]
							} else {
								subCtx.Variables[d.KeyVar] = &parser.IntValue{Value: int64(i), Raw: fmt.Sprintf("%d", i)}
							}
						}
						processEval(v.Tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, forceUncond)
					}
				} else if m, ok := iterable.(*parser.MapValue); ok {
					// Dict iteration: two-variable foreach binds
					// key and value of every member.
					v.muActive.Lock()
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":body" {
							v.ActiveFragments[f] = true
						}
					}
					v.muActive.Unlock()
					for _, key := range m.Keys {
						subCtx := &index.EvaluationContext{
							Variables: make(map[string]parser.Value),
							Parent:    ed.Ctx,
							Tree:      v.Tree,
							Scope:     index.ChildScope(ed.Ctx, id+"#"+key),
						}
						if d.KeyVar != "" {
							subCtx.Variables[d.KeyVar] = &parser.StringValue{Value: key, Quoted: true}
						}
						if d.ValueVar != "" {
							subCtx.Variables[d.ValueVar] = m.Values[key]
						}
						processEval(v.Tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, forceUncond)
					}
				}
			case *parser.AssertStatement:
				// Evaluated once activation settles: the condition may
				// reference variables defined by nodes activated later.
				v.muActive.Lock()
				if v.pendingAsserts == nil {
					v.pendingAsserts = make(map[*parser.AssertStatement]assertInstance)
				}
				v.pendingAsserts[d] = assertInstance{ctx: ed.Ctx, file: ed.File, node: node}
				v.muActive.Unlock()
			case *parser.WithBlock:
				if v.Tree.HasUnresolvedReference(d.Path, ed.Ctx) {
					// The path variable is not bound in this scope (a loop
					// body is visited both per iteration and at node level).
					// Defer: the binding expansion loads the document, and
					// reportDeferredWiths reports paths that never resolve.
					v.muActive.Lock()
					if v.pendingWith == nil {
						v.pendingWith = make(map[*parser.WithBlock]withInstance)
					}
					v.pendingWith[d] = withInstance{ctx: ed.Ctx, file: ed.File, node: node}
					v.muActive.Unlock()
					continue
				}
				home := loader.ProjectHome(ed.File, v.ProjectRoot)
				resolved := loader.ResolvePathFrom(ed.File, home, v.ValueToString(d.Path, ed.Ctx))
				v.muActive.Lock()
				if v.loadedWith == nil {
					v.loadedWith = make(map[*parser.WithBlock]bool)
				}
				v.loadedWith[d] = true
				v.muActive.Unlock()
				val, err := v.loadStructured(resolved, d.Format)
				if err != nil {
					if err == errAlreadyReported {
						if v.MissingFilesAreWarnings {
							val = parser.NewMapValue(d.Position, map[string]parser.Value{})
						} else {
							continue
						}
					} else {
						level := LevelError
						summary := "field references will not resolve"
						if v.MissingFilesAreWarnings {
							level = LevelWarning
							summary = "assuming the documented fields exist"
							// Tolerant mode: bind an empty document so member
							// references and iterations over the binding stay
							// quiet instead of cascading into errors.
							val = parser.NewMapValue(d.Position, map[string]parser.Value{})
						}
						v.report(node, "with_load_failed", level,
							fmt.Sprintf("with %s(%q): %v (%s)", d.Format, resolved, err, summary),
							d.Position, ed.File)
						if val == nil {
							continue
						}
					}
				}
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				subCtx := &index.EvaluationContext{
					Variables: map[string]parser.Value{d.BindName: val},
					Parent:    ed.Ctx,
					Tree:      v.Tree,
					Scope:     index.ChildScope(ed.Ctx, id+":with"),
				}
				v.muActive.Lock()
				for _, f := range node.Fragments {
					if f.IsConditional && f.BranchID == id+":with" {
						v.ActiveFragments[f] = true
						if f.EvalCtx == nil {
							// Body definitions evaluate against the
							// document binding on later passes.
							f.EvalCtx = subCtx
						}
					}
				}
				v.muActive.Unlock()
				// The with block always loads its document: the body's
				// definitions are unconditional.
				processEval(v.Tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, true)
			case *parser.TemplateDefinition:
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				v.muActive.Lock()
				for _, f := range node.Fragments {
					if f.IsConditional && f.BranchID == id+":template" {
						v.ActiveFragments[f] = true
					}
				}
				v.muActive.Unlock()
				processEval(v.Tree.EvaluateDefinitions(d.Body, ed.Ctx, ed.File), node, forceUncond)
			}
		}
	}

	processEval(evaluated, node, forceUncond)

	// Sibling children that this expansion did not define belong to the
	// base scope (they may come from another file sharing the package).
	// Recursing into them with a loop/with context leaked that expansion
	// into unrelated subtrees and multiplied their fragments once per
	// iteration. Base visits (empty scope) still walk the whole tree.
	if evalCtx == nil || evalCtx.Scope == "" {
		for name, child := range node.Children {
			if !written[name] && !child.IsConditional {
				v.collectActiveNodes(ctx, child, evalCtx, forceUncond)
			}
		}
	}
}

// Activate materializes the active tree: loop iterations, template
// instantiations and with-block bodies are expanded, and active fields
// and references are resolved. It produces no diagnostics and is cheap
// relative to full validation, so callers (e.g. the LSP) can run it
// eagerly to make a tree self-consistent for navigation.
func (v *Validator) Activate(ctx context.Context) {
	if v.Tree == nil {
		return
	}
	v.baseCtx = &index.EvaluationContext{Variables: v.Variables, Tree: v.Tree}
	// Initial full resolution (before activation pass, as ActiveFragments is empty)
	v.Tree.ResolveFields(nil)
	v.Tree.ResolveReferences(nil)

	// Multi-pass active node collection to handle variables defined in conditional blocks
	for pass := 0; pass < 5; pass++ { // Max 5 passes to avoid infinite loops
		evalCtx := &index.EvaluationContext{Variables: v.Variables, Tree: v.Tree}

		v.Tree.Walk(func(n *index.ProjectNode) {
			for k, varInfo := range n.Variables {
				if _, ok := v.Variables[k]; !ok || varInfo.Def.IsConst {
					v.Variables[k] = varInfo.Def.DefaultValue
				}

				if valStr, ok := v.RawOverrides[k]; ok {
					v.setOverrideValue(k, valStr, varInfo.Def)
				}
			}
		})
		// Re-apply overrides
		for k, val := range v.Overrides {
			v.Variables[k] = val
		}

		prevCount := len(v.ActiveFragments)
		v.expandedScopes = make(map[*index.ProjectNode]map[string]bool)
		if v.Tree.Root != nil {
			v.collectActiveNodes(ctx, v.Tree.Root, evalCtx, false)
		}
		for _, node := range v.Tree.IsolatedFiles {
			v.collectActiveNodes(ctx, node, evalCtx, false)
		}

		// Re-resolve only active things after activation pass
		v.Tree.ResolveFields(v.ActiveFragments)
		v.Tree.ResolveReferences(v.ActiveFragments)

		if len(v.ActiveFragments) == prevCount {
			break
		}
	}
	if v.Tree.Root != nil {
		pruneShadowedFragments(v.Tree.Root,
			func(f *index.Fragment) bool {
				v.muActive.Lock()
				defer v.muActive.Unlock()
				return v.ActiveFragments[f]
			},
			func(f *index.Fragment) {
				v.muActive.Lock()
				defer v.muActive.Unlock()
				delete(v.ActiveFragments, f)
			})
	}
	for _, root := range v.Tree.IsolatedFiles {
		pruneShadowedFragments(root,
			func(f *index.Fragment) bool {
				v.muActive.Lock()
				defer v.muActive.Unlock()
				return v.ActiveFragments[f]
			},
			func(f *index.Fragment) {
				v.muActive.Lock()
				defer v.muActive.Unlock()
				delete(v.ActiveFragments, f)
			})
	}
	v.buildFileFragments()
	v.reportWithBindingTypos()
	v.reportDeferredWiths()
	v.evaluateAsserts()
}

// buildFileFragments indexes fragments by file for position queries.
func (v *Validator) buildFileFragments() {
	frags := make(map[string][]*index.Fragment)
	var walk func(n *index.ProjectNode)
	walk = func(n *index.ProjectNode) {
		if n == nil {
			return
		}
		for _, f := range n.Fragments {
			if f.File != "" {
				frags[f.File] = append(frags[f.File], f)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if v.Tree.Root != nil {
		walk(v.Tree.Root)
	}
	for _, iso := range v.Tree.IsolatedFiles {
		walk(iso)
	}
	v.fileFragments = frags
}

// reportWithBindingTypos warns when a block body refers to a variable that
// is one or two edits away from the name the block binds (e.g.
// `as siganls` + `@signals`). Such a typo makes the body silently expand to
// nothing and only shows up later as missing generated objects.
func (v *Validator) reportWithBindingTypos() {
	var walk func(n *index.ProjectNode)
	walk = func(n *index.ProjectNode) {
		if n == nil {
			return
		}
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				wb, ok := def.(*parser.WithBlock)
				if !ok || wb.BindName == "" {
					continue
				}
				for _, ref := range v.Tree.FileReferences[frag.File] {
					if !ref.IsVariable {
						continue
					}
					if ref.Position.Line < wb.Position.Line || ref.Position.Line > wb.EndPosition.Line {
						continue
					}
					name := strings.TrimPrefix(strings.TrimPrefix(ref.Name, "@"), "$")
					if name == wb.BindName || len(name) != len(wb.BindName) {
						continue
					}
					if name[0] != wb.BindName[0] || name[len(name)-1] != wb.BindName[len(wb.BindName)-1] {
						continue
					}
					if editDistance(name, wb.BindName) > 2 {
						continue
					}
					v.report(n, "with_binding_typo", LevelWarning,
						fmt.Sprintf("with %s(...) as %s: '@%s' looks like the binding '%s' (did you mean '@%s'?)", wb.Format, wb.BindName, name, wb.BindName, wb.BindName),
						ref.Position, ref.File)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if v.Tree.Root != nil {
		walk(v.Tree.Root)
	}
	for _, iso := range v.Tree.IsolatedFiles {
		walk(iso)
	}
}

// editDistance is the Levenshtein distance between two short names.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			cur[j] = del
			if ins < cur[j] {
				cur[j] = ins
			}
			if sub < cur[j] {
				cur[j] = sub
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// reportDeferredWiths reports with-blocks whose path still references an
// unbound variable after activation. Blocks that loaded in some scope are
// left alone (the same definition is visited per loop iteration and at node
// level, and only the node-level visit may lack the binding).
func (v *Validator) reportDeferredWiths() {
	v.muActive.Lock()
	pending := make([]*parser.WithBlock, 0, len(v.pendingWith))
	instances := make(map[*parser.WithBlock]withInstance, len(v.pendingWith))
	for def, inst := range v.pendingWith {
		if v.loadedWith[def] {
			continue
		}
		pending = append(pending, def)
		instances[def] = inst
	}
	v.muActive.Unlock()

	for _, def := range pending {
		inst := instances[def]
		path := v.ValueToString(def.Path, inst.ctx)
		msg := fmt.Sprintf("with %s(%q): the path references a variable that is never bound", def.Format, path)
		if path == "" || !v.Tree.HasUnresolvedReference(def.Path, inst.ctx) {
			// Resolvable now but the load never succeeded in any scope.
			msg = fmt.Sprintf("with %s(%q): the document could not be loaded", def.Format, path)
		}
		v.mu.Lock()
		if v.reportedLoadFailures == nil {
			v.reportedLoadFailures = make(map[string]bool)
		}
		key := fmt.Sprintf("%s:%d:%d", inst.file, def.Position.Line, def.Position.Column)
		if v.reportedLoadFailures[key] {
			v.mu.Unlock()
			continue
		}
		v.reportedLoadFailures[key] = true
		v.mu.Unlock()
		v.report(inst.node, "with_load_failed", LevelError, msg, def.Position, inst.file)
	}
}

// evaluateAsserts checks every assert statement reached by activation. A
// failing condition is an error diagnostic, so `mdt check` reports it and
// `mdt build` refuses to write output. Each assert is reported at most once
// per validator run.
func (v *Validator) evaluateAsserts() {
	v.muActive.Lock()
	pending := make([]*parser.AssertStatement, 0, len(v.pendingAsserts))
	instances := make(map[*parser.AssertStatement]assertInstance, len(v.pendingAsserts))
	for def, inst := range v.pendingAsserts {
		if v.reportedAsserts[def] {
			continue
		}
		pending = append(pending, def)
		instances[def] = inst
	}
	v.muActive.Unlock()

	for _, def := range pending {
		inst := instances[def]
		cond := v.Tree.EvaluateValue(def.Condition, inst.ctx)
		if cond == nil {
			v.report(inst.node, "assert_unresolved", LevelError,
				"assert: the condition could not be evaluated (unresolved reference?)",
				def.Position, inst.file)
		} else if v.Tree.IsTrue(cond) {
			continue
		} else {
			msg := "assertion failed"
			if def.Message != nil {
				if rendered := v.ValueToString(def.Message, inst.ctx); rendered != "" {
					msg = rendered
				}
			}
			v.report(inst.node, "assert_failed", LevelError, msg, def.Position, inst.file)
		}
		v.muActive.Lock()
		v.reportedAsserts[def] = true
		v.muActive.Unlock()
	}
}

func (v *Validator) ValidateProject(ctx context.Context) {
	if v.Tree == nil {
		return
	}
	v.Activate(ctx)
	if ctx.Err() != nil {
		return
	}

	evalCtx := &index.EvaluationContext{Variables: v.Variables, Tree: v.Tree}

	numWorkers := runtime.NumCPU()
	if numWorkers < 4 {
		numWorkers = 4
	}
	tasks := make(chan *index.ProjectNode, 100)
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case node, ok := <-tasks:
					if !ok {
						return
					}
					func() {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("[ERROR] panic while validating node %q: %v\n%s", node.RealName, r, debug.Stack())
							}
							wg.Done()
						}()
						v.validateNode(ctx, node, evalCtx)
					}()
				}
			}
		}()
	}

	queueTask := func(n *index.ProjectNode) {
		// We need to count tasks to wait for them
		// But validateNode itself recurses and we don't want to deadlock
		// if we queue from inside validateNode.
		// Actually, let's just use the workers for TOP-LEVEL packages/files
		// and let validateNode recurse synchronously within each worker.
		wg.Add(1)
		select {
		case <-ctx.Done():
			wg.Done()
		case tasks <- n:
		}
	}

	if v.Tree.Root != nil {
		for _, child := range v.Tree.Root.Children {
			if ctx.Err() != nil {
				break
			}
			queueTask(child)
		}
	}
	for _, node := range v.Tree.IsolatedFiles {
		if ctx.Err() != nil {
			break
		}
		queueTask(node)
	}

	// Close the tasks channel to signal workers that no more tasks will arrive.
	// Workers drain any remaining in-flight tasks, then exit when the channel
	// is empty and closed. This prevents goroutine leaks when the context is
	// cancelled (BUG-005).
	close(tasks)
	wg.Wait()

	if ctx.Err() != nil {
		return
	}

	v.CheckUnused(ctx)
	v.CheckDataSourceThreading(ctx)
	v.CheckINOUTOrdering(ctx)
	v.CheckSignalConsistency(ctx)
	v.CheckVariables(ctx)
	v.CheckUnresolvedVariables(ctx)
	v.CheckConditionalReferences(ctx)
}

func (v *Validator) validateNode(ctx context.Context, node *index.ProjectNode, evalCtx *index.EvaluationContext) {
	if ctx.Err() != nil {
		return
	}

	// Only validate if at least one fragment is active
	hasActiveFragment := false
	v.muActive.Lock()
	for _, frag := range node.Fragments {
		if v.ActiveFragments[frag] {
			hasActiveFragment = true
			break
		}
	}
	v.muActive.Unlock()

	// Exception: Packages (Root, or nodes without fragments) might still be active containers
	if !hasActiveFragment && len(node.Fragments) > 0 {
		return
	}

	// Check for invalid content in Signals container of DataSource
	if node.RealName == "Signals" && node.Parent != nil && v.Tree.IsDataSource(node.Parent) {
		for _, frag := range node.Fragments {
			for _, def := range frag.Definitions {
				if f, ok := def.(*parser.Field); ok {
					v.report(node, "invalid_container_content", LevelError,
						fmt.Sprintf("Invalid content in Signals container: Field '%s' is not allowed. Only Signal objects are allowed.", f.Name),
						f.Position, frag.File)
				}
			}
		}
	}

	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		v.muActive.Lock()
		active := v.ActiveFragments[frag]
		v.muActive.Unlock()
		if active {
			c := evalCtx
			if frag.EvalCtx != nil && frag.EvalCtx.Scope != "" {
				c = frag.EvalCtx
			} else if frag.BranchID == "" && frag.Source != nil && evalCtx != nil && evalCtx.Scope != "" {
				// Unscoped index fragment reached through a scoped
				// expansion: evaluate it under the base scope rather than
				// the iteration's bindings.
				c = v.baseCtx
			}
			evaluated = append(evaluated, v.Tree.EvaluateDefinitions(frag.Definitions, c, frag.File)...)
		}
	}

	fields := v.extractFields(evaluated)
	// A field repeated across different expansions (loop iterations sharing
	// a container) is not a duplicate definition: each expansion contributes
	// its own value. Only repetitions within the same scope are errors.
	fieldScopes := make(map[string]map[string]bool)
	for _, ed := range evaluated {
		if d, ok := ed.Def.(*parser.Field); ok {
			if fieldScopes[d.Name] == nil {
				fieldScopes[d.Name] = make(map[string]bool)
			}
			scope := ""
			if ed.Ctx != nil {
				scope = ed.Ctx.Scope
			}
			fieldScopes[d.Name][scope] = true
		}
	}
	var objects []index.EvaluatedDefinition
	for _, ed := range evaluated {
		if _, ok := ed.Def.(*parser.ObjectNode); ok {
			objects = append(objects, ed)
		}
	}

	// 1. Check for duplicate fields (evaluated only)
	for name, defs := range fields {
		if len(defs) > 1 && len(fieldScopes[name]) <= 1 {
			v.report(node, "duplicate_field", LevelError,
				fmt.Sprintf("Duplicate Field Definition: '%s' is already defined in object %s (%s)", name, nodePath(node), defs[0].File),
				defs[1].Raw.Position, defs[1].File)
		}
	}

	// NEW: Strict Field Validation
	for name, defs := range fields {
		for _, f := range defs {
			if name == "Class" {
				v.validateClassField(f, node)
			} else if name == "Type" {
				v.validateTypeField(f, node)
			} else if name == "Alias" {
				v.validateAliasField(f, node, fields)
			} else {
				v.validateGenericField(f, node)
			}
		}
	}

	// 2. Check for mandatory Class if it's an object node (+/$)
	className := ""
	if classFields, ok := fields["Class"]; ok && len(classFields) > 0 {
		className = v.getFieldValue(classFields[0], node)
	}

	if node.RealName != "" && (node.RealName[0] == '+' || node.RealName[0] == '$') && !v.Tree.IsSignal(node) {
		if className == "" {
			pos := v.getNodePosition(node)
			file := v.getNodeFile(node)
			v.report(node, "missing_class", LevelError,
				fmt.Sprintf("Node %s is an object and must contain a 'Class' field", node.RealName),
				pos, file)
		}
	} else if v.Tree.IsSignal(node) {
		// Signals (no + prefix) must have Type
		if _, ok := fields["Type"]; !ok {
			pos := v.getNodePosition(node)
			file := v.getNodeFile(node)
			v.report(node, "missing_type", LevelError,
				fmt.Sprintf("Signal %s must contain a 'Type' field", node.RealName),
				pos, file)
		}
	}

	// 4. Signal Validation (for DataSource signals)
	if v.Tree.IsSignal(node) {
		v.validateSignal(node, fields)
	}

	// RealTimeThread function lists are checked by class, whether or not
	// the node carries a '+'/'$' prefix.
	if className == "RealTimeThread" {
		v.checkFunctionsArray(node, fields)
	}

	// 5. GAM Validation (Signal references)
	if v.Tree.IsGAM(node) {
		v.validateGAM(node)
	}

	// 6. DataSource Validation
	if v.Tree.IsDataSource(node) {
		v.validateDataSource(node)
	}

	// 7. Template Use Validation
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if inst, ok := def.(*parser.TemplateInstantiation); ok {
				v.checkTemplateUse(inst, frag.File)
			}
		}
	}

	// IOGAM copies its whole input memory to the output memory; report a
	// mismatch with the size derivation of every signal on both sides.
	if className == "IOGAM" {
		v.checkIOGAMSize(node)
	}

	// 3. CUE Validation
	if className != "" && v.Schema != nil {
		v.validateWithCUE(node, className)
	}

	written := make(map[string]bool)
	for _, obj := range objects {
		objectNode := obj.Def.(*parser.ObjectNode)
		objName := v.ValueToString(objectNode.Name, obj.Ctx)
		if strings.Contains(objName, "@") {
			// Unexpanded loop/template body definition: the
			// materialized instances (with resolved bindings) are
			// validated instead.
			continue
		}
		norm := index.NormalizeName(objName)

		if child, ok := node.Children[norm]; ok {
			if !written[norm] {
				v.validateNode(ctx, child, obj.Ctx)
				written[norm] = true
			}
		} else {
			// Dynamic object
			v.validateDynamicObject(ctx, objectNode, obj.Ctx, obj.File)
		}
	}

	// Recursively validate remaining static children (e.g. packages)
	for name, child := range node.Children {
		if !written[name] {
			// Check if child has at least one active fragment
			childActive := false
			v.muActive.Lock()
			for _, f := range child.Fragments {
				if v.ActiveFragments[f] {
					childActive = true
					break
				}
			}
			v.muActive.Unlock()

			if !child.IsConditional || childActive {
				v.validateNode(ctx, child, evalCtx)
			}
		}
	}
}

func (v *Validator) validateDynamicObject(ctx context.Context, obj *parser.ObjectNode, evalCtx *index.EvaluationContext, file string) {
	evaluated := v.Tree.EvaluateDefinitions(obj.Subnode.Definitions, evalCtx, file)

	fields := v.extractFields(evaluated)
	// A field repeated across different expansions (loop iterations sharing
	// a container) is not a duplicate definition: each expansion contributes
	// its own value. Only repetitions within the same scope are errors.
	fieldScopes := make(map[string]map[string]bool)
	for _, ed := range evaluated {
		if d, ok := ed.Def.(*parser.Field); ok {
			if fieldScopes[d.Name] == nil {
				fieldScopes[d.Name] = make(map[string]bool)
			}
			scope := ""
			if ed.Ctx != nil {
				scope = ed.Ctx.Scope
			}
			fieldScopes[d.Name][scope] = true
		}
	}
	var objects []index.EvaluatedDefinition
	for _, ed := range evaluated {
		if _, ok := ed.Def.(*parser.ObjectNode); ok {
			objects = append(objects, ed)
		}
	}

	// Perform basic validation on dynamic object (Class existence, duplicates)
	for name, defs := range fields {
		if len(defs) > 1 {
			objName := v.ValueToString(obj.Name, evalCtx)
			v.report(nil, "duplicate_field", LevelError,
				fmt.Sprintf("Duplicate Field Definition: '%s' is already defined in object %s (%s)", name, objName, file),
				defs[1].Raw.Position, defs[1].File)
		}
		for _, f := range defs {
			if name == "Class" {
				v.validateClassField(f, nil)
			} else if name == "Type" {
				v.validateTypeField(f, nil)
			} else {
				v.validateGenericField(f, nil)
			}
		}
	}

	// Recurse into sub-objects
	for _, sub := range objects {
		v.validateDynamicObject(ctx, sub.Def.(*parser.ObjectNode), sub.Ctx, sub.File)
	}
}

func (v *Validator) extractFields(evaluated []index.EvaluatedDefinition) map[string][]index.EvaluatedField {
	fields := make(map[string][]index.EvaluatedField)
	for _, ed := range evaluated {
		if d, ok := ed.Def.(*parser.Field); ok {
			fields[d.Name] = append(fields[d.Name], index.EvaluatedField{
				Raw:   d,
				Value: v.Tree.EvaluateValue(d.Value, ed.Ctx),
				File:  ed.File,
			})
		}
	}
	return fields
}

func (v *Validator) ValueToString(val parser.Value, ctx *index.EvaluationContext) string {
	res := v.Tree.EvaluateValue(val, ctx)
	return v.Tree.ValueToString(res)
}

func (v *Validator) validateClassField(f index.EvaluatedField, node *index.ProjectNode) {
	// Class field should always have a value Class (string quoted or not)
	var className string
	switch val := f.Value.(type) {
	case *parser.StringValue:
		className = val.Value
	case *parser.ReferenceValue:
		// Treated as string literal for Class field
		className = val.Value
	default:
		v.report(node, "invalid_class_field", LevelError,
			fmt.Sprintf("Class field must be a string (quoted or identifier), got %T", f.Value),
			f.Raw.Position, f.File)
		return
	}

	// Check if class exists in schema
	if v.Schema != nil && className != "" {
		// Strip namespace if present (e.g. SDN::SDNSubscriber -> SDNSubscriber)
		lookupName := className
		if idx := strings.LastIndex(className, "::"); idx != -1 {
			lookupName = className[idx+2:]
		}

		// Use a string selector: class names may contain characters that
		// cue.ParsePath would misread (e.g. "DAN::DANSource").
		path := cue.ParsePath("#Classes")
		if v.Schema.Value.LookupPath(path).Lookup(lookupName).Err() != nil {
			// Unknown Class
			v.report(node, "unknown_class", LevelWarning,
				fmt.Sprintf("Unknown Class '%s'", className),
				f.Raw.Position, f.File)
		}
	}
}

func (v *Validator) validateTypeField(f index.EvaluatedField, node *index.ProjectNode) {
	// Type field should always have a type as value (uint etc)
	var typeName string
	switch val := f.Value.(type) {
	case *parser.StringValue:
		typeName = val.Value
	case *parser.ReferenceValue:
		typeName = val.Value
	default:
		v.report(node, "invalid_type_field", LevelError,
			fmt.Sprintf("Type field must be a valid type string, got %T", f.Value),
			f.Raw.Position, f.File)
		return
	}

	if !isValidType(typeName) {
		v.report(node, "invalid_type", LevelError,
			fmt.Sprintf("Invalid Type '%s'", typeName),
			f.Raw.Position, f.File)
	}
}

// validateAliasField resolves an `Alias = X` value against the sibling
// `DataSource`: X names a signal *inside* that DataSource, not a node of the
// enclosing package. Treating it as a global reference produced bogus
// "Unknown reference" errors for valid aliased GAM signal entries.
func (v *Validator) validateAliasField(f index.EvaluatedField, node *index.ProjectNode, fields map[string][]index.EvaluatedField) {
	var name string
	switch val := f.Value.(type) {
	case *parser.ReferenceValue:
		name = val.Value
	case *parser.StringValue:
		name = val.Value
	default:
		v.validateGenericField(f, node)
		return
	}
	dsName := ""
	if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
		dsName = v.getFieldValue(dsFields[0], node)
	}
	if dsName == "" {
		v.validateGenericField(f, node)
		return
	}
	ds := v.resolveReference(dsName, node, func(n *index.ProjectNode) bool { return v.Tree.IsDataSource(n) })
	if ds == nil {
		return
	}
	if signals, ok := ds.Children["Signals"]; ok {
		if sig, ok := signals.Children[index.NormalizeName(name)]; ok {
			v.updateReferenceTarget(f.File, f.Raw.Position, sig)
		}
	}
	// Not found: the signal may be created implicitly by a GAM output
	// (`Implicitly Defined Signal` warns about that), so this is not an
	// error. MARTe resolves the alias inside the DataSource at runtime.
}

func (v *Validator) validateGenericField(f index.EvaluatedField, node *index.ProjectNode) {
	v.validateValue(f.Value, node, f.File)
}

func (v *Validator) validateValue(val parser.Value, node *index.ProjectNode, file string) {
	switch t := val.(type) {
	case *parser.ReferenceValue:
		// Non-quoted string: a reference -> Must resolve
		target := v.resolveReference(t.Value, node, nil)
		if target == nil {
			v.report(node, "unknown_reference", LevelError,
				fmt.Sprintf("Unknown reference '%s'", t.Value),
				t.Position, file)
		} else {
			// Link reference
			v.updateReferenceTarget(file, t.Position, target)
		}
	case *parser.StringValue:
		if !t.Quoted {
			// Should be handled as ReferenceValue by Parser for identifiers.
			// But if parser produces StringValue(Quoted=false), treat as ref?
			// Current parser produces ReferenceValue for identifiers.
			// So StringValue is likely Quoted=true.
		}
	case *parser.ArrayValue:
		for _, elem := range t.Elements {
			v.validateValue(elem, node, file)
		}
	case *parser.BinaryExpression:
		v.validateValue(t.Left, node, file)
		v.validateValue(t.Right, node, file)
	case *parser.UnaryExpression:
		v.validateValue(t.Right, node, file)
	}
}

func (v *Validator) isSuppressed(warningType string, node *index.ProjectNode) bool {
	// Global suppression check
	// Use an empty string if node is nil for file context,
	// but isGloballyAllowed should ideally have a file context.
	file := ""
	if node != nil {
		file = v.getNodeFile(node)
	}

	if v.isGloballyAllowed(warningType, file) {
		return true
	}

	// Legacy tag support
	if warningType == "unused_gam" || warningType == "unused_signal" {
		if v.isGloballyAllowed("unused", file) {
			return true
		}
	}
	if warningType == "implicit_signal" {
		if v.isGloballyAllowed("implicit", file) {
			return true
		}
	}

	if node == nil {
		return false
	}

	// Check local pragmas on the node
	checkTags := []string{warningType}
	if warningType == "unused_gam" || warningType == "unused_signal" {
		checkTags = append(checkTags, "unused")
	}
	if warningType == "implicit_signal" {
		checkTags = append(checkTags, "implicit")
	}

	for _, tag := range checkTags {
		prefix1 := fmt.Sprintf("allow(%s)", tag)
		prefix2 := fmt.Sprintf("ignore(%s)", tag)
		for _, p := range node.Pragmas {
			normalized := strings.ReplaceAll(p, " ", "")
			if strings.HasPrefix(normalized, prefix1) || strings.HasPrefix(normalized, prefix2) {
				return true
			}
			// Special case for colon-separated pragmas //! unused: ...
			if strings.HasPrefix(p, tag+":") {
				return true
			}
		}
	}
	return false
}

func (v *Validator) isPositionActive(file string, pos parser.Position) bool {
	frags := v.fileFragments[file]
	if len(frags) == 0 {
		return false
	}
	// Position is inactive when it falls inside a conditional block body
	// (#if/#foreach/#template) that was not selected for this pass.
	for _, frag := range frags {
		if frag.IsObject || !frag.IsConditional {
			continue
		}
		if pos.Line >= frag.ObjectPos.Line && pos.Line <= frag.EndPos.Line {
			v.muActive.Lock()
			active := v.ActiveFragments[frag]
			v.muActive.Unlock()
			if !active {
				return false
			}
		}
	}
	// Otherwise the innermost object fragment containing the position
	// decides, so a definition inside an inactive branch stays inactive.
	var best *index.Fragment
	for _, frag := range frags {
		if !frag.IsObject {
			continue
		}
		start := frag.ObjectPos
		end := frag.EndPos
		if (pos.Line > start.Line || (pos.Line == start.Line && pos.Column >= start.Column)) &&
			(pos.Line < end.Line || (pos.Line == end.Line && pos.Column <= end.Column)) {
			if best == nil || spanLess(frag, best) {
				best = frag
			}
		}
	}
	if best != nil {
		v.muActive.Lock()
		active := v.ActiveFragments[best]
		v.muActive.Unlock()
		return active
	}
	for _, frag := range frags {
		if !frag.IsObject && !frag.IsConditional && frag.BranchID == "" {
			return true
		}
	}
	return true
}

// spanLess orders two fragments by source range size (smallest wins, i.e.
// the innermost definition).
func spanLess(a, b *index.Fragment) bool {
	as := (a.EndPos.Line-a.ObjectPos.Line)*(1<<20) + (a.EndPos.Column - a.ObjectPos.Column)
	bs := (b.EndPos.Line-b.ObjectPos.Line)*(1<<20) + (b.EndPos.Column - b.ObjectPos.Column)
	return as < bs
}

// nodePath renders the containment path of a node (e.g.
// "/App/+Data/+DDB5/Signals/DI0") so diagnostics on data-driven,
// generated objects say which object they refer to.
func nodePath(node *index.ProjectNode) string {
	if node == nil {
		return ""
	}
	var parts []string
	for n := node; n != nil; n = n.Parent {
		name := n.RealName
		if name == "" {
			name = n.Name
		}
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		parts = append(parts, name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

func (v *Validator) report(node *index.ProjectNode, tag string, level DiagnosticLevel, msg string, pos parser.Position, file string) {
	if v.isSuppressed(tag, node) {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Diagnostics = append(v.Diagnostics, Diagnostic{
		Level:    level,
		Message:  msg,
		Position: pos,
		File:     file,
	})
}

func shouldAutoQuoteWithDef(valStr string, def *parser.VariableDefinition) bool {
	trimmed := strings.TrimSpace(valStr)
	if strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") {
		return false
	}
	// JSON/YAML override objects and lists are already MARTe literals.
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return false
	}
	typeExpr := strings.TrimSpace(def.TypeExpr)
	// Structured or list types may contain the word "string" in a field
	// type; the override is a whole literal, not a string.
	if strings.HasPrefix(typeExpr, "{") || strings.HasPrefix(typeExpr, "[") {
		return false
	}
	if strings.Contains(typeExpr, "string") || strings.Contains(typeExpr, "char8") {
		return true
	}
	if strings.Contains(typeExpr, "|") && strings.Contains(typeExpr, "\"") {
		return true
	}
	if strings.HasPrefix(typeExpr, "\"") && strings.HasSuffix(typeExpr, "\"") {
		return true
	}
	// Check default value
	if def.DefaultValue != nil {
		if _, ok := def.DefaultValue.(*parser.StringValue); ok {
			return true
		}
	}
	return false
}

func (v *Validator) validateWithCUE(node *index.ProjectNode, className string) {
	// Strip namespace if present
	lookupName := className
	if idx := strings.LastIndex(className, "::"); idx != -1 {
		lookupName = className[idx+2:]
	}

	// Check if class exists in schema
	classPath := cue.ParsePath("#Classes")
	if v.Schema.Value.LookupPath(classPath).Lookup(lookupName).Err() != nil {
		return // Unknown class, skip validation
	}

	// Convert node to map (shallow for validation of this specific object)
	// We use depth 3 to reach Signals and their elements for GAM/DataSource validation.
	data := v.nodeToMapWithDepth(node, 3)

	// Encode data to CUE
	dataVal := v.Schema.Context.Encode(data)

	// Unify with #Object
	// #Object requires "Class" field, which is present in data.
	objDef := v.Schema.Value.LookupPath(cue.ParsePath("#Object"))

	// Unify
	res := objDef.Unify(dataVal)

	if err := res.Validate(cue.Concrete(true)); err != nil {
		// Report errors

		// Parse CUE error to diagnostic
		v.reportCUEError(err, node)
	}

	// Check Parent constraints from #meta
	meta := res.LookupPath(cue.ParsePath("#meta"))
	if meta.Exists() {
		v.validateParent(node, meta)
	}
}

func (v *Validator) validateParent(node *index.ProjectNode, meta cue.Value) {
	parentConstraint := meta.LookupPath(cue.ParsePath("Parent"))
	if !parentConstraint.Exists() || parentConstraint.Err() != nil {
		return
	}

	parent := node.Parent
	if parent == nil {
		// Root node has no parent. If there is a constraint, it might be an error.
		// Usually only RealTimeApplication is root.
		return
	}

	// Check Name
	if nameVal := parentConstraint.LookupPath(cue.ParsePath("Name")); nameVal.Exists() {
		expectedName, _ := nameVal.String()
		if parent.RealName != expectedName && parent.Name != expectedName {
			v.report(node, "parent_mismatch", LevelError,
				fmt.Sprintf("Parent Name Mismatch: Node '%s' must have parent named '%s', but has '%s'", node.RealName, expectedName, parent.RealName),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}

	// Check Class
	if classVal := parentConstraint.LookupPath(cue.ParsePath("Class")); classVal.Exists() {
		expectedClass, _ := classVal.String()
		parentClass := v.getNodeClass(parent)
		if parentClass != expectedClass {
			v.report(node, "parent_mismatch", LevelError,
				fmt.Sprintf("Parent Class Mismatch: Node '%s' must have parent of class '%s', but has '%s'", node.RealName, expectedClass, parentClass),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}

	// Check MetaType
	if typeVal := parentConstraint.LookupPath(cue.ParsePath("MetaType")); typeVal.Exists() {
		expectedType, _ := typeVal.String()
		parentMeta := v.getMetaType(parent)
		if parentMeta != expectedType {
			v.report(node, "parent_mismatch", LevelError,
				fmt.Sprintf("Parent MetaType Mismatch: Node '%s' must have parent of type '%s', but has '%s'", node.RealName, expectedType, parentMeta),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}
}

func (v *Validator) getMetaType(node *index.ProjectNode) string {
	className := v.getNodeClass(node)
	if className == "" {
		return ""
	}
	if v.Schema == nil {
		return ""
	}

	path := cue.ParsePath(fmt.Sprintf("#Classes.%s.#meta.MetaType", className))
	val := v.Schema.Value.LookupPath(path)
	if val.Exists() {
		s, _ := val.String()
		return s
	}
	return ""
}

func (v *Validator) reportCUEError(err error, node *index.ProjectNode) {
	list := errors.Errors(err)
	for _, e := range list {
		msg := e.Error()
		v.mu.Lock()
		explained := v.sizeMismatch[node]
		v.mu.Unlock()
		if explained && (strings.Contains(msg, "InputSize") || strings.Contains(msg, "OutputSize")) {
			// checkIOGAMSize already reported this with a breakdown.
			continue
		}
		v.report(node, "schema_validation", LevelError,
			fmt.Sprintf("Schema Validation Error: %v", msg),
			v.getNodePosition(node), v.getNodeFile(node))
	}
}

func (v *Validator) nodeToMapWithDepth(node *index.ProjectNode, depth int) map[string]interface{} {
	m := make(map[string]interface{})
	fields := v.getFields(node)

	for name, defs := range fields {
		if len(defs) > 0 {
			// Use the last definition (duplicates checked elsewhere)
			val := v.ValueToInterface(defs[len(defs)-1].Value, node)
			// Strip namespace from Class field value for schema validation
			if name == "Class" {
				if s, ok := val.(string); ok {
					if idx := strings.LastIndex(s, "::"); idx != -1 {
						val = s[idx+2:]
					}
				}
			}
			m[name] = val
		}
	}

	if size := v.getSignalByteSize(node); size > 0 {
		m["ByteSize"] = int(size)
	}

	// Children as nested maps?
	if depth > 0 {
		signalsContainer := v.isGAMSignalsContainer(node)
		for name, child := range node.Children {
			if signalsContainer && !v.nodeActive(child) {
				// Signal from the `#if` branch that was not selected: it
				// must not count towards InputSize/OutputSize nor be
				// type-checked.
				continue
			}
			m[name] = v.nodeToMapWithDepth(child, depth-1)
		}
	}

	return m
}

func (v *Validator) ValueToInterface(val parser.Value, ctx *index.ProjectNode) interface{} {
	switch t := val.(type) {
	case *parser.StringValue:
		return t.Value
	case *parser.IntValue:
		i, _ := strconv.ParseInt(t.Raw, 0, 64)
		return i
	case *parser.FloatValue:
		f, _ := strconv.ParseFloat(t.Raw, 64)
		return f
	case *parser.BoolValue:
		return t.Value
	case *parser.ReferenceValue:
		return t.Value
	case *parser.VariableReferenceValue:
		name := strings.TrimLeft(t.Name, "@$")
		if ov, ok := v.Overrides[name]; ok {
			return v.ValueToInterface(ov, ctx)
		}
		if info := v.Tree.ResolveVariable(ctx, name); info != nil {
			if info.Def.DefaultValue != nil {
				return v.ValueToInterface(info.Def.DefaultValue, ctx)
			}
		}
		return nil
	case *parser.ArrayValue:
		arr := make([]interface{}, 0, len(t.Elements))
		for _, e := range t.Elements {
			arr = v.appendValue(arr, e, ctx)
		}
		return arr
	case *parser.ConditionalArrayElements:
		return v.appendValue(nil, t, ctx)
	case *parser.MemberAccess:
		res := v.Tree.Evaluate(val, ctx)
		if _, stillExpr := res.(*parser.MemberAccess); stillExpr {
			return nil // unresolvable member access
		}
		return v.ValueToInterface(res, ctx)
	case *parser.MapValue:
		m := make(map[string]interface{}, len(t.Keys))
		for _, k := range t.Keys {
			m[k] = v.ValueToInterface(t.Values[k], ctx)
		}
		return m
	case *parser.BinaryExpression, *parser.UnaryExpression:
		// Delegate to the tree evaluator, which implements the full
		// operator set: arithmetic, comparison, logical, bitwise and
		// string concatenation. When an operand cannot be computed the
		// evaluator returns the expression itself; treat that as an
		// unknown (null) value instead of recursing forever.
		res := v.Tree.Evaluate(val, ctx)
		switch res.(type) {
		case *parser.BinaryExpression, *parser.UnaryExpression:
			return nil
		}
		return v.ValueToInterface(res, ctx)
	}
	return nil
}

// appendValue converts val and appends it to dst, flattening conditional
// array elements (#if/#else blocks) into dst inline.
func (v *Validator) appendValue(dst []interface{}, val parser.Value, ctx *index.ProjectNode) []interface{} {
	if cae, ok := val.(*parser.ConditionalArrayElements); ok {
		for _, e := range v.activeConditionalBranch(cae, ctx) {
			dst = v.appendValue(dst, e, ctx)
		}
		return dst
	}
	return append(dst, v.ValueToInterface(val, ctx))
}

// loadStructured loads (and caches) an external structured document
// for a with-block binding.
func (v *Validator) loadStructured(path, format string) (parser.Value, error) {
	if v.loadCache == nil {
		v.loadCache = make(map[string]parser.Value)
	}
	if v.reportedLoadFailures == nil {
		v.reportedLoadFailures = make(map[string]bool)
	}
	// Activation runs several passes; report a failed load once per document.
	if v.reportedLoadFailures[path+"|"+format] {
		return nil, errAlreadyReported
	}
	if val, ok := v.loadCache[path]; ok {
		return val, nil
	}
	val, err := loader.Load(format, path)
	if err != nil {
		v.reportedLoadFailures[path+"|"+format] = true
		return nil, err
	}
	v.loadCache[path] = val
	return val, nil
}

// errAlreadyReported marks a load failure whose diagnostic was already
// emitted on an earlier activation pass.
var errAlreadyReported = fmt.Errorf("previously reported")

// activeConditionalBranch returns the value list of the first branch whose
// condition evaluates to true, falling back to the #else branch.
func (v *Validator) activeConditionalBranch(cae *parser.ConditionalArrayElements, ctx *index.ProjectNode) []parser.Value {
	return index.ActiveBranch(cae, func(val parser.Value) bool {
		return v.Tree.IsTrue(v.Tree.Evaluate(val, ctx))
	})
}

func (v *Validator) evaluateBinary(left interface{}, op parser.TokenType, right interface{}) interface{} {
	if left == nil || right == nil {
		return nil
	}

	if op == parser.TokenConcat {
		return fmt.Sprintf("%v%v", left, right)
	}

	toInt := func(val interface{}) (int64, bool) {
		switch v := val.(type) {
		case int64:
			return v, true
		case int:
			return int64(v), true
		}
		return 0, false
	}

	toFloat := func(val interface{}) (float64, bool) {
		switch v := val.(type) {
		case float64:
			return v, true
		case int64:
			return float64(v), true
		case int:
			return float64(v), true
		}
		return 0, false
	}

	if l, ok := toInt(left); ok {
		if r, ok := toInt(right); ok {
			switch op {
			case parser.TokenPlus:
				return l + r
			case parser.TokenMinus:
				return l - r
			case parser.TokenStar:
				return l * r
			case parser.TokenSlash:
				if r != 0 {
					return l / r
				}
			case parser.TokenPercent:
				if r != 0 {
					return l % r
				}
			}
		}
	}

	if l, ok := toFloat(left); ok {
		if r, ok := toFloat(right); ok {
			switch op {
			case parser.TokenPlus:
				return l + r
			case parser.TokenMinus:
				return l - r
			case parser.TokenStar:
				return l * r
			case parser.TokenSlash:
				if r != 0 {
					return l / r
				}
			}
		}
	}

	return nil
}

func (v *Validator) evaluateUnary(op parser.TokenType, val interface{}) interface{} {
	if val == nil {
		return nil
	}

	switch op {
	case parser.TokenMinus:
		switch v := val.(type) {
		case int64:
			return -v
		case float64:
			return -v
		}
	case parser.TokenSymbol: // ! is Symbol?
		// Parser uses TokenSymbol for ! ?
		// Lexer: '!' -> Symbol.
		if b, ok := val.(bool); ok {
			return !b
		}
	}
	return nil
}

func (v *Validator) validateSignal(node *index.ProjectNode, fields map[string][]index.EvaluatedField) {
	// ... (same as before)
	if typeFields, ok := fields["Type"]; !ok || len(typeFields) == 0 {
		v.report(node, "missing_signal_type", LevelError,
			fmt.Sprintf("Signal '%s' is missing mandatory field 'Type'", node.RealName),
			v.getNodePosition(node), v.getNodeFile(node))
	} else {
		typeVal := typeFields[0].Value
		var typeStr string
		switch t := typeVal.(type) {
		case *parser.StringValue:
			typeStr = t.Value
		case *parser.ReferenceValue:
			typeStr = t.Value
		default:
			v.report(node, "invalid_signal_type", LevelError,
				fmt.Sprintf("Field 'Type' in Signal '%s' must be a type name", node.RealName),
				typeFields[0].Raw.Position, typeFields[0].File)
			return
		}

		if !isValidType(typeStr) {
			v.report(node, "invalid_signal_type", LevelError,
				fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeStr, node.RealName),
				typeFields[0].Raw.Position, typeFields[0].File)
		}
	}

	v.validateByteSize(node, fields)
}

func (v *Validator) validateGAM(node *index.ProjectNode) {
	if inputs, ok := node.Children["InputSignals"]; ok {
		v.validateGAMSignals(node, inputs, "Input")
	}
	if outputs, ok := node.Children["OutputSignals"]; ok {
		v.validateGAMSignals(node, outputs, "Output")
	}
}

func (v *Validator) validateDataSource(node *index.ProjectNode) {
	// Hooks for DataSource specialized validation
	// switch v.getNodeClass(node) { ... }
}

func (v *Validator) validateGAMSignals(gamNode, signalsContainer *index.ProjectNode, direction string) {
	for _, signal := range signalsContainer.Children {
		if !v.nodeActive(signal) {
			continue // belongs to the `#if` branch that was not selected
		}
		v.validateGAMSignal(gamNode, signal, direction)
	}
}

func (v *Validator) validateGAMSignal(gamNode, signalNode *index.ProjectNode, direction string) {
	fields := v.getFields(signalNode)
	var dsName string
	if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
		dsName = v.getFieldValue(dsFields[0], signalNode)
	}

	if dsName == "" {
		return // Ignore implicit signals or missing datasource (handled elsewhere if mandatory)
	}

	dsNode := v.resolveReference(dsName, signalNode, v.Tree.IsDataSource)
	if dsNode == nil {
		v.report(signalNode, "unknown_datasource", LevelError,
			fmt.Sprintf("Unknown DataSource '%s' referenced in signal '%s'", dsName, signalNode.RealName),
			v.getNodePosition(signalNode), v.getNodeFile(signalNode))
		return
	}

	// Link DataSource reference
	if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
		if val, ok := dsFields[0].Raw.Value.(*parser.ReferenceValue); ok {
			v.updateReferenceTarget(v.getNodeFile(signalNode), val.Position, dsNode)
		}
	}

	// Check Direction using CUE Schema
	dsClass := v.getNodeClass(dsNode)
	if dsClass != "" {
		// Lookup class definition in Schema
		// path: #Classes.ClassName.#meta.direction
		path := cue.ParsePath(fmt.Sprintf("#Classes.%s.#meta.direction", dsClass))
		val := v.Schema.Value.LookupPath(path)

		if val.Err() == nil {
			dsDir, err := val.String()
			if err == nil && dsDir != "" {
				if direction == "Input" && dsDir == "OUT" {
					v.report(signalNode, "datasource_direction", LevelError,
						fmt.Sprintf("DataSource '%s' (Class %s) is Output-only but referenced in InputSignals of GAM '%s'", dsName, dsClass, gamNode.RealName),
						v.getNodePosition(signalNode), v.getNodeFile(signalNode))
				}
				if direction == "Output" && dsDir == "IN" {
					v.report(signalNode, "datasource_direction", LevelError,
						fmt.Sprintf("DataSource '%s' (Class %s) is Input-only but referenced in OutputSignals of GAM '%s'", dsName, dsClass, gamNode.RealName),
						v.getNodePosition(signalNode), v.getNodeFile(signalNode))
				}
			}
		}
	}

	// Check Signal Existence
	targetSignalName := index.NormalizeName(signalNode.RealName)
	if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
		targetSignalName = v.getFieldValue(aliasFields[0], signalNode) // Alias is usually the name in DataSource
	}

	var targetNode *index.ProjectNode
	if signalsContainer, ok := dsNode.Children["Signals"]; ok {
		targetNorm := index.NormalizeName(targetSignalName)

		if child, ok := signalsContainer.Children[targetNorm]; ok {
			targetNode = child
		} else {
			// Fallback check
			for _, child := range signalsContainer.Children {
				if index.NormalizeName(child.RealName) == targetNorm {
					targetNode = child
					break
				}
			}
		}
	}

	if targetNode == nil {
		if os.Getenv("MDT_SIG_DEBUG") != "" {
			src := "object"
			for _, f := range signalNode.Fragments {
				switch f.Source.(type) {
				case *parser.SignalShorthand:
					src = "shorthand"
				}
			}
			fmt.Fprintf(os.Stderr, "[SIG] %s src=%s pragmas=%v fields=%v\n", signalNode.RealName, src, signalNode.Pragmas, func() []string {
				k := []string{}
				for f := range signalNode.Fields {
					k = append(k, f)
				}
				return k
			}())
		}
		v.report(signalNode, "implicit_signal", LevelWarning,
			fmt.Sprintf("Implicitly Defined Signal: '%s' is defined in GAM '%s' but not in DataSource '%s'", targetSignalName, gamNode.RealName, dsName),
			v.getNodePosition(signalNode), v.getNodeFile(signalNode))

		if typeFields, ok := fields["Type"]; !ok || len(typeFields) == 0 {
			v.report(signalNode, "implicit_signal_missing_type", LevelError,
				fmt.Sprintf("Implicit signal '%s' must define Type", targetSignalName),
				v.getNodePosition(signalNode), v.getNodeFile(signalNode))
		} else {
			// Check Type validity even for implicit
			typeVal := v.getFieldValue(typeFields[0], signalNode)
			if !isValidType(typeVal) {
				v.report(signalNode, "invalid_signal_type", LevelError,
					fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeVal, signalNode.RealName),
					typeFields[0].Raw.Position, typeFields[0].File)
			}
		}
	} else {
		signalNode.Target = targetNode
		// Link Alias reference
		if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
			if val, ok := aliasFields[0].Raw.Value.(*parser.ReferenceValue); ok {
				v.updateReferenceTarget(v.getNodeFile(signalNode), val.Position, targetNode)
			}
		}

		// Property checks
		v.checkSignalProperty(signalNode, targetNode, "Type")

		// `Samples` multiplies the signal size. Pairing it with a DataSource
		// signal that already declares NumberOfElements multiplies twice,
		// which is the usual cause of an unexpected IOGAM size mismatch.
		if own := v.signalSize(signalNode); own.Samples > 1 {
			if target := v.signalSize(targetNode); target.Elements > 1 {
				v.report(signalNode, "samples_multiplied", LevelWarning,
					fmt.Sprintf("Signal '%s': Samples=%d multiplies '%s::%s', which already declares %d elements — the GAM buffer is %d×%d elements (%d bytes)",
						signalNode.RealName, own.Samples, dsName, targetNode.RealName, target.Elements, target.Elements, own.Samples, own.Total),
					v.getNodePosition(signalNode), v.getNodeFile(signalNode))
			}
		}

		// Validate Ranges
		if rangeFields, ok := fields["Ranges"]; ok && len(rangeFields) > 0 {
			val := rangeFields[0].Value
			if arr, ok := val.(*parser.ArrayValue); ok {
				for _, elem := range arr.Elements {
					if inner, ok := elem.(*parser.ArrayValue); ok {
						if len(inner.Elements) != 2 {
							v.report(signalNode, "invalid_ranges_format", LevelError,
								fmt.Sprintf("Ranges element must be a 2D vector [start, end], found %d elements", len(inner.Elements)),
								inner.Position, rangeFields[0].File)
						}
					} else {
						v.report(signalNode, "invalid_ranges_format", LevelError,
							"Ranges property must be a 2D vector (array of arrays), e.g. {{0, 0}}",
							elem.Pos(), rangeFields[0].File)
					}
				}
			} else {
				v.report(signalNode, "invalid_ranges_format", LevelError,
					"Ranges property must be an array, e.g. {{0, 0}}",
					val.Pos(), rangeFields[0].File)
			}
		}

		// If Ranges or Samples are present, NumberOfElements/Dimensions might differ legitimately (local override/modification)
		hasModifiers := false
		if r, ok := fields["Ranges"]; ok && len(r) > 0 {
			hasModifiers = true
		}
		if s, ok := fields["Samples"]; ok && len(s) > 0 {
			hasModifiers = true
		}

		if !hasModifiers {
			v.checkSignalProperty(signalNode, targetNode, "NumberOfElements")
			v.checkSignalProperty(signalNode, targetNode, "NumberOfDimensions")
		}

		// Check Type validity if present
		if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
			typeVal := v.getFieldValue(typeFields[0], signalNode)
			if !isValidType(typeVal) {
				v.report(signalNode, "invalid_signal_type", LevelError,
					fmt.Sprintf("Invalid Type '%s' for Signal '%s'", typeVal, signalNode.RealName),
					typeFields[0].Raw.Position, typeFields[0].File)
			}
		}
	}

	// Validate ByteSize
	v.validateByteSize(signalNode, fields)

	// Validate Value initialization
	if valField, hasValue := fields["Value"]; hasValue && len(valField) > 0 {
		var typeStr string
		if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
			typeStr = v.getFieldValue(typeFields[0], signalNode)
		} else if signalNode.Target != nil {
			if tFields, ok := signalNode.Target.Fields["Type"]; ok && len(tFields) > 0 {
				typeStr = v.getFieldValue(tFields[0], signalNode.Target)
			} else if t, ok := signalNode.Target.Metadata["Type"]; ok {
				typeStr = t
			}
		}

		if typeStr != "" && v.Schema != nil {
			ctx := v.Schema.Context
			typeVal := ctx.CompileString(typeStr)
			if typeVal.Err() == nil {
				valInterface := v.ValueToInterface(valField[0].Value, signalNode)
				valVal := ctx.Encode(valInterface)
				res := typeVal.Unify(valVal)
				if err := res.Validate(cue.Concrete(true)); err != nil {
					v.report(signalNode, "signal_value_mismatch", LevelError,
						fmt.Sprintf("Value initialization mismatch for signal '%s': %v", signalNode.RealName, err),
						valField[0].Raw.Position, v.getNodeFile(signalNode))
				}
			}
		}
	}
}

func (v *Validator) getEvaluatedMetadata(node *index.ProjectNode, key string) string {
	if fields, ok := v.getFields(node)[key]; ok && len(fields) > 0 {
		return v.getFieldValue(fields[len(fields)-1], node)
	}
	return node.Metadata[key]
}

// signalSize describes how a signal's byte size is derived: the type, the
// element count (NumberOfElements × NumberOfDimensions, or the Ranges total)
// and the Samples multiplier. Diagnostics use the parts to explain a mismatch.
type signalSize struct {
	Type     string
	TypeSize int64
	Elements int64
	Samples  int64
	Total    int64
}

func (v *Validator) getSignalByteSize(node *index.ProjectNode) int64 {
	return v.signalSize(node).Total
}

func (v *Validator) signalSize(node *index.ProjectNode) signalSize {
	fields := v.getFields(node)

	// Get Type
	typeStr := ""
	if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
		typeStr = v.getFieldValue(typeFields[0], node)
	} else if node.Target != nil {
		if tFields, ok := node.Target.Fields["Type"]; ok && len(tFields) > 0 {
			typeStr = v.getFieldValue(tFields[0], node.Target)
		} else if t, ok := node.Target.Metadata["Type"]; ok {
			typeStr = t
		}
	}

	if typeStr == "" {
		return signalSize{}
	}

	typeSize := int64(v.getTypeSize(typeStr))
	if typeSize <= 0 {
		return signalSize{Type: typeStr} // Variable size or unknown type
	}

	totalElements := int64(1)

	// 1. Check for Ranges (Overrides base dimensions)
	if rangeFields, ok := fields["Ranges"]; ok && len(rangeFields) > 0 {
		if arr, ok := rangeFields[0].Value.(*parser.ArrayValue); ok {
			totalElements = 1
			for _, elem := range arr.Elements {
				if inner, ok := elem.(*parser.ArrayValue); ok && len(inner.Elements) == 2 {
					start := v.ValueToInterface(inner.Elements[0], node)
					stop := v.ValueToInterface(inner.Elements[1], node)

					var iStart, iStop int64
					if s, ok := start.(int64); ok {
						iStart = s
					} else if s, ok := start.(int); ok {
						iStart = int64(s)
					}
					if s, ok := stop.(int64); ok {
						iStop = s
					} else if s, ok := stop.(int); ok {
						iStop = int64(s)
					}

					if iStop >= iStart {
						totalElements *= (iStop - iStart + 1)
					}
				}
			}
		}
	} else {
		// Base elements calculation
		numElements := int64(1)
		if neFields, ok := fields["NumberOfElements"]; ok && len(neFields) > 0 {
			val := v.ValueToInterface(neFields[0].Value, node)
			if i, ok := val.(int64); ok {
				numElements = i
			} else if i, ok := val.(int); ok {
				numElements = int64(i)
			}
		} else if node.Target != nil {
			if neFields, ok := node.Target.Fields["NumberOfElements"]; ok && len(neFields) > 0 {
				val := v.ValueToInterface(neFields[0].Value, node.Target)
				if i, ok := val.(int64); ok {
					numElements = i
				} else if i, ok := val.(int); ok {
					numElements = int64(i)
				}
			} else if ne, ok := node.Target.Metadata["NumberOfElements"]; ok {
				if i, err := strconv.ParseInt(ne, 0, 64); err == nil {
					numElements = i
				}
			}
		}

		numDimensions := int64(1)
		if ndFields, ok := fields["NumberOfDimensions"]; ok && len(ndFields) > 0 {
			val := v.ValueToInterface(ndFields[0].Value, node)
			if i, ok := val.(int64); ok {
				numDimensions = i
			} else if i, ok := val.(int); ok {
				numDimensions = int64(i)
			}
		} else if node.Target != nil {
			if ndFields, ok := node.Target.Fields["NumberOfDimensions"]; ok && len(ndFields) > 0 {
				val := v.ValueToInterface(ndFields[0].Value, node.Target)
				if i, ok := val.(int64); ok {
					numDimensions = i
				} else if i, ok := val.(int); ok {
					numDimensions = int64(i)
				}
			} else if nd, ok := node.Target.Metadata["NumberOfDimensions"]; ok {
				if i, err := strconv.ParseInt(nd, 0, 64); err == nil {
					numDimensions = i
				}
			}
		}
		totalElements = numElements * numDimensions
	}

	// 2. Check for Samples multiplier
	samples := int64(0)
	if sampleFields, ok := fields["Samples"]; ok && len(sampleFields) > 0 {
		val := v.ValueToInterface(sampleFields[0].Value, node)
		if i, ok := val.(int64); ok {
			samples = i
		} else if i, ok := val.(int); ok {
			samples = int64(i)
		}
	}
	if samples > 0 {
		totalElements *= samples
	}

	return signalSize{
		Type:     typeStr,
		TypeSize: typeSize,
		Elements: totalElements,
		Samples:  samples,
		Total:    typeSize * totalElements,
	}
}

// describe renders the size derivation as `type×elements[×samples]=bytes`.
func (s signalSize) describe() string {
	if s.Type == "" || s.Total == 0 {
		return "unknown size"
	}
	text := fmt.Sprintf("%s×%d", s.Type, s.Elements)
	if s.Samples > 1 && s.Elements%s.Samples == 0 {
		text = fmt.Sprintf("%s×%d×%dsamples", s.Type, s.Elements/s.Samples, s.Samples)
	}
	return fmt.Sprintf("%s=%d", text, s.Total)
}

// checkIOGAMSize reports an IOGAM whose input and output memories differ in
// size, listing how each signal's size was derived. Without it the only hint
// was CUE's "conflicting values N and M" on #Object.InputSize.
func (v *Validator) checkIOGAMSize(node *index.ProjectNode) {
	inDesc, inTotal, inOK := v.signalsSize(node.Children["InputSignals"])
	outDesc, outTotal, outOK := v.signalsSize(node.Children["OutputSignals"])
	if !inOK || !outOK || inTotal == outTotal {
		return
	}

	v.mu.Lock()
	if v.sizeMismatch == nil {
		v.sizeMismatch = make(map[*index.ProjectNode]bool)
	}
	v.sizeMismatch[node] = true
	v.mu.Unlock()

	v.report(node, "iogam_size", LevelError,
		fmt.Sprintf("IOGAM '%s': input memory is %d bytes but output memory is %d bytes — in: %s | out: %s (the IOGAM copies the whole input memory; Samples multiplies a signal size, NumberOfElements sets the element count)",
			node.RealName, inTotal, outTotal, inDesc, outDesc),
		v.getNodePosition(node), v.getNodeFile(node))
}

// signalsSize sums the active signals of a GAM signal container and describes
// each contribution. ok is false when a signal's size is unknown, so a partial
// sum is never reported as a mismatch.
func (v *Validator) signalsSize(container *index.ProjectNode) (string, int64, bool) {
	if container == nil {
		return "", 0, false
	}
	names := make([]string, 0, len(container.Children))
	for name, sig := range container.Children {
		if !v.nodeActive(sig) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", 0, false
	}

	var total int64
	parts := make([]string, 0, len(names))
	for i, name := range names {
		size := v.signalSize(container.Children[name])
		if size.Total == 0 {
			return "", 0, false
		}
		total += size.Total
		if i < 8 {
			parts = append(parts, fmt.Sprintf("%s %s", name, size.describe()))
		}
	}
	if len(names) > 8 {
		parts = append(parts, "…")
	}
	return strings.Join(parts, ", "), total, true
}

func (v *Validator) validateByteSize(node *index.ProjectNode, fields map[string][]index.EvaluatedField) {
	expectedSize := v.getSignalByteSize(node)
	if expectedSize == 0 {
		return
	}

	// Check ByteSize or ByteDimension
	sizeFields := [][]index.EvaluatedField{}
	if f, ok := fields["ByteSize"]; ok {
		sizeFields = append(sizeFields, f)
	}
	if f, ok := fields["ByteDimension"]; ok {
		sizeFields = append(sizeFields, f)
	}

	for _, fs := range sizeFields {
		if len(fs) > 0 {
			val := v.ValueToInterface(fs[0].Value, node)
			var definedSize int64
			if i, ok := val.(int64); ok {
				definedSize = i
			} else if i, ok := val.(int); ok {
				definedSize = int64(i)
			} else {
				continue
			}

			if definedSize != expectedSize {
				v.report(node, "signal_size_mismatch", LevelError,
					fmt.Sprintf("Size mismatch for signal '%s': defined %d, expected %d", node.RealName, definedSize, expectedSize),
					fs[0].Raw.Position, v.getNodeFile(node))
			}
		}
	}
}

func (v *Validator) getTypeSize(typeName string) int {
	switch typeName {
	case "uint8", "int8", "char8", "bool":
		return 1
	case "uint16", "int16":
		return 2
	case "uint32", "int32", "float32":
		return 4
	case "uint64", "int64", "float64":
		return 8
	case "string":
		return -1 // Variable size, usually pointer or handled differently
	}
	return 0 // Unknown
}

func (v *Validator) checkSignalProperty(gamSig, dsSig *index.ProjectNode, prop string) {
	gamVal := v.getEvaluatedMetadata(gamSig, prop)
	dsVal := v.getEvaluatedMetadata(dsSig, prop)

	if gamVal == "" {
		return
	}

	if dsVal != "" && gamVal != dsVal {
		if prop == "Type" {
			if v.checkCastPragma(gamSig, dsVal, gamVal) {
				return
			}
		}

		v.report(gamSig, "signal_property_mismatch", LevelError,
			fmt.Sprintf("Signal '%s' property '%s' mismatch: defined '%s', referenced '%s'", gamSig.RealName, prop, dsVal, gamVal),
			v.getNodePosition(gamSig), v.getNodeFile(gamSig))
	}
}

func (v *Validator) checkCastPragma(node *index.ProjectNode, defType, curType string) bool {
	for _, p := range node.Pragmas {
		if strings.HasPrefix(p, "cast(") {
			content := strings.TrimPrefix(p, "cast(")
			if idx := strings.Index(content, ")"); idx != -1 {
				content = content[:idx]
				parts := strings.Split(content, ",")
				if len(parts) == 2 {
					d := strings.TrimSpace(parts[0])
					c := strings.TrimSpace(parts[1])
					if d == defType && c == curType {
						return true
					}
				}
			}
		}
	}
	return false
}

func (v *Validator) updateReferenceTarget(file string, pos parser.Position, target *index.ProjectNode) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if refs, ok := v.Tree.FileReferences[file]; ok {
		for i := range refs {
			if refs[i].Position == pos {
				refs[i].Target = target
				// Also update legacy slice if necessary, but FileReferences is primary now
				// pt.References will be rebuilt by ResolveReferences anyway?
				// But we are in middle of validation.
				// For now, update map entry.
				return
			}
		}
	}
}

// Helpers

func (v *Validator) getFields(node *index.ProjectNode) map[string][]index.EvaluatedField {
	v.muActive.Lock()
	defer v.muActive.Unlock()

	activeDefs := make(map[parser.Definition]bool)
	allDefs := make(map[parser.Definition]bool)
	hasActiveFragment := false
	for _, frag := range node.Fragments {
		active := v.ActiveFragments[frag]
		if active {
			hasActiveFragment = true
		}
		for _, d := range frag.Definitions {
			allDefs[d] = true
			if active {
				activeDefs[d] = true
			}
		}
	}

	fields := make(map[string][]index.EvaluatedField, len(node.Fields))
	for name, defs := range node.Fields {
		for _, f := range defs {
			// When the node has an active fragment, values from the other,
			// unselected fragments of the same node (e.g. the two branches
			// of a signal shorthand) must not shadow or contradict it.
			// Fields that cannot be traced to any fragment are synthesized
			// and always kept.
			if hasActiveFragment && allDefs[f.Raw] && !activeDefs[f.Raw] {
				continue
			}
			fields[name] = append(fields[name], f)
		}
	}
	return fields
}

func (v *Validator) getFieldValue(f index.EvaluatedField, ctx *index.ProjectNode) string {
	res := v.ValueToInterface(f.Value, ctx)
	if res == nil {
		return ""
	}
	return fmt.Sprintf("%v", res)
}

func (v *Validator) resolveReference(name string, ctx *index.ProjectNode, predicate func(*index.ProjectNode) bool) *index.ProjectNode {
	target := v.Tree.ResolveName(ctx, name, predicate)
	if target != nil {
		v.muActive.Lock()
		active := v.ActiveNodes[target]
		v.muActive.Unlock()
		if !active {
			return nil
		}
	}
	return target
}

func (v *Validator) getNodeClass(node *index.ProjectNode) string {
	if cls, ok := node.Metadata["Class"]; ok {
		// Strip namespace if present
		if idx := strings.LastIndex(cls, "::"); idx != -1 {
			return cls[idx+2:]
		}
		return cls
	}
	return ""
}

func isValidType(t string) bool {
	switch t {
	case "uint8", "int8", "uint16", "int16", "uint32", "int32", "uint64", "int64",
		"float32", "float64", "string", "bool", "char8":
		return true
	}
	return false
}

func (v *Validator) CheckUnused(ctx context.Context) {
	referencedNodes := make(map[*index.ProjectNode]bool)
	for _, ref := range v.Tree.References {
		if !v.isPositionActive(ref.File, ref.Position) {
			continue
		}
		if ref.Target != nil {
			referencedNodes[ref.Target] = true
		}
	}

	if v.Tree.Root != nil {
		v.collectTargetUsage(v.Tree.Root, referencedNodes)
	}
	for _, node := range v.Tree.IsolatedFiles {
		v.collectTargetUsage(node, referencedNodes)
	}

	if v.Tree.Root != nil {
		v.checkUnusedRecursive(ctx, v.Tree.Root, referencedNodes)
	}
	for _, node := range v.Tree.IsolatedFiles {
		v.checkUnusedRecursive(ctx, node, referencedNodes)
	}
}

func (v *Validator) collectTargetUsage(node *index.ProjectNode, referenced map[*index.ProjectNode]bool) {
	if node.Target != nil {
		referenced[node.Target] = true
	}
	for _, child := range node.Children {
		v.collectTargetUsage(child, referenced)
	}
}

func (v *Validator) checkUnusedRecursive(ctx context.Context, node *index.ProjectNode, referenced map[*index.ProjectNode]bool) {
	if ctx.Err() != nil {
		return
	}
	v.muActive.Lock()
	active := v.ActiveNodes[node]
	v.muActive.Unlock()
	if !active {
		return
	}
	// Heuristic for GAM
	if v.Tree.IsGAM(node) {
		if !referenced[node] {
			v.report(node, "unused_gam", LevelWarning,
				fmt.Sprintf("Unused GAM: %s is defined but not referenced in any thread or scheduler", node.RealName),
				v.getNodePosition(node), v.getNodeFile(node))
		}
	}

	// Heuristic for DataSource and its signals
	if v.Tree.IsDataSource(node) {
		if signalsNode, ok := node.Children["Signals"]; ok {
			for _, signal := range signalsNode.Children {
				if !referenced[signal] {
					v.report(signal, "unused_signal", LevelWarning,
						fmt.Sprintf("Unused Signal: %s is defined in DataSource %s but never referenced", signal.RealName, node.RealName),
						v.getNodePosition(signal), v.getNodeFile(signal))
				}
			}
		}
	}

	for _, child := range node.Children {
		v.checkUnusedRecursive(ctx, child, referenced)
	}
}

func (v *Validator) getNodePosition(node *index.ProjectNode) parser.Position {
	v.muActive.Lock()
	defer v.muActive.Unlock()
	for _, frag := range node.Fragments {
		if v.ActiveFragments[frag] {
			return frag.ObjectPos
		}
	}
	if len(node.Fragments) > 0 {
		return node.Fragments[0].ObjectPos
	}
	return parser.Position{Line: 1, Column: 1}
}

func (v *Validator) getNodeFile(node *index.ProjectNode) string {
	v.muActive.Lock()
	defer v.muActive.Unlock()
	for _, frag := range node.Fragments {
		if v.ActiveFragments[frag] {
			return frag.File
		}
	}
	if len(node.Fragments) > 0 {
		return node.Fragments[0].File
	}
	return ""
}

func (v *Validator) checkFunctionsArray(node *index.ProjectNode, fields map[string][]index.EvaluatedField) {
	if funcs, ok := fields["Functions"]; ok && len(funcs) > 0 {
		f := funcs[0]
		if arr, ok := f.Value.(*parser.ArrayValue); ok {
			for _, elem := range arr.Elements {
				if ref, ok := elem.(*parser.ReferenceValue); ok {
					target := v.resolveReference(ref.Value, node, v.Tree.IsGAM)
					if target == nil {
						v.report(node, "invalid_function", LevelError,
							fmt.Sprintf("Function '%s' not found or is not a valid GAM", ref.Value),
							ref.Position, v.getNodeFile(node))
					}
				} else {
					v.report(node, "invalid_function", LevelError,
						"Functions array must contain references",
						f.Raw.Position, v.getNodeFile(node))
				}
			}
		}
	}
}

func (v *Validator) isGloballyAllowed(warningType string, contextFile string) bool {
	prefix1 := fmt.Sprintf("allow(%s)", warningType)
	prefix2 := fmt.Sprintf("ignore(%s)", warningType)

	// If context file is isolated, only check its own pragmas
	if _, isIsolated := v.Tree.IsolatedFiles[contextFile]; isIsolated {
		if pragmas, ok := v.Tree.GlobalPragmas[contextFile]; ok {
			for _, p := range pragmas {
				normalized := strings.ReplaceAll(p, " ", "")
				if strings.HasPrefix(normalized, prefix1) || strings.HasPrefix(normalized, prefix2) {
					return true
				}
			}
		}
		return false
	}

	// If project file, check all non-isolated files
	for file, pragmas := range v.Tree.GlobalPragmas {
		if _, isIsolated := v.Tree.IsolatedFiles[file]; isIsolated {
			continue
		}
		for _, p := range pragmas {
			normalized := strings.ReplaceAll(p, " ", "")
			if strings.HasPrefix(normalized, prefix1) || strings.HasPrefix(normalized, prefix2) {
				return true
			}
		}
	}
	return false
}

func (v *Validator) CheckDataSourceThreading(ctx context.Context) {
	if v.Tree.Root == nil {
		return
	}

	var appNodes []*index.ProjectNode
	findApp := func(n *index.ProjectNode) {
		if cls, ok := n.Metadata["Class"]; ok && cls == "RealTimeApplication" {
			appNodes = append(appNodes, n)
		}
	}
	v.Tree.Walk(findApp)

	for _, appNode := range appNodes {
		if ctx.Err() != nil {
			return
		}
		v.checkAppDataSourceThreading(ctx, appNode)
	}
}

func (v *Validator) checkAppDataSourceThreading(ctx context.Context, appNode *index.ProjectNode) {
	// 2. Find States
	var statesNode *index.ProjectNode
	if s, ok := appNode.Children["States"]; ok {
		statesNode = s
	} else {
		for _, child := range appNode.Children {
			if cls, ok := child.Metadata["Class"]; ok && cls == "StateMachine" {
				statesNode = child
				break
			}
		}
	}

	if statesNode == nil {
		return
	}

	// 3. Iterate States
	for _, state := range statesNode.Children {
		dsUsage := make(map[*index.ProjectNode]string) // DS Node -> Thread Name
		var threads []*index.ProjectNode

		// Search for threads in the state (either direct children or inside "Threads" container)
		for _, child := range state.Children {
			if child.RealName == "Threads" {
				for _, t := range child.Children {
					if cls, ok := t.Metadata["Class"]; ok && cls == "RealTimeThread" {
						threads = append(threads, t)
					}
				}
			} else {
				if cls, ok := child.Metadata["Class"]; ok && cls == "RealTimeThread" {
					threads = append(threads, child)
				}
			}
		}

		for _, thread := range threads {
			gams := v.getThreadGAMs(thread)
			for _, gam := range gams {
				dss := v.getGAMDataSources(gam)
				for _, ds := range dss {
					if existingThread, ok := dsUsage[ds]; ok {
						if existingThread != thread.RealName {
							if !v.isMultithreaded(ds) {
								v.report(gam, "datasource_threading", LevelError,
									fmt.Sprintf("DataSource '%s' is not multithreaded but used in multiple threads (%s, %s) in state '%s'", ds.RealName, existingThread, thread.RealName, state.RealName),
									v.getNodePosition(gam), v.getNodeFile(gam))
							}
						}
					} else {
						dsUsage[ds] = thread.RealName
					}
				}
			}
		}
	}
}

func (v *Validator) getThreadGAMs(thread *index.ProjectNode) []*index.ProjectNode {
	var gams []*index.ProjectNode
	fields := v.getFields(thread)
	if funcs, ok := fields["Functions"]; ok && len(funcs) > 0 {
		f := funcs[0]
		if arr, ok := f.Value.(*parser.ArrayValue); ok {
			for _, elem := range arr.Elements {
				if ref, ok := elem.(*parser.ReferenceValue); ok {
					target := v.resolveReference(ref.Value, thread, v.Tree.IsGAM)
					if target != nil {
						gams = append(gams, target)
					}
				}
			}
		}
	}
	return gams
}

func (v *Validator) getGAMDataSources(gam *index.ProjectNode) []*index.ProjectNode {
	dsMap := make(map[*index.ProjectNode]bool)

	processSignals := func(container *index.ProjectNode) {
		if container == nil {
			return
		}
		for _, sig := range container.Children {
			if !v.nodeActive(sig) {
				continue // defined in the `#if` branch that was not selected
			}
			fields := v.getFields(sig)
			if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
				dsName := v.getFieldValue(dsFields[0], sig)
				dsNode := v.resolveReference(dsName, sig, v.Tree.IsDataSource)
				if dsNode != nil {
					dsMap[dsNode] = true
				}
			}
		}
	}

	processSignals(gam.Children["InputSignals"])
	processSignals(gam.Children["OutputSignals"])

	var dss []*index.ProjectNode
	for ds := range dsMap {
		dss = append(dss, ds)
	}
	return dss
}

func (v *Validator) isMultithreaded(ds *index.ProjectNode) bool {
	if meta, ok := ds.Children["#meta"]; ok {
		fields := v.getFields(meta)
		if mt, ok := fields["multithreaded"]; ok && len(mt) > 0 {
			val := v.getFieldValue(mt[0], meta)
			return val == "true"
		}
	}
	return false
}

func (v *Validator) CheckINOUTOrdering(ctx context.Context) {
	if v.Tree.Root == nil {
		return
	}

	var appNodes []*index.ProjectNode
	findApp := func(n *index.ProjectNode) {
		if cls, ok := n.Metadata["Class"]; ok && cls == "RealTimeApplication" {
			appNodes = append(appNodes, n)
		}
	}
	v.Tree.Walk(findApp)

	for _, appNode := range appNodes {
		if ctx.Err() != nil {
			return
		}
		v.checkAppINOUTOrdering(ctx, appNode)
	}
}

func (v *Validator) checkAppINOUTOrdering(ctx context.Context, appNode *index.ProjectNode) {
	var statesNode *index.ProjectNode
	if s, ok := appNode.Children["States"]; ok {
		statesNode = s
	} else {
		for _, child := range appNode.Children {
			if cls, ok := child.Metadata["Class"]; ok && cls == "StateMachine" {
				statesNode = child
				break
			}
		}
	}

	if statesNode == nil {
		return
	}

	for _, state := range statesNode.Children {
		var threads []*index.ProjectNode
		for _, child := range state.Children {
			if child.RealName == "Threads" {
				for _, t := range child.Children {
					if cls, ok := t.Metadata["Class"]; ok && cls == "RealTimeThread" {
						threads = append(threads, t)
					}
				}
			} else {
				if cls, ok := child.Metadata["Class"]; ok && cls == "RealTimeThread" {
					threads = append(threads, child)
				}
			}
		}

		for _, thread := range threads {
			producedSignals := make(map[*index.ProjectNode]map[string][]*index.ProjectNode)
			consumedSignals := make(map[*index.ProjectNode]map[string]bool)

			gams := v.getThreadGAMs(thread)
			for _, gam := range gams {
				v.processGAMSignalsForOrdering(gam, "InputSignals", producedSignals, consumedSignals, true, thread, state)
				v.processGAMSignalsForOrdering(gam, "OutputSignals", producedSignals, consumedSignals, false, thread, state)
			}
			// Check for produced but not consumed
			for ds, signals := range producedSignals {
				for sigName, producers := range signals {
					consumed := false
					if cSet, ok := consumedSignals[ds]; ok {
						if cSet[sigName] {
							consumed = true
						}
					}
					if !consumed {
						for _, prod := range producers {
							v.report(prod, "not_consumed", LevelWarning,
								fmt.Sprintf("INOUT Signal '%s' (DS '%s') is produced in thread '%s' but never consumed in the same thread.", sigName, ds.RealName, thread.RealName),
								v.getNodePosition(prod), v.getNodeFile(prod))
						}
					}
				}
			}
		}
	}
}

func (v *Validator) processGAMSignalsForOrdering(gam *index.ProjectNode, containerName string, produced map[*index.ProjectNode]map[string][]*index.ProjectNode, consumed map[*index.ProjectNode]map[string]bool, isInput bool, thread, state *index.ProjectNode) {
	container := gam.Children[containerName]
	if container == nil {
		return
	}
	for _, sig := range container.Children {
		if !v.nodeActive(sig) {
			continue // defined in the `#if` branch that was not selected
		}
		fields := v.getFields(sig)
		var dsNode *index.ProjectNode
		var sigName string

		if sig.Target != nil {
			if sig.Target.Parent != nil && sig.Target.Parent.Parent != nil {
				dsNode = sig.Target.Parent.Parent
				sigName = sig.Target.RealName
			}
		}

		if dsNode == nil {
			if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
				dsName := v.getFieldValue(dsFields[0], sig)
				dsNode = v.resolveReference(dsName, sig, v.Tree.IsDataSource)
			}
			if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
				sigName = v.getFieldValue(aliasFields[0], sig)
			} else {
				sigName = sig.RealName
			}
		}

		if dsNode == nil || sigName == "" {
			continue
		}

		sigName = index.NormalizeName(sigName)

		if v.isMultithreaded(dsNode) {
			continue
		}

		dir := v.getDataSourceDirection(dsNode)
		if dir != "INOUT" {
			continue
		}

		if isInput {
			// Check if signal has 'Value' field - treat as produced/initialized
			if _, hasValue := fields["Value"]; hasValue {
				if produced[dsNode] == nil {
					produced[dsNode] = make(map[string][]*index.ProjectNode)
				}
				produced[dsNode][sigName] = append(produced[dsNode][sigName], sig)
			}

			isProduced := false
			if set, ok := produced[dsNode]; ok {
				if len(set[sigName]) > 0 {
					isProduced = true
				}
			}

			if !isProduced {
				v.report(sig, "not_produced", LevelError,
					fmt.Sprintf("INOUT Signal '%s' (DS '%s') is consumed by GAM '%s' in thread '%s' (State '%s') before being produced by any previous GAM.", sigName, dsNode.RealName, gam.RealName, thread.RealName, state.RealName),
					v.getNodePosition(sig), v.getNodeFile(sig))
			}

			if consumed[dsNode] == nil {
				consumed[dsNode] = make(map[string]bool)
			}
			consumed[dsNode][sigName] = true
		} else {
			if produced[dsNode] == nil {
				produced[dsNode] = make(map[string][]*index.ProjectNode)
			}
			produced[dsNode][sigName] = append(produced[dsNode][sigName], sig)
		}
	}
}

func (v *Validator) getDataSourceDirection(ds *index.ProjectNode) string {
	cls := v.getNodeClass(ds)
	if cls == "" {
		return ""
	}
	if v.Schema == nil {
		return ""
	}
	path := cue.ParsePath(fmt.Sprintf("#Classes.%s.#meta.direction", cls))
	val := v.Schema.Value.LookupPath(path)
	if val.Err() == nil {
		s, _ := val.String()
		return s
	}
	return ""
}

func (v *Validator) CheckSignalConsistency(ctx context.Context) {
	// Map: DataSourceNode -> SignalName -> List of Signals
	signals := make(map[*index.ProjectNode]map[string][]*index.ProjectNode)

	// Helper to collect signals
	collect := func(node *index.ProjectNode) {
		if ctx.Err() != nil {
			return
		}
		if !v.Tree.IsGAM(node) {
			return
		}
		// Check Input and Output
		for _, dir := range []string{"InputSignals", "OutputSignals"} {
			if container, ok := node.Children[dir]; ok {
				for _, sig := range container.Children {
					fields := v.getFields(sig)
					var dsNode *index.ProjectNode
					var sigName string

					// Resolve DS
					if dsFields, ok := fields["DataSource"]; ok && len(dsFields) > 0 {
						dsName := v.getFieldValue(dsFields[0], sig)
						if dsName != "" {
							dsNode = v.resolveReference(dsName, sig, v.Tree.IsDataSource)
						}
					}

					// Resolve Name (Alias or RealName)
					if aliasFields, ok := fields["Alias"]; ok && len(aliasFields) > 0 {
						sigName = v.getFieldValue(aliasFields[0], sig)
					} else {
						sigName = sig.RealName
					}

					if dsNode != nil && sigName != "" {
						sigName = index.NormalizeName(sigName)
						if signals[dsNode] == nil {
							signals[dsNode] = make(map[string][]*index.ProjectNode)
						}
						signals[dsNode][sigName] = append(signals[dsNode][sigName], sig)
					}
				}
			}
		}
	}

	v.Tree.Walk(collect)

	// Check Consistency
	for ds, sigMap := range signals {
		if ctx.Err() != nil {
			return
		}
		for sigName, usages := range sigMap {
			if len(usages) <= 1 {
				continue
			}

			// Check Type consistency
			var firstType string
			var firstNode *index.ProjectNode

			for _, u := range usages {
				// Get Type
				typeVal := ""
				fields := v.getFields(u)
				if typeFields, ok := fields["Type"]; ok && len(typeFields) > 0 {
					typeVal = v.getFieldValue(typeFields[0], u)
				}

				if typeVal == "" {
					continue
				}

				if firstNode == nil {
					firstType = typeVal
					firstNode = u
				} else {
					if typeVal != firstType {
						v.report(u, "signal_type_mismatch", LevelError,
							fmt.Sprintf("Signal Type Mismatch: Signal '%s' (in DS '%s') is defined as '%s' in '%s' but as '%s' in '%s'", sigName, ds.RealName, firstType, firstNode.Parent.Parent.RealName, typeVal, u.Parent.Parent.RealName),
							v.getNodePosition(u), v.getNodeFile(u))
					}
				}
			}
		}
	}
}

func (v *Validator) CheckVariables(ctx context.Context) {
	if v.Schema == nil {
		return
	}
	ctx_cue := v.Schema.Context

	checkNodeVars := func(node *index.ProjectNode) {
		if ctx.Err() != nil {
			return
		}
		seen := make(map[string]parser.Position)
		for _, frag := range node.Fragments {
			v.muActive.Lock()
			active := v.ActiveFragments[frag]
			v.muActive.Unlock()
			if !active {
				continue
			}
			for _, def := range frag.Definitions {
				if vdef, ok := def.(*parser.VariableDefinition); ok {
					// Variables are scoped to their block body: a name may be
					// declared again in a sibling block. Compare within the
					// same fragment branch.
					scopeKey := frag.BranchID + "\x00" + vdef.Name
					if prevPos, exists := seen[scopeKey]; exists {
						v.report(node, "duplicate_variable", LevelError,
							fmt.Sprintf("Duplicate variable definition: '%s' was already defined at %d:%d", vdef.Name, prevPos.Line, prevPos.Column),
							vdef.Position, frag.File)
					}
					seen[scopeKey] = vdef.Position

					if vdef.IsConst && vdef.DefaultValue == nil {
						v.report(node, "missing_variable_value", LevelError,
							fmt.Sprintf("Constant variable '%s' must have an initial value", vdef.Name),
							vdef.Position, frag.File)
						continue
					}

					// A struct literal needs a declared structured type. A
					// bare undeclared name with a non-struct value stays a
					// node/class reference (established convention).
					if _, isStructVal := vdef.DefaultValue.(*parser.MapValue); isStructVal &&
						!v.isDeclaredType(vdef.TypeExpr) && isBareTypeName(vdef.TypeExpr) {
						v.report(node, "unknown_type", LevelError,
							fmt.Sprintf("Unknown type '%s' for variable '%s' (declare it with `type %s { ... }`)", vdef.TypeExpr, vdef.Name, vdef.TypeExpr),
							vdef.Position, frag.File)
						continue
					}

					// Compile Type
					var typeCUE string
					if vdef.InlineType != nil {
						typeCUE = v.structTypeToCUE(vdef.InlineType, map[string]bool{})
					} else {
						typeCUE = v.translateVarTypeToCUE(vdef.TypeExpr, map[string]bool{})
					}
					typeVal := ctx_cue.CompileString(typeCUE)
					if typeVal.Err() != nil {
						v.report(node, "invalid_variable_type", LevelError,
							fmt.Sprintf("Invalid type expression for variable '%s': %v (CUE type: %s)", vdef.Name, typeVal.Err(), typeCUE),
							vdef.Position, frag.File)
						continue
					}

					// Validate the effective value: an override (-v) replaces
					// (or, for structured types, merges into) the default, so
					// overrides are checked against the declared type too.
					effective := vdef.DefaultValue
					if effective == nil {
						effective = v.Tree.EffectiveStructDefault(vdef)
					}
					if ov, ok := v.Overrides[vdef.Name]; ok {
						effective = ov
					}

					// A structured variable with no value anywhere would render
					// as an unresolved reference downstream; say so here.
					isStructType := vdef.InlineType != nil || v.isDeclaredType(vdef.TypeExpr)
					if isStructType && (effective == nil || index.IsNilValue(effective)) {
						v.report(node, "missing_struct_value", LevelWarning,
							fmt.Sprintf("Structured variable '%s' has no default value and no override", vdef.Name),
							vdef.Position, frag.File)
					}

					if effective != nil && !index.IsNilValue(effective) {
						valInterface := v.ValueToInterface(effective, node)
						valVal := ctx_cue.Encode(valInterface)

						// Unify
						res := typeVal.Unify(valVal)
						if err := res.Validate(cue.Concrete(true)); err != nil {
							v.report(node, "variable_value_mismatch", LevelError,
								fmt.Sprintf("Variable '%s' value mismatch: %v", vdef.Name, err),
								vdef.Position, frag.File)
						}
					}
				}
			}
		}
	}

	v.Tree.Walk(checkNodeVars)
}
func (v *Validator) CheckUnresolvedVariables(ctx context.Context) {
	for _, ref := range v.Tree.References {
		if ctx.Err() != nil {
			return
		}
		if !v.isPositionActive(ref.File, ref.Position) {
			continue
		}
		if ref.IsVariable && ref.TargetVariable == nil {
			name := strings.TrimPrefix(strings.TrimPrefix(ref.Name, "@"), "$")
			if v.Tree.BlockVars[name] {
				// Declared by a `let`/`var` inside a block body: bound by the
				// block expansion, not by a node variable.
				continue
			}
			v.report(nil, "unresolved_variable", LevelError,
				fmt.Sprintf("Unresolved variable reference: '@%s'", ref.Name),
				ref.Position, ref.File)
		}
	}
}

// nodeIsEffectivelyConditional returns true when node or any ancestor is
// IsConditional. This is needed because inner nodes of a conditional object
// (e.g. InputSignals inside a GAM that is inside #if) are created with
// IsConditional=false by addObjectFragment, but their GAM ancestor still
// carries IsConditional=true. Any reference made from such a node is already
// guarded by the ancestor's condition and must not be flagged.
func nodeIsEffectivelyConditional(node *index.ProjectNode) bool {
	curr := node
	for curr != nil {
		if curr.IsConditional {
			return true
		}
		curr = curr.Parent
	}
	return false
}

// CheckConditionalReferences reports an error when a field in an unconditional
// context holds a reference to a node that is only defined inside #if blocks.
// Example: `+Obj = { Ref = Data }` is invalid if `+Data` is only defined under
// `#if (COND) +Data = {...} #end`, because Data may not exist at runtime.
//
// No error is raised when the referencing node (or any of its ancestors) is
// itself conditional, because in that case both the reference site and the
// target are already guarded by some condition and we trust the user to use
// matching conditions.
func (v *Validator) CheckConditionalReferences(ctx context.Context) {
	v.Tree.Walk(func(node *index.ProjectNode) {
		if ctx.Err() != nil {
			return
		}
		// If this node (or any ancestor) is only defined inside #if blocks,
		// all its field references are already in a conditional context.
		if nodeIsEffectivelyConditional(node) {
			return
		}
		for _, frag := range node.Fragments {
			if frag.IsConditional {
				// References inside conditional fragments are the user's responsibility.
				continue
			}
			for _, def := range frag.Definitions {
				f, ok := def.(*parser.Field)
				if !ok || f.Name == "Alias" {
					// IfBlock/ForeachBlock definitions inside a non-conditional fragment
					// create their own conditional sub-fragments; skip them here.
					// Alias names a signal inside the sibling DataSource.
					continue
				}
				v.checkValueForConditionalRef(f.Value, node, frag.File)
			}
		}
	})
}

func (v *Validator) checkValueForConditionalRef(val parser.Value, node *index.ProjectNode, file string) {
	switch t := val.(type) {
	case *parser.ReferenceValue:
		// Resolve without the ActiveNodes filter so we can detect conditional targets
		// even when their condition evaluated to false with current variable values.
		target := v.Tree.ResolveName(node, t.Value, nil)
		if target == nil || !target.IsConditional {
			return
		}
		// The target exists in the active configuration (its condition
		// evaluated true), so the reference is valid now; flagging it
		// produces noise for every conditional DataSource that is enabled.
		if v.nodeActive(target) {
			return
		}
		v.report(node, "conditional_reference", LevelError,
			fmt.Sprintf("'%s' is only defined inside a conditional block and may not exist at runtime", t.Value),
			t.Position, file)
	case *parser.ArrayValue:
		for _, elem := range t.Elements {
			v.checkValueForConditionalRef(elem, node, file)
		}
	case *parser.BinaryExpression:
		v.checkValueForConditionalRef(t.Left, node, file)
		v.checkValueForConditionalRef(t.Right, node, file)
	case *parser.UnaryExpression:
		v.checkValueForConditionalRef(t.Right, node, file)
	}
}

func (v *Validator) checkTemplateUse(inst *parser.TemplateInstantiation, file string) {
	tdef, ok := v.Tree.Templates[inst.Template]
	if !ok {
		v.report(nil, "unknown_template", LevelError,
			fmt.Sprintf("Unknown template: '%s'", inst.Template),
			inst.Position, file)
		return
	}

	// Check arguments
	provided := make(map[string]bool)
	for _, arg := range inst.Arguments {
		provided[arg.Name] = true
		// Find param
		found := false
		for _, p := range tdef.Parameters {
			if p.Name == arg.Name {
				found = true
				break
			}
		}
		if !found {
			v.report(nil, "invalid_template_arg", LevelError,
				fmt.Sprintf("Template '%s' has no parameter named '%s'", inst.Template, arg.Name),
				inst.Position, file)
		}
	}

	// Check missing mandatory params
	for _, p := range tdef.Parameters {
		if p.DefaultValue == nil && !provided[p.Name] {
			v.report(nil, "missing_template_arg", LevelError,
				fmt.Sprintf("Missing mandatory argument '%s' for template '%s'", p.Name, inst.Template),
				inst.Position, file)
		}
	}
}

// setOverrideValue parses a raw `-v` override and stores it. When the target
// variable is declared with a structured type, a struct override is merged
// into the default field-by-field, so `-vCFG='{ board_id = 2 }'` replaces only
// that field.
func (v *Validator) setOverrideValue(name, valStr string, def *parser.VariableDefinition) {
	if def != nil && shouldAutoQuoteWithDef(valStr, def) {
		valStr = "\"" + valStr + "\""
	}
	ovVal := parser.ParseValueOverride(valStr)
	if ovVal == nil {
		return
	}
	if def != nil {
		if merged, ok := v.Tree.MergeStructOverride(def, ovVal); ok {
			// MergeStructOverride already falls back to the type's field
			// defaults when the variable has no initializer.
			v.Overrides[name] = merged
			v.Variables[name] = merged
			return
		}
	}
	v.Overrides[name] = ovVal
	v.Variables[name] = ovVal
}

// builtinTypeNames are scalar type expressions that never denote a `type`
// declaration reference.
var builtinTypeNames = map[string]bool{
	"string": true, "str": true, "int": true, "uint": true, "float": true,
	"bool": true, "number": true, "any": true,
	"int8": true, "int16": true, "int32": true, "int64": true,
	"uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float16": true, "float32": true, "float64": true,
}

// isBareTypeName reports whether typeExpr is a single identifier that could be
// a structured type reference (as opposed to a scalar/list/union expression).
func isBareTypeName(typeExpr string) bool {
	e := strings.TrimSpace(typeExpr)
	if e == "" || strings.ContainsAny(e, "[]|&=~ \"(){}") {
		return false
	}
	if builtinTypeNames[e] {
		return false
	}
	for _, r := range e {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// translateVarTypeToCUE translates a variable/field type expression to CUE,
// resolving names declared with `type` into closed struct constraints. The
// expanding set guards against recursive type references.
func (v *Validator) translateVarTypeToCUE(typeExpr string, expanding map[string]bool) string {
	expr := strings.TrimSpace(typeExpr)
	if t, ok := v.Tree.Types[expr]; ok {
		return v.structTypeToCUE(t, expanding)
	}
	// Anonymous structured types arrive with their canonical `{ ... }` form
	// and are validated through the parsed InlineType, which carries range
	// constraints per field.
	if strings.HasPrefix(expr, "{") && strings.HasSuffix(expr, "}") {
		return translateTypeToCUE(expr)
	}
	// A list of structured types, e.g. [ADCConf_T].
	if strings.HasPrefix(expr, "[") && strings.HasSuffix(expr, "]") {
		inner := strings.TrimSpace(expr[1 : len(expr)-1])
		if _, isType := v.Tree.Types[inner]; isType {
			return "[..." + v.translateVarTypeToCUE(inner, expanding) + "]"
		}
	}
	return translateTypeToCUE(expr)
}

// structTypeToCUE renders a declared type as a closed CUE struct: fields are
// required, unknown fields are rejected.
func (v *Validator) structTypeToCUE(t *parser.TypeDefinition, expanding map[string]bool) string {
	if expanding[t.Name] {
		return "_"
	}
	expanding[t.Name] = true
	defer delete(expanding, t.Name)

	parts := make([]string, 0, len(t.Fields))
	for _, f := range t.Fields {
		if f.InlineType != nil {
			parts = append(parts, fmt.Sprintf("%s: %s", f.Name, v.structTypeToCUE(f.InlineType, expanding)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, v.translateVarTypeToCUE(f.TypeExpr, expanding)))
	}
	return "close({" + strings.Join(parts, ", ") + "})"
}

// isDeclaredType reports whether typeExpr names a `type` declaration.
func (v *Validator) isDeclaredType(typeExpr string) bool {
	_, ok := v.Tree.Types[strings.TrimSpace(typeExpr)]
	return ok
}

func translateTypeToCUE(typeExpr string) string {
	clean := strings.ReplaceAll(typeExpr, " ", "")
	if clean == "" {
		return "any"
	}

	// Regex-constrained types: `string =~ "^re"` compiles as
	// `string & =~"^re"`.
	if idx := strings.Index(clean, "=~"); idx != -1 {
		base := strings.TrimSuffix(strings.TrimSpace(clean[:idx]), "&")
		base = strings.TrimSpace(base)
		pattern := clean[idx+2:]
		if base == "" {
			base = "string"
		}
		return base + " & =~" + pattern
	}

	// Anything with CUE-structural syntax is passed through verbatim.
	if strings.ContainsAny(clean, "{}()~") {
		return typeExpr
	}

	// Translate a single base type to CUE.
	translateBase := func(t string) string {
		if strings.HasPrefix(t, "&") {
			// References to nodes/classes are spelled as strings.
			return "string"
		}
		switch t {
		case "str":
			return "string"
		case "int8":
			return "int & >=-128 & <=127"
		case "int16":
			return "int & >=-32768 & <=32767"
		case "int32":
			return "int & >=-2147483648 & <=2147483647"
		case "int64":
			return "int"
		case "uint8":
			return "uint & >=0 & <=255"
		case "uint16":
			return "uint & >=0 & <=65535"
		case "uint32":
			return "uint & >=0 & <=4294967295"
		case "uint64":
			return "uint"
		case "float16", "float32", "float64":
			return "float"
		}
		switch t {
		case "string", "str", "int", "uint", "float", "bool", "number":
			return t
		case "any":
			// CUE's top type; spelled "_" because the compiled snippet has
			// no prelude.
			return "_"
		}
		// Any other named type denotes a reference to a node or class;
		// references are spelled as strings in configurations.
		return "string"
	}

	// Array types, e.g. [int] or [&GAM]: open list.
	if strings.HasPrefix(clean, "[") && strings.HasSuffix(clean, "]") {
		inner := clean[1 : len(clean)-1]
		return "[..." + translateBase(inner) + "]"
	}

	// Union types, e.g. int|uint or &GAM|&DataSource.
	if strings.Contains(clean, "|") {
		parts := strings.Split(clean, "|")
		for i, p := range parts {
			parts[i] = translateBase(p)
		}
		return strings.Join(parts, " | ")
	}

	return translateBase(clean)
}
