package builder

import (
	"context"
	"fmt"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/loader"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
	"github.com/marte-community/marte-dev-tools/internal/validator"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Builder struct {
	Files       []string
	Overrides   map[string]string
	ProjectRoot string // Directory containing .marte_schema.cue (derived from Files)
	// Optimize enables MARTe-oriented output optimisations (IOGAM merging,
	// signal ordering). See optimize.go.
	Optimize       bool
	OptimizeReport OptimizeReport
	// LowerReport summarizes copy/wiring sugar expansion.
	LowerReport LowerReport
	variables   map[string]parser.Value
	tree        *index.ProjectTree
	// dynamicGroups maps a parsed copy group with dynamic endpoints to the
	// concrete group it materializes into (one per enclosing scope).
	dynamicGroups   map[*parser.CopyGroup]*parser.CopyGroup
	activeNodes     map[*index.ProjectNode]bool
	activeFragments map[*index.Fragment]bool
	// copySeen dedupes dynamic copy-group expansions visited from several
	// evaluation paths (key: group, scope, item index).
	copySeen map[string]bool
	// expandedScopes is the per-pass (node, scope) visit guard, matching the
	// validator's: contexts are recreated per visit, so pointer-keyed guards
	// re-expand the same subtree indefinitely.
	expandedScopes map[*index.ProjectNode]map[string]bool
}

func NewBuilder(files []string, overrides map[string]string) *Builder {
	root := "."
	if len(files) > 0 {
		if d := filepath.Dir(files[0]); d != "" {
			root = d
		}
	}
	return &Builder{
		Files:           files,
		Overrides:       overrides,
		ProjectRoot:     root,
		variables:       make(map[string]parser.Value),
		activeNodes:     make(map[*index.ProjectNode]bool),
		activeFragments: make(map[*index.Fragment]bool),
	}
}

// pruneMismatchedChildFragments drops fragments whose definition does not
// evaluate to the child's name in that fragment's scope. Cross-scope replay
// (a loop body definition evaluated under a sibling iteration's context)
// would otherwise attach a signal's fields to another signal, producing
// spurious duplicate-field diagnostics.
func (b *Builder) pruneMismatchedChildFragments(node *index.ProjectNode) {
	if node == nil {
		return
	}
	for _, child := range node.Children {
		var kept []*index.Fragment
		dropped := false
		for _, f := range child.Fragments {
			obj, ok := f.Source.(*parser.ObjectNode)
			if !ok || f.EvalCtx == nil || f.EvalCtx.Scope == "" {
				kept = append(kept, f)
				continue
			}
			name := b.tree.ValueToString(b.tree.EvaluateValue(obj.Name, f.EvalCtx))
			if name == "" || strings.Contains(name, "@") || index.NormalizeName(name) == child.Name {
				kept = append(kept, f)
				continue
			}
			delete(b.activeFragments, f)
			dropped = true
		}
		if dropped {
			child.Fragments = kept
		}
		b.pruneMismatchedChildFragments(child)
	}
}

func (b *Builder) collectActiveNodes(node *index.ProjectNode, evalCtx *index.EvaluationContext, forceUncond bool) {
	scopeKey := ""
	if evalCtx != nil {
		scopeKey = evalCtx.Scope
	}
	if b.expandedScopes == nil {
		b.expandedScopes = make(map[*index.ProjectNode]map[string]bool)
	}
	scopes := b.expandedScopes[node]
	if scopes != nil && scopes[scopeKey] {
		return
	}
	if scopes == nil {
		scopes = make(map[string]bool)
		b.expandedScopes[node] = scopes
	}
	scopes[scopeKey] = true
	b.activeNodes[node] = true
	for _, frag := range node.Fragments {
		if !frag.IsConditional {
			b.activeFragments[frag] = true
		}
	}

	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		if !b.activeFragments[frag] {
			continue
		}
		if frag.IsConditional && frag.EvalCtx == nil {
			// Block body or index-time placeholder for an object inside one:
			// the block expansion materializes scoped copies.
			continue
		}
		c := evalCtx
		if frag.EvalCtx != nil && frag.EvalCtx.Scope != "" {
			// Fragment materialized under a loop iteration, template or
			// with binding: evaluate with those bindings. Base-scope
			// fragments use the caller's context so overrides apply.
			c = frag.EvalCtx
		} else if frag.BranchID == "" && frag.Source != nil && evalCtx != nil && evalCtx.Scope != "" {
			// Unscoped index fragment reached through a scoped expansion:
			// it belongs to the base scope, which is evaluated separately.
			// Replaying it here would attach its definitions to this
			// iteration, duplicating the expansion.
			continue
		}
		evaluated = append(evaluated, b.tree.EvaluateDefinitions(frag.Definitions, c, frag.File)...)
	}

	written := make(map[string]bool)
	// expanded marks (child, evaluation context) pairs already walked. A
	// loop revisits the same child name on every iteration with a fresh
	// context, and each iteration defines its own nested objects, so the
	// name alone is not enough to decide whether the subtree was expanded.
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
			switch d := ed.Def.(type) {
			case *parser.SignalShorthand:
				nodeName := d.SignalName
				if d.AliasName != "" {
					nodeName = d.AliasName
				}
				norm := index.NormalizeName(nodeName)
				child, ok := node.Children[norm]
				if ok && !written[norm] {
					for _, f := range child.Fragments {
						if f.Source == parser.Definition(d) {
							b.activeFragments[f] = true
							if f.EvalCtx == nil {
								f.EvalCtx = ed.Ctx
							}
							break
						}
					}
					b.collectActiveNodes(child, ed.Ctx, forceUncond)
					written[norm] = true
				}
			case *parser.ObjectNode:
				if !nameReferencesResolved(d.Name, ed.Ctx) {
					// The name references a variable that is not bound in
					// this pass (a loop variable or a with binding): the
					// bound expansion creates the real object; skip the raw
					// residual here. Without this, a concatenation whose
					// unresolvable part collapses to "" emitted objects with
					// truncated names and empty fields.
					continue
				}
				objName := b.tree.ValueToString(b.tree.EvaluateValue(d.Name, ed.Ctx))
				if strings.Contains(objName, "@") || objName == "" {
					continue
				}
				norm := index.NormalizeName(objName)

				// Find or create the child node
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
					b.tree.AddToNodeMap(child)
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
					if f.Source == parser.Definition(d) && index.SameEvalScope(f.EvalCtx, ed.Ctx) {
						b.activeFragments[f] = true
						if f.EvalCtx == nil {
							f.EvalCtx = ed.Ctx
						}
						found = true
						break
					}
				}
				if !found {
					b.tree.PopulateObjectFragment(child, ed.File, d, "", nil, nil, !forceUncond)
					// Stamp this expansion's scope on the fragment that
					// carries the definition (not necessarily the last one
					// appended: population may add field fragments too).
					for _, f := range child.Fragments {
						if f.Source == parser.Definition(d) && f.EvalCtx == nil {
							b.activeFragments[f] = true
							f.EvalCtx = ed.Ctx
							break
						}
					}
				}

				if k := (expandKey{norm, ed.Ctx}); !expanded[k] {
					expanded[k] = true
					written[norm] = true
					b.collectActiveNodes(child, ed.Ctx, forceUncond)
				}
			case *parser.IfBlock:
				cond := b.tree.EvaluateValue(d.Condition, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				if b.tree.IsTrue(cond) {
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":then" {
							b.activeFragments[f] = true
						}
					}
					processEval(b.tree.EvaluateDefinitions(d.Then, ed.Ctx, ed.File), node, false)
				} else {
					matched := false
					for i, ei := range d.ElseIf {
						elseifCond := b.tree.EvaluateValue(ei.Condition, ed.Ctx)
						if b.tree.IsTrue(elseifCond) {
							branchID := fmt.Sprintf("%s:elseif%d", id, i)
							for _, f := range node.Fragments {
								if f.IsConditional && f.BranchID == branchID {
									b.activeFragments[f] = true
								}
							}
							processEval(b.tree.EvaluateDefinitions(ei.Body, ed.Ctx, ed.File), node, false)
							matched = true
							break
						}
					}
					if !matched && len(d.Else) > 0 {
						for _, f := range node.Fragments {
							if f.IsConditional && f.BranchID == id+":else" {
								b.activeFragments[f] = true
							}
						}
						processEval(b.tree.EvaluateDefinitions(d.Else, ed.Ctx, ed.File), node, false)
					}
				}
			case *parser.ForeachBlock:
				iterable := b.tree.EvaluateValue(d.Iterable, ed.Ctx)
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				if m, ok := iterable.(*parser.MapValue); ok {
					// Dict iteration: two-variable foreach binds
					// key and value of every member.
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":body" {
							b.activeFragments[f] = true
						}
					}
					for _, key := range m.Keys {
						subCtx := &index.EvaluationContext{
							Variables: make(map[string]parser.Value),
							Parent:    ed.Ctx,
							Tree:      b.tree,
							Scope:     index.ChildScope(ed.Ctx, id+"#"+key),
						}
						if d.KeyVar != "" {
							subCtx.Variables[d.KeyVar] = &parser.StringValue{Value: key, Quoted: true}
						}
						if d.ValueVar != "" {
							subCtx.Variables[d.ValueVar] = m.Values[key]
						}
						processEval(b.tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, forceUncond)
					}
				}
				if arr, ok := iterable.(*parser.ArrayValue); ok {
					for _, f := range node.Fragments {
						if f.IsConditional && f.BranchID == id+":body" {
							b.activeFragments[f] = true
						}
					}
					for i, val := range arr.Elements {
						subCtx := &index.EvaluationContext{
							Variables: make(map[string]parser.Value),
							Parent:    ed.Ctx,
							Tree:      b.tree,
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
						processEval(b.tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, forceUncond)
					}
				}
			case *parser.CopyGroup:
				if !parser.CopyGroupHasDynamicEndpoints(d) {
					continue // static groups are lowered once by the lowerer
				}
				b.materializeCopyGroup(node, d, ed.Ctx, ed.File)
			case *parser.WithBlock:
				if b.tree.HasUnresolvedReference(d.Path, ed.Ctx) {
					// Path not bound in this scope (e.g. a loop body visited
					// outside its iteration): the expansion that binds the
					// variable loads the document.
					continue
				}
				home := loader.ProjectHome(ed.File, b.ProjectRoot)
				resolved := loader.ResolvePathFrom(ed.File, home, b.tree.ValueToString(b.tree.EvaluateValue(d.Path, ed.Ctx)))
				val, err := loader.Load(d.Format, resolved)
				if err != nil {
					// Validation reports the failed load with the sugar
					// position (and honours MissingFilesAreWarnings); writing
					// it to stderr here would surface as an error in editors.
					continue
				}
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				subCtx := &index.EvaluationContext{
					Variables: map[string]parser.Value{d.BindName: val},
					Parent:    ed.Ctx,
					Tree:      b.tree,
					Scope:     index.ChildScope(ed.Ctx, id+":with"),
				}
				for _, f := range node.Fragments {
					if f.IsConditional && f.BranchID == id+":with" {
						b.activeFragments[f] = true
						// Later activation passes re-evaluate the body with
						// this context; without it the binding is lost and
						// nested foreach blocks iterate nothing.
						if f.EvalCtx == nil {
							f.EvalCtx = subCtx
						}
					}
				}
				// The with block always loads its document: the body's
				// definitions are unconditional.
				processEval(b.tree.EvaluateDefinitions(d.Body, subCtx, ed.File), node, true)
			case *parser.TemplateDefinition:
				id := fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column)
				for _, f := range node.Fragments {
					if f.IsConditional && f.BranchID == id+":template" {
						b.activeFragments[f] = true
					}
				}
				processEval(b.tree.EvaluateDefinitions(d.Body, ed.Ctx, ed.File), node, forceUncond)
			}
		}
	}

	processEval(evaluated, node, forceUncond)

	// Scoped expansions only walk the children they define (handled above);
	// visiting unrelated siblings here would stamp every iteration's scope
	// onto them. The base-scope visit covers the rest of the tree.
	if evalCtx == nil || evalCtx.Scope == "" {
		for name, child := range node.Children {
			if !written[name] && !child.IsConditional {
				b.collectActiveNodes(child, evalCtx, forceUncond)
			}
		}
	}
}

