package lsp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// Sugar support (copy groups, `from` wiring, pipelines): endpoint detection
// on the current line, resolution against the tree, and hover/definition/
// completion helpers.

var (
	sugarDSRe  = regexp.MustCompile(`\b([A-Za-z_]\w*)::([A-Za-z_]\w*)`)
	sugarGAMRe = regexp.MustCompile(`\b([A-Za-z_]\w*)\.([A-Za-z_]\w*)`)
	classRe    = regexp.MustCompile(`\bClass\s*=\s*([A-Za-z_]\w*)`)
)

// sugarEndpoint is one `DS::Sig` or `GAM.Sig` occurrence on a line.
type sugarEndpoint struct {
	object string
	signal string
	isDS   bool
	// byte offsets within the line
	start, end int
}

// sugarEndpointsAt returns the endpoint containing the 1-based column col.
func sugarEndpointsAt(line string, col int) (sugarEndpoint, bool) {
	byteCol := byteCol(line, col-1)
	for _, m := range sugarDSRe.FindAllStringSubmatchIndex(line, -1) {
		if byteCol >= m[0] && byteCol <= m[1] {
			return sugarEndpoint{
				object: line[m[2]:m[3]],
				signal: line[m[4]:m[5]],
				isDS:   true,
				start:  m[0], end: m[1],
			}, true
		}
	}
	for _, m := range sugarGAMRe.FindAllStringSubmatchIndex(line, -1) {
		if m[0] > 0 && line[m[0]-1] == '@' {
			continue // @var.member: handled by the struct-member features
		}
		if byteCol >= m[0] && byteCol <= m[1] {
			return sugarEndpoint{
				object: line[m[2]:m[3]],
				signal: line[m[4]:m[5]],
				start:  m[0], end: m[1],
			}, true
		}
	}
	return sugarEndpoint{}, false
}

// pipelineClassAt returns the pipeline name when the cursor is on the
// identifier of `Class = <name>` and <name> is a `gam` macro.
func pipelineClassAt(tree *index.ProjectTree, line string, col int) (string, bool) {
	if tree == nil || len(tree.GamDefinitions) == 0 {
		return "", false
	}
	byteCol := byteCol(line, col-1)
	for _, m := range classRe.FindAllStringSubmatchIndex(line, -1) {
		if byteCol < m[2] || byteCol > m[3] {
			continue
		}
		name := line[m[2]:m[3]]
		if _, ok := tree.GamDefinitions[name]; ok {
			return name, true
		}
	}
	return "", false
}

// --- resolution ---------------------------------------------------------

func sugarNode(tree *index.ProjectTree, name string) *index.ProjectNode {
	if tree == nil || tree.NodeMap == nil {
		return nil
	}
	list := tree.NodeMap[index.NormalizeName(name)]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func sugarClass(node *index.ProjectNode) string {
	if node == nil {
		return ""
	}
	for _, frag := range node.Fragments {
		for _, def := range frag.Definitions {
			if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
				return sugarValue(f.Value)
			}
		}
	}
	return ""
}

type sugarSigInfo struct {
	ds    string
	typ   string
	alias string
	dim   int
}

func sugarSignalInfo(sig *index.ProjectNode) sugarSigInfo {
	var info sugarSigInfo
	for _, frag := range sig.Fragments {
		for _, def := range frag.Definitions {
			f, ok := def.(*parser.Field)
			if !ok {
				continue
			}
			switch f.Name {
			case "DataSource":
				info.ds = sugarValue(f.Value)
			case "Alias":
				info.alias = sugarValue(f.Value)
			case "Type":
				info.typ = sugarValue(f.Value)
			case "NumberOfElements":
				fmt.Sscanf(sugarValue(f.Value), "%d", &info.dim)
			}
		}
	}
	return info
}