// activate collects active fragments in up to five passes, refreshing the
// variable values each time so conditions defined by newly activated nodes
// take effect. Shared by Build and Lower (the `mdt check` path needs the
// same scope expansion so dynamic copy endpoints can be materialized).

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
	if changed {
		node.Fragments = kept
	}
	// Children may carry base-scope fragments for definitions that were
	// materialized under a scope elsewhere (a loop body evaluated once at
	// base scope, e.g. with an unresolved name). Drop those fragments and
	// any child left empty by the removal.
	childScoped := make(map[parser.Definition]bool)
	for _, c := range node.Children {
		for _, f := range c.Fragments {
			if f.Source != nil && f.EvalCtx != nil && f.EvalCtx.Scope != "" {
				childScoped[f.Source] = true
			}
		}
	}
	if len(childScoped) > 0 {
		for name, c := range node.Children {
			var kept []*index.Fragment
			dropped := false
			for _, f := range c.Fragments {
				if f.Source != nil && childScoped[f.Source] && (f.EvalCtx == nil || f.EvalCtx.Scope == "") {
					deactivate(f)
					dropped = true
					continue
				}
				kept = append(kept, f)
			}
			if !dropped {
				continue
			}
			c.Fragments = kept
			if len(c.Fragments) == 0 {
				delete(node.Children, name)
			}
		}
	}

	for _, c := range node.Children {
		pruneShadowedFragments(c, isActive, deactivate)
	}
}

func (b *Builder) activate() {
	tree := b.tree
	if tree == nil {
		return
	}
	b.collectVariables(tree)
	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)

	for pass := 0; pass < 5; pass++ {
		// Each pass replays every scope; keep only the latest expansion.
		for _, concrete := range b.dynamicGroups {
			concrete.Items = nil
		}
		b.copySeen = nil
		evalCtx := &index.EvaluationContext{Variables: make(map[string]parser.Value), Tree: b.tree}
		for k, v := range b.variables {
			evalCtx.Variables[k] = v
		}

		// Refresh variables from tree (might have new ones from newly activated fragments)
		tree.Walk(func(n *index.ProjectNode) {
			for k, varInfo := range n.Variables {
				if _, ok := b.variables[k]; !ok || varInfo.Def.IsConst {
					b.variables[k] = varInfo.Def.DefaultValue
				}
			}
		})
		// Re-apply overrides
		b.collectVariables(tree) // This re-parses overrides

		prevCount := len(b.activeFragments)
		b.expandedScopes = make(map[*index.ProjectNode]map[string]bool)
		b.collectActiveNodes(tree.Root, evalCtx, false)
		for _, node := range tree.IsolatedFiles {
			b.collectActiveNodes(node, evalCtx, false)
		}

		// Re-resolve only active things after activation pass
		tree.ResolveFields(b.activeFragments)
		tree.ResolveReferences(b.activeFragments)

		if len(b.activeFragments) == prevCount {
			break
		}
	}

	if b.tree.Root != nil {
		b.pruneMismatchedChildFragments(b.tree.Root)
	}
	if b.tree.Root != nil {
		pruneShadowedFragments(b.tree.Root,
			func(f *index.Fragment) bool { return b.activeFragments[f] },
			func(f *index.Fragment) { delete(b.activeFragments, f) })
	}
	for _, root := range b.tree.IsolatedFiles {
		pruneShadowedFragments(root,
			func(f *index.Fragment) bool { return b.activeFragments[f] },
			func(f *index.Fragment) { delete(b.activeFragments, f) })
	}

	for group, concrete := range b.dynamicGroups {
		if len(concrete.Items) == 0 {
			fmt.Fprintf(os.Stderr, "Warning: dynamic copy group %q at %d:%d produced no copies (unresolvable endpoint name?)\n",
				concrete.Name, group.Position.Line, group.Position.Column)
		}
	}
}