func sugarValue(v parser.Value) string {
	switch t := v.(type) {
	case *parser.ReferenceValue:
		return t.Value
	case *parser.StringValue:
		return t.Value
	case *parser.IntValue:
		return t.Raw
	case *parser.FloatValue:
		return t.Raw
	case *parser.BoolValue:
		return fmt.Sprintf("%v", t.Value)
	}
	return ""
}

func sugarSignalNode(gam *index.ProjectNode, container, name string) (*index.ProjectNode, string) {
	for _, c := range []string{"InputSignals", "OutputSignals"} {
		list, ok := gam.Children[c]
		if !ok {
			continue
		}
		if sig, ok := list.Children[index.NormalizeName(name)]; ok {
			return sig, c
		}
	}
	return nil, ""
}

// pipelineHover shows a `gam` macro when hovering `Class = <pipeline>`.
func pipelineHover(tree *index.ProjectTree, text string, line, col int) *Hover {
	lines := strings.Split(text, "\n")
	if line-1 < 0 || line-1 >= len(lines) {
		return nil
	}
	lineStr := lines[line-1]

	if name, ok := pipelineClassAt(tree, lineStr, col); ok {
		def := tree.GamDefinitions[name]
		var b strings.Builder
		fmt.Fprintf(&b, "**Pipeline**: `%s`\n\n", name)
		if len(def.Inputs) > 0 {
			ports := make([]string, 0, len(def.Inputs))
			for _, p := range def.Inputs {
				ports = append(ports, portText(p))
			}
			fmt.Fprintf(&b, "Inputs: %s\n\n", strings.Join(ports, ", "))
		}
		if len(def.Outputs) > 0 {
			ports := make([]string, 0, len(def.Outputs))
			for _, p := range def.Outputs {
				ports = append(ports, portText(p))
			}
			fmt.Fprintf(&b, "Outputs: %s\n\n", strings.Join(ports, ", "))
		}
		b.WriteString("Instantiate with `Class = " + name + "` and bind every port.")
		return &Hover{Contents: MarkupContent{Kind: "markdown", Value: b.String()}}
	}
	return nil
}

// sugarHover builds hover content for a copy endpoint.
func sugarHover(tree *index.ProjectTree, file, text string, line, col int) *Hover {
	lines := strings.Split(text, "\n")
	if line-1 < 0 || line-1 >= len(lines) {
		return nil
	}
	lineStr := lines[line-1]

	ep, ok := sugarEndpointsAt(lineStr, col)
	if !ok {
		return nil
	}
	if ep.isDS {
		ds := sugarNode(tree, ep.object)
		if ds == nil {
			return nil
		}
		info := sugarSigInfoOfDS(ds, ep.signal)
		content := fmt.Sprintf("**DataSource**: `%s` (`%s`)", ep.object, sugarClass(ds))
		if info.typ != "" {
			content += fmt.Sprintf("\n\nSignal: `%s` · `%s`", ep.signal, typeText(info.typ, info.dim))
		} else {
			content += fmt.Sprintf("\n\nSignal: `%s`", ep.signal)
		}
		return &Hover{Contents: MarkupContent{Kind: "markdown", Value: content}}
	}

	gam := sugarNode(tree, ep.object)
	if gam == nil || !tree.IsGAM(gam) {
		// Inside a `gam` macro body the GAM is not indexed; resolve it in
		// the pipeline definition's AST.
		obj, def := pipelineBodyGAM(tree, file, line, ep.object)
		if obj != nil {
			_ = def
			info, container, ok := astSignalInfo(obj, ep.signal)
			if ok {
				content := fmt.Sprintf("**GAM** (pipeline body): `%s` (`%s`)\n\n%s signal: `%s`", ep.object, astClass(obj), strings.TrimSuffix(container, "Signals"), ep.signal)
				if info.ds != "" {
					content += fmt.Sprintf(" → `%s`", info.ds)
				}
				if info.typ != "" {
					content += fmt.Sprintf("\n\nType: `%s`", typeText(info.typ, info.dim))
				}
				return &Hover{Contents: MarkupContent{Kind: "markdown", Value: content}}
			}
		}
		return nil
	}
	sig, container := sugarSignalNode(gam, "", ep.signal)
	if sig == nil {
		return nil
	}
	info := sugarSignalInfo(sig)
	content := fmt.Sprintf("**GAM**: `%s` (`%s`)\n\n%s signal: `%s`", ep.object, sugarClass(gam), strings.TrimSuffix(container, "Signals"), ep.signal)
	if info.ds != "" {
		content += fmt.Sprintf(" → `%s`", info.ds)
		if info.alias != "" {
			content += fmt.Sprintf(" (Alias `%s`)", info.alias)
		}
	}
	if info.typ != "" {
		content += fmt.Sprintf("\n\nType: `%s`", typeText(info.typ, info.dim))
	}
	return &Hover{Contents: MarkupContent{Kind: "markdown", Value: content}}
}