// materializeCopyGroup records one expansion of a copy group with dynamic
// endpoints. All expansions of the same group fold into one concrete group,
// so the lowered IOGAM carries every copied signal.
func (b *Builder) materializeCopyGroup(node *index.ProjectNode, group *parser.CopyGroup, ctx *index.EvaluationContext, file string) bool {
	if b.dynamicGroups == nil {
		b.dynamicGroups = make(map[*parser.CopyGroup]*parser.CopyGroup)
	}
	concrete := b.dynamicGroups[group]
	if concrete == nil {
		concrete = &parser.CopyGroup{
			Position:    group.Position,
			EndPosition: group.EndPosition,
			Name:        group.Name,
		}
		b.dynamicGroups[group] = concrete
		// The lowered GAM must live in the Functions container that held
		// the loop, in a definition fragment of its own (marked active so
		// the lowerer picks it up). The original group stays in place and
		// is skipped by lowering.
		frag := &index.Fragment{
			File:           file,
			Definitions:    []parser.Definition{concrete},
			DefinitionDocs: map[parser.Definition]string{},
			IsConditional:  false,
		}
		node.Fragments = append(node.Fragments, frag)
		b.activeFragments[frag] = true
		if b.activeNodes == nil {
			b.activeNodes = make(map[*index.ProjectNode]bool)
		}
		b.activeNodes[node] = true
	}

	if b.copySeen == nil {
		b.copySeen = make(map[string]bool)
	}
	scope := ""
	if ctx != nil {
		scope = ctx.Scope
	}
	for i, item := range group.Items {
		resolved, ok := b.resolveDynamicItem(item, ctx)
		if !ok {
			return false
		}
		// The same expansion is visited from several paths (node-level
		// evaluation and block expansion); record each (scope, item) once.
		key := fmt.Sprintf("%p|%s|%d", group, scope, i)
		if b.copySeen[key] {
			continue
		}
		b.copySeen[key] = true
		concrete.Items = append(concrete.Items, resolved)
	}
	return true
}

// resolveDynamicItem evaluates the expressions of a copy item: dynamic
// endpoint names and every per-signal option value (so options can use loop
// variables and with bindings).
func (b *Builder) resolveDynamicItem(item parser.CopyItem, ctx *index.EvaluationContext) (parser.CopyItem, bool) {
	resolveEP := func(ep parser.CopyEndpoint) (parser.CopyEndpoint, bool) {
		if !ep.Dynamic() {
			return ep, true
		}
		name := b.tree.ValueToString(b.tree.EvaluateValue(ep.SignalExpr, ctx))
		if name == "" {
			// Early passes may run before the loop variable is bound; the
			// final pass decides. activate() warns if nothing materializes.
			return ep, false
		}
		ep.Signal = name
		ep.SignalExpr = nil
		return ep, true
	}
	resolveOpts := func(opts []parser.CopyOption) []parser.CopyOption {
		out := make([]parser.CopyOption, len(opts))
		for i, o := range opts {
			out[i] = parser.CopyOption{Position: o.Position, Name: o.Name, Value: b.tree.EvaluateValue(o.Value, ctx)}
		}
		return out
	}

	src, ok := resolveEP(item.Src)
	if !ok {
		return item, false
	}
	dst, ok := resolveEP(item.Dst)
	if !ok {
		return item, false
	}
	item.Src, item.Dst = src, dst
	item.SrcOptions = resolveOpts(item.SrcOptions)
	item.DstOptions = resolveOpts(item.DstOptions)
	for i := range item.SrcGroup {
		ep, ok := resolveEP(item.SrcGroup[i].Endpoint)
		if !ok {
			return item, false
		}
		item.SrcGroup[i].Endpoint = ep
		item.SrcGroup[i].Options = resolveOpts(item.SrcGroup[i].Options)
	}
	for i := range item.DstGroup {
		ep, ok := resolveEP(item.DstGroup[i].Endpoint)
		if !ok {
			return item, false
		}
		item.DstGroup[i].Endpoint = ep
		item.DstGroup[i].Options = resolveOpts(item.DstGroup[i].Options)
	}
	return item, true
}

func (b *Builder) Build(f *os.File) error {
	// Build the Project Tree
	tree := index.NewProjectTree()
	b.tree = tree

	var expectedProject string
	var projectSet bool

	for _, file := range b.Files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		p := parser.NewParser(string(content))
		config, err := p.Parse()
		if err != nil {
			return fmt.Errorf("error parsing %s: %v", file, err)
		}

		// Check Namespace/Project Consistency
		proj := ""
		if config.Package != nil {
			parts := strings.Split(config.Package.URI, ".")
			if len(parts) > 0 {
				proj = strings.TrimSpace(parts[0])
			}
		}

		if !projectSet {
			expectedProject = proj
			projectSet = true
		} else if proj != expectedProject {
			return fmt.Errorf("multiple namespaces defined in sources: found '%s' and '%s'", expectedProject, proj)
		}

		tree.AddFile(file, config)
	}

	b.activate()

	// Expand copy/wiring sugar before validation: copies create the signal
	// producers that the ordering checks expect.
	rep, err := b.lowerAll()
	if err != nil {
		return err
	}
	b.LowerReport = rep
	// Generated references (pipeline/copy GAMs) must resolve like parsed
	// ones so usage checks see them.
	b.tree.ResolveReferences(b.activeFragments)

	// Validate before building to ensure ActiveFragments are consistent and fields resolved
	v := &validator.Validator{
		Tree:            tree,
		ActiveFragments: b.activeFragments,
		ActiveNodes:     make(map[*index.ProjectNode]bool),
		Variables:       b.variables,
		Overrides:       make(map[string]parser.Value),
		Schema:          schema.LoadFullSchema(b.ProjectRoot),
	}
	v.ValidateProject(context.Background())
	if len(v.Diagnostics) > 0 {
		hasError := false
		for _, d := range v.Diagnostics {
			if d.Level == validator.LevelError {
				hasError = true
				break
			}
		}
		if hasError {
			// Print errors to stderr but we might want to continue if it's just warnings
			// Actually Build should probably fail on errors.
			// v.ValidateProject already prints to log if using logger?
			// No, it just populates Diagnostics.
		}
	}

	if expectedProject == "" {
		// Sort keys for deterministic order
		var isoPaths []string
		for path := range tree.IsolatedFiles {
			isoPaths = append(isoPaths, path)
		}
		sort.Strings(isoPaths)

		for _, path := range isoPaths {
			iso := tree.IsolatedFiles[path]
			tree.Root.Fragments = append(tree.Root.Fragments, iso.Fragments...)
			for name, child := range iso.Children {
				if existing, ok := tree.Root.Children[name]; ok {
					b.mergeNodes(existing, child)
				} else {
					tree.Root.Children[name] = child
					child.Parent = tree.Root
				}
			}
		}
	}

	rootNode := tree.Root
	if expectedProject != "" {
		if node, ok := tree.Root.Children[expectedProject]; ok {
			rootNode = node
		} else {
			return fmt.Errorf("project '%s' not found in indexed tree", expectedProject)
		}
	}

	if b.Optimize {
		b.OptimizeReport = b.optimize(rootNode)
	}

	b.writeNodeBody(f, rootNode, 0, nil)

	return nil
}

// childHasActiveFragment reports whether any fragment of the node is
// active. Such nodes are real runtime objects even when they were
// indexed as conditional.
func (b *Builder) childHasActiveFragment(node *index.ProjectNode) bool {
	for _, frag := range node.Fragments {
		if b.activeFragments[frag] {
			return true
		}
	}
	return false
}

func (b *Builder) writeNodeContent(f *os.File, node *index.ProjectNode, indent int, ctx *index.EvaluationContext) {
	indentStr := strings.Repeat("  ", indent)

	// If this node has a RealName (e.g. +App), we print it as an object definition
	if node.RealName != "" {
		fmt.Fprintf(f, "%s%s = {\n", indentStr, node.RealName)
		indent++
	}

	b.writeNodeBody(f, node, indent, ctx)

	if node.RealName != "" {
		indent--
		indentStr = strings.Repeat("  ", indent)
		fmt.Fprintf(f, "%s}\n", indentStr)
	}
}

func (b *Builder) mergeNodes(dest, src *index.ProjectNode) {
	dest.Fragments = append(dest.Fragments, src.Fragments...)
	for name, child := range src.Children {
		if existing, ok := dest.Children[name]; ok {
			b.mergeNodes(existing, child)
		} else {
			dest.Children[name] = child
			child.Parent = dest
		}
	}
}

func hasClass(frag *index.Fragment) bool {
	for _, def := range frag.Definitions {
		if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
			return true
		}
	}
	return false
}