// sugarSigInfoOfDS reads the declared type of a DataSource signal.
func sugarSigInfoOfDS(ds *index.ProjectNode, signal string) sugarSigInfo {
	signals, ok := ds.Children["Signals"]
	if !ok {
		return sugarSigInfo{}
	}
	sig, ok := signals.Children[index.NormalizeName(signal)]
	if !ok {
		return sugarSigInfo{}
	}
	return sugarSignalInfo(sig)
}

func typeText(typ string, dim int) string {
	if dim > 0 {
		return fmt.Sprintf("%s[%d]", typ, dim)
	}
	return typ
}

func portText(p parser.GamPort) string {
	if p.Type == "" {
		return "`" + p.Name + "`"
	}
	return fmt.Sprintf("`%s: %s`", p.Name, typeText(p.Type, p.Dim))
}

// pipelineDefinition resolves `Class = <pipeline>` to the gam definition.
func pipelineDefinition(tree *index.ProjectTree, text string, line, col int) []Location {
	lines := strings.Split(text, "\n")
	if line-1 < 0 || line-1 >= len(lines) {
		return nil
	}
	if name, ok := pipelineClassAt(tree, lines[line-1], col); ok {
		def := tree.GamDefinitions[name]
		file := tree.GamFiles[name]
		if def == nil || file == "" {
			return nil
		}
		return []Location{locationAt(file, def.Position)}
	}
	return nil
}

// sugarDefinition resolves a copy endpoint to its declaration.
func sugarDefinition(tree *index.ProjectTree, file, text string, line, col int) []Location {
	lines := strings.Split(text, "\n")
	if line-1 < 0 || line-1 >= len(lines) {
		return nil
	}
	lineStr := lines[line-1]

	ep, ok := sugarEndpointsAt(lineStr, col)
	if !ok {
		return nil
	}
	if ep.isDS {
		ds := sugarNode(tree, ep.object)
		if ds == nil {
			return nil
		}
		signals, ok := ds.Children["Signals"]
		if !ok {
			return nil
		}
		sig, ok := signals.Children[index.NormalizeName(ep.signal)]
		if !ok {
			return nil
		}
		return []Location{nodeLocation(sig)}
	}
	gam := sugarNode(tree, ep.object)
	if gam == nil {
		if obj, _ := pipelineBodyGAM(tree, file, line, ep.object); obj != nil {
			if _, _, ok := astSignalInfo(obj, ep.signal); ok {
				return []Location{locationAt(file, obj.Position)}
			}
		}
		return nil
	}
	sig, _ := sugarSignalNode(gam, "", ep.signal)
	if sig == nil {
		return nil
	}
	return []Location{nodeLocation(sig)}
}

func locationAt(file string, pos parser.Position) Location {
	return Location{
		URI: "file://" + file,
		Range: Range{
			Start: Position{Line: pos.Line - 1, Character: pos.Column - 1},
			End:   Position{Line: pos.Line - 1, Character: pos.Column},
		},
	}
}