func (b *Builder) collectVariables(tree *index.ProjectTree) {
	processNode := func(n *index.ProjectNode) {
		for _, frag := range n.Fragments {
			for _, def := range frag.Definitions {
				if vdef, ok := def.(*parser.VariableDefinition); ok {
					if valStr, ok := b.Overrides[vdef.Name]; ok {
						if !vdef.IsConst {
							// Structured variables (inline type or named type)
							// take struct literals; never auto-quote them.
							if vdef.InlineType == nil && !isDeclaredTypeName(vdef.TypeExpr) && shouldAutoQuoteWithDef(valStr, vdef) {
								valStr = "\"" + valStr + "\""
							}
							ovVal := parser.ParseValueOverride(valStr)
							if ovVal == nil {
								fmt.Fprintf(os.Stderr, "Warning: failed to parse variable override for %s: %q\n", vdef.Name, valStr)
							} else {
								// Structured types merge field-by-field, so
								// `-vCFG='{ board_id = 2 }'` replaces only that
								// field and keeps the rest of the default
								// (which may reference other variables).
								if merged, ok := tree.MergeStructOverride(vdef, ovVal); ok {
									b.variables[vdef.Name] = merged
									continue
								}
								b.variables[vdef.Name] = ovVal
								continue
							}
						}
					}
					if vdef.DefaultValue != nil {
						if _, ok := b.variables[vdef.Name]; !ok || vdef.IsConst {
							b.variables[vdef.Name] = vdef.DefaultValue
						}
					} else if def := tree.EffectiveStructDefault(vdef); def != nil {
						// Structured type with field defaults and no
						// initializer: the variable takes the defaults.
						if _, ok := b.variables[vdef.Name]; !ok {
							b.variables[vdef.Name] = def
						}
					}
				}
			}
		}
	}
	tree.Walk(processNode)
}

func shouldAutoQuoteWithDef(valStr string, def *parser.VariableDefinition) bool {
	if strings.HasPrefix(valStr, "\"") && strings.HasSuffix(valStr, "\"") {
		return false
	}
	typeExpr := def.TypeExpr
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

type EvaluatedDefinition struct {
	Def  parser.Definition
	Ctx  *index.EvaluationContext
	File string
}

func (b *Builder) writeNodeBody(f *os.File, node *index.ProjectNode, indent int, ctx *index.EvaluationContext) {
	if ctx == nil {
		ctx = &index.EvaluationContext{Variables: make(map[string]parser.Value), Tree: b.tree}
		for k, v := range b.variables {
			ctx.Variables[k] = v
		}
	}

	written := make(map[string]bool)
	b.writeEvaluatedBody(f, node, ctx, indent, node, written)

	// Write remaining children (e.g. packages implicit nodes)
	var childNames []string
	for name := range node.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)

	for _, name := range childNames {
		if !written[name] {
			child := node.Children[name]
			if child.IsConditional && !b.childHasActiveFragment(child) {
				continue
			}
			if strings.Contains(child.RealName, "@") {
				// Leftover from a definition whose dynamic name could
				// not be resolved during indexing; the resolved
				// iterations are written through the evaluated path.
				continue
			}
			b.writeNodeContent(f, child, indent, ctx)
		}
	}
}

func (b *Builder) writeEvaluatedBody(f *os.File, node *index.ProjectNode, ctx *index.EvaluationContext, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool) {
	var evaluated []index.EvaluatedDefinition
	for _, frag := range node.Fragments {
		if !b.activeFragments[frag] {
			continue
		}
		if frag.IsConditional && frag.BranchID == "" && frag.EvalCtx == nil {
			// Placeholder for a node materialized inside a block; the scoped
			// copy carries the content.
			continue
		}
		c := ctx
		if frag.EvalCtx != nil && frag.EvalCtx.Scope != "" {
			// Fragment materialized under a loop iteration or template
			// expansion: evaluate with those bindings so field values
			// referencing loop/parameter variables resolve.
			c = frag.EvalCtx
		}
		evaluated = append(evaluated, b.tree.EvaluateDefinitions(frag.Definitions, c, frag.File)...)
	}
	b.writeEvaluatedDefinitions(f, evaluated, indent, parentNode, writtenChildren, ctx)
}

func (b *Builder) writeEvaluatedDefinitions(f *os.File, evaluated []index.EvaluatedDefinition, indent int, parentNode *index.ProjectNode, writtenChildren map[string]bool, defaultCtx *index.EvaluationContext) {
	var fields []EvaluatedDefinition
	var objects []EvaluatedDefinition

	var shorthands []EvaluatedDefinition
	for _, ed := range evaluated {
		switch d := ed.Def.(type) {
		case *parser.Field:
			fields = append(fields, EvaluatedDefinition{Def: d, Ctx: ed.Ctx, File: ed.File})
		case *parser.ObjectNode:
			objects = append(objects, EvaluatedDefinition{Def: d, Ctx: ed.Ctx, File: ed.File})
		case *parser.SignalShorthand:
			_ = d
			shorthands = append(shorthands, EvaluatedDefinition{Def: ed.Def, Ctx: ed.Ctx, File: ed.File})
		}
	}

	sort.SliceStable(fields, func(i, j int) bool {
		return fields[i].Def.(*parser.Field).Name == "Class" && fields[j].Def.(*parser.Field).Name != "Class"
	})

	for _, field := range fields {
		b.writeField(f, field.Def.(*parser.Field), field.Ctx, indent)
	}

	if writtenChildren == nil {
		writtenChildren = make(map[string]bool)
	}

	for _, obj := range objects {
		objectNode := obj.Def.(*parser.ObjectNode)
		objName := b.formatValueWithCtx(objectNode.Name, obj.Ctx)
		if !nameReferencesResolved(objectNode.Name, obj.Ctx) {
			// The name references an unbound loop variable / with binding:
			// the bound pass already materialized the real object.
			continue
		}

		// If name still has variables, skip it. It will be rendered as a resolved child.
		if strings.Contains(objName, "@") {
			continue
		}

		// Attempt to resolve merged node if we have a parent context
		if parentNode != nil {
			cleanedName := objName
			if strings.HasPrefix(cleanedName, "\"") && strings.HasSuffix(cleanedName, "\"") && len(cleanedName) >= 2 {
				cleanedName = cleanedName[1 : len(cleanedName)-1]
			}

			norm := index.NormalizeName(cleanedName)
			if child, ok := parentNode.Children[norm]; ok {
				if !writtenChildren[norm] {
					b.writeNodeContent(f, child, indent, obj.Ctx)
					writtenChildren[norm] = true
				}
				continue
			}
		}

		b.writeEvaluatedObject(f, objectNode, obj.Ctx, indent, obj.File)
	}

	// Emit SignalShorthand entries as standard MARTe signal blocks.
	indentStr := strings.Repeat("  ", indent)
	for _, sh := range shorthands {
		d := sh.Def.(*parser.SignalShorthand)
		nodeName := d.SignalName
		if d.AliasName != "" {
			nodeName = d.AliasName
		}
		norm := index.NormalizeName(nodeName)

		// Use the indexed child node when available (handles merging).
		if parentNode != nil {
			if child, ok := parentNode.Children[norm]; ok {
				if !writtenChildren[norm] {
					b.writeNodeContent(f, child, indent, sh.Ctx)
					writtenChildren[norm] = true
				}
				continue
			}
		}

		// Fallback: write directly from shorthand fields.
		fmt.Fprintf(f, "%s%s = {\n", indentStr, nodeName)
		if d.DataSource != "" {
			fmt.Fprintf(f, "%s  DataSource = %s\n", indentStr, d.DataSource)
		}
		if d.AliasName != "" {
			fmt.Fprintf(f, "%s  Alias = %s\n", indentStr, d.SignalName)
		}
		if d.Type != "" {
			fmt.Fprintf(f, "%s  Type = %s\n", indentStr, d.Type)
		}
		if d.NumElements != nil {
			fmt.Fprintf(f, "%s  NumberOfElements = %s\n", indentStr, b.formatValueWithCtx(d.NumElements, sh.Ctx))
		}
		if d.HasExtraFields {
			for _, def := range d.ExtraFields.Definitions {
				if fld, ok := def.(*parser.Field); ok {
					b.writeField(f, fld, sh.Ctx, indent+1)
				}
			}
		}
		fmt.Fprintf(f, "%s}\n", indentStr)
	}
}

func (b *Builder) writeField(f *os.File, field *parser.Field, ctx *index.EvaluationContext, indent int) {
	indentStr := strings.Repeat("  ", indent)
	fmt.Fprintf(f, "%s%s = %s\n", indentStr, field.Name, b.formatValueWithCtx(field.Value, ctx))
}

func (b *Builder) writeEvaluatedObject(f *os.File, obj *parser.ObjectNode, ctx *index.EvaluationContext, indent int, file string) {
	indentStr := strings.Repeat("  ", indent)
	objName := b.formatValueWithCtx(obj.Name, ctx)
	fmt.Fprintf(f, "%s%s = {\n", indentStr, objName)

	evaluated := b.tree.EvaluateDefinitions(obj.Subnode.Definitions, ctx, file)
	b.writeEvaluatedDefinitions(f, evaluated, indent+1, nil, nil, ctx)

	fmt.Fprintf(f, "%s}\n", indentStr)
}