func nodeLocation(node *index.ProjectNode) Location {
	file := node.File
	pos := parser.Position{Line: 1, Column: 1}
	if len(node.Fragments) > 0 {
		if node.Fragments[0].File != "" {
			file = node.Fragments[0].File
		}
		pos = node.Fragments[0].ObjectPos
	}
	return locationAt(file, pos)
}

// sugarCompletion suggests source/destination objects inside copy groups.
func sugarCompletion(tree *index.ProjectTree, prefix string) *CompletionList {
	ctx := sugarCompletionContext(prefix)
	if ctx == "" {
		return nil
	}
	seen := map[string]bool{}
	var items []CompletionItem
	add := func(label, detail string, kind int, insert string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		item := CompletionItem{Label: label, Kind: kind, Detail: detail}
		if insert != "" {
			item.InsertText = insert
		}
		items = append(items, item)
	}
	for _, node := range tree.Nodes() {
		name := index.NormalizeName(node.RealName)
		switch {
		case tree.IsGAM(node):
			add(name, sugarClass(node), 5, "")
		case tree.IsDataSource(node):
			add(name, "DataSource · "+sugarClass(node), 9, name+"::")
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &CompletionList{Items: items}
}

// sugarCompletionContext reports whether the prefix asks for a copy endpoint:
// right after `->`, or right after `from` at the start of an item.
func sugarCompletionContext(prefix string) string {
	if i := strings.LastIndex(prefix, "->"); i >= 0 {
		if strings.TrimSpace(prefix[i+2:]) == "" {
			return "destination"
		}
		return ""
	}
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "from" || strings.HasPrefix(trimmed, "from ") {
		return "source"
	}
	return ""
}

// pipelineBodyGAM finds a GAM object inside a `gam` macro whose body covers
// the given line in the file.
func pipelineBodyGAM(tree *index.ProjectTree, file string, line int, name string) (*parser.ObjectNode, *parser.GamDefinition) {
	for gname, def := range tree.GamDefinitions {
		if tree.GamFiles[gname] != file {
			continue
		}
		if line < def.Position.Line || line > def.EndPosition.Line {
			continue
		}
		if obj := findASTObject(def.Body, name); obj != nil {
			return obj, def
		}
	}
	return nil, nil
}

func findASTObject(defs []parser.Definition, name string) *parser.ObjectNode {
	for _, def := range defs {
		obj, ok := def.(*parser.ObjectNode)
		if !ok {
			continue
		}
		if index.NormalizeName(sugarValue(obj.Name)) == index.NormalizeName(name) {
			return obj
		}
		if nested := findASTObject(obj.Subnode.Definitions, name); nested != nil {
			return nested
		}
	}
	return nil
}

func astClass(obj *parser.ObjectNode) string {
	for _, def := range obj.Subnode.Definitions {
		if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
			return sugarValue(f.Value)
		}
	}
	return ""
}

// astSignalInfo reads a signal entry from a pipeline body GAM.
func astSignalInfo(gam *parser.ObjectNode, signal string) (sugarSigInfo, string, bool) {
	for _, containerName := range []string{"InputSignals", "OutputSignals"} {
		for _, def := range gam.Subnode.Definitions {
			cont, ok := def.(*parser.ObjectNode)
			if !ok || sugarValue(cont.Name) != containerName {
				continue
			}
			for _, sd := range cont.Subnode.Definitions {
				entry, ok := sd.(*parser.ObjectNode)
				if !ok || sugarValue(entry.Name) != signal {
					continue
				}
				var info sugarSigInfo
				for _, fd := range entry.Subnode.Definitions {
					f, ok := fd.(*parser.Field)
					if !ok {
						continue
					}
					switch f.Name {
					case "DataSource":
						info.ds = sugarValue(f.Value)
					case "Alias":
						info.alias = sugarValue(f.Value)
					case "Type":
						info.typ = sugarValue(f.Value)
					case "NumberOfElements":
						fmt.Sscanf(sugarValue(f.Value), "%d", &info.dim)
					}
				}
				return info, containerName, true
			}
		}
	}
	return sugarSigInfo{}, "", false
}