func (b *Builder) formatValueWithCtx(val parser.Value, ctx *index.EvaluationContext) string {
	val = b.tree.EvaluateValue(val, ctx)
	switch v := val.(type) {
	case *parser.StringValue:
		if v.Quoted {
			return fmt.Sprintf("\"%s\"", parser.EscapeString(v.Value))
		}
		return v.Value
	case *parser.IntValue:
		return v.Raw
	case *parser.FloatValue:
		return v.Raw
	case *parser.BoolValue:
		return fmt.Sprintf("%v", v.Value)
	case *parser.VariableReferenceValue:
		return v.Name
	case *parser.ReferenceValue:
		return v.Value
	case *parser.ArrayValue:
		elements := []string{}
		for _, e := range v.Elements {
			elements = append(elements, b.formatValueWithCtx(e, ctx))
		}
		return fmt.Sprintf("{ %s }", strings.Join(elements, " "))
	case *parser.MemberAccess:
		res := b.tree.EvaluateValue(v, ctx)
		return b.formatValueWithCtx(res, ctx)
	case *parser.MethodCall:
		res := b.tree.EvaluateValue(v, ctx)
		if _, stillCall := res.(*parser.MethodCall); stillCall {
			// Unresolvable: render the original expression.
			parts := make([]string, 0, len(v.Args))
			for _, a := range v.Args {
				parts = append(parts, b.formatValueWithCtx(a, ctx))
			}
			return fmt.Sprintf("%s.%s(%s)", b.formatValueWithCtx(v.Base, ctx), v.Method, strings.Join(parts, ", "))
		}
		return b.formatValueWithCtx(res, ctx)
	case *parser.FunctionCall:
		res := b.tree.EvaluateValue(v, ctx)
		if _, stillCall := res.(*parser.FunctionCall); stillCall {
			// Unknown builtin or incompatible arguments: keep the call.
			parts := make([]string, 0, len(v.Args))
			for _, a := range v.Args {
				parts = append(parts, b.formatValueWithCtx(a, ctx))
			}
			return fmt.Sprintf("%s(%s)", v.Name, strings.Join(parts, ", "))
		}
		return b.formatValueWithCtx(res, ctx)
	case *parser.ListComprehension:
		res := b.tree.EvaluateValue(v, ctx)
		if _, stillComp := res.(*parser.ListComprehension); stillComp {
			// Unresolvable iterable: keep the expression as written.
			vars := v.ValueVar
			if v.KeyVar != "" {
				vars = v.KeyVar + ", " + v.ValueVar
			}
			text := "{ " + b.formatValueWithCtx(v.Expr, ctx) + " for " + vars +
				" in " + b.formatValueWithCtx(v.Iterable, ctx)
			if v.Filter != nil {
				text += " if " + b.formatValueWithCtx(v.Filter, ctx)
			}
			return text + " }"
		}
		return b.formatValueWithCtx(res, ctx)
	case *parser.MapValue:
		parts := []string{}
		for _, k := range v.Keys {
			parts = append(parts, fmt.Sprintf("%s = %s", k, b.formatValueWithCtx(v.Values[k], ctx)))
		}
		return fmt.Sprintf("{ %s }", strings.Join(parts, " "))
	case *parser.ConditionalArrayElements:
		cond := b.tree.EvaluateValue(v.Condition, ctx)
		if b.tree.IsTrue(cond) {
			parts := []string{}
			for _, e := range v.Then {
				parts = append(parts, b.formatValueWithCtx(e, ctx))
			}
			return strings.Join(parts, " ")
		}
		for _, ei := range v.ElseIf {
			elseifCond := b.tree.EvaluateValue(ei.Condition, ctx)
			if b.tree.IsTrue(elseifCond) {
				parts := []string{}
				for _, e := range ei.Body {
					parts = append(parts, b.formatValueWithCtx(e, ctx))
				}
				return strings.Join(parts, " ")
			}
		}
		parts := []string{}
		for _, e := range v.Else {
			parts = append(parts, b.formatValueWithCtx(e, ctx))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

// nameReferencesResolved reports whether every variable that the expression
// references is bound in ctx. Object names inside foreach/with bodies belong
// to their bound expansion; evaluating them in a pass where the loop variable
// or with binding is absent produced objects with truncated names and empty
// fields (the unresolvable part of the concatenation collapses to "").
func nameReferencesResolved(v parser.Value, ctx *index.EvaluationContext) bool {
	switch t := v.(type) {
	case *parser.VariableReferenceValue:
		name := strings.TrimPrefix(strings.TrimPrefix(t.Name, "@"), "$")
		return ctx.Resolve(name) != nil
	case *parser.MemberAccess:
		return nameReferencesResolved(t.Base, ctx)
	case *parser.MethodCall:
		return nameReferencesResolved(t.Base, ctx) && allResolved(t.Args, ctx)
	case *parser.FunctionCall:
		return allResolved(t.Args, ctx)
	case *parser.ListComprehension:
		// The loop variables are bound by the comprehension itself; only the
		// iterable and filter must resolve in the surrounding scope.
		if !nameReferencesResolved(t.Iterable, ctx) {
			return false
		}
		if t.Filter != nil && !nameReferencesResolved(t.Filter, ctx) {
			return false
		}
		return true
	case *parser.BinaryExpression:
		return nameReferencesResolved(t.Left, ctx) && nameReferencesResolved(t.Right, ctx)
	case *parser.UnaryExpression:
		return nameReferencesResolved(t.Right, ctx)
	}
	return true
}

// allResolved reports whether every value in vs is fully bound in ctx.
func allResolved(vs []parser.Value, ctx *index.EvaluationContext) bool {
	for _, v := range vs {
		if !nameReferencesResolved(v, ctx) {
			return false
		}
	}
	return true
}

// isDeclaredTypeName reports whether the type expression names a structured
// type declared with `type Name { ... }` rather than a scalar.
func isDeclaredTypeName(typeExpr string) bool {
	t := strings.TrimSpace(typeExpr)
	if t == "" || strings.ContainsAny(t, "{}[]|") {
		return false
	}
	switch t {
	case "uint8", "int8", "char8", "bool", "uint16", "int16", "uint32", "int32",
		"float32", "uint64", "int64", "float64", "string":
		return false
	}
	return true
}
