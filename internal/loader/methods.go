package loader

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// Builtin methods for configuration values, written `receiver.method(args)`:
//
//	strings: replace(old, new), upper(), lower(), trim(), split(sep),
//	         contains(sub), startsWith(p), endsWith(s), substring(b, e),
//	         length(), isEmpty()
//	lists:   append(v…), contains(v), first(), last(), reverse(), sort(),
//	         slice(b, e), join(sep), length(), isEmpty()
//	dicts:   keys(), values(), contains(k), length(), isEmpty()
//
// All methods are pure: they return new values. An unknown method, a wrong
// argument count or an incompatible receiver returns nil, which renders as an
// empty value (and is reported by validation when it matters).

// MethodCall evaluates a builtin method invocation on an evaluated receiver.
// It lives in this package so every evaluator (index, validator, builder)
// shares one behaviour.
func MethodCall(recv parser.Value, method string, args []parser.Value) parser.Value {
	switch r := recv.(type) {
	case *parser.StringValue:
		return stringMethod(r.Value, method, args)
	case *parser.ArrayValue:
		return arrayMethod(r, method, args)
	case *parser.MapValue:
		return mapMethod(r, method, args)
	}
	return nil
}

func stringMethod(s, method string, args []parser.Value) parser.Value {
	arg := func(i int) string {
		if i < len(args) {
			return renderScalar(args[i])
		}
		return ""
	}
	switch method {
	case "replace":
		if len(args) >= 2 {
			return strValue(strings.ReplaceAll(s, arg(0), arg(1)))
		}
	case "upper":
		return strValue(strings.ToUpper(s))
	case "lower":
		return strValue(strings.ToLower(s))
	case "trim":
		return strValue(strings.TrimSpace(s))
	case "split":
		arr := &parser.ArrayValue{}
		for _, p := range strings.Split(s, arg(0)) {
			arr.Elements = append(arr.Elements, strValue(p))
		}
		return arr
	case "contains":
		return boolValue(strings.Contains(s, arg(0)))
	case "startsWith":
		return boolValue(strings.HasPrefix(s, arg(0)))
	case "endsWith":
		return boolValue(strings.HasSuffix(s, arg(0)))
	case "substring":
		runes := []rune(s)
		start := intArg(args, 0, 0)
		end := len(runes)
		if len(args) > 1 {
			end = intArg(args, 1, len(runes))
		}
		start, end = clampRange(start, end, len(runes))
		return strValue(string(runes[start:end]))
	case "length":
		return intValue(len([]rune(s)))
	case "isEmpty":
		return boolValue(s == "")
	}
	return nil
}

func arrayMethod(arr *parser.ArrayValue, method string, args []parser.Value) parser.Value {
	switch method {
	case "append":
		out := &parser.ArrayValue{Position: arr.Position}
		out.Elements = append(out.Elements, arr.Elements...)
		out.Elements = append(out.Elements, args...)
		return out
	case "contains":
		want := renderScalar(argsFirst(args))
		for _, e := range arr.Elements {
			if renderScalar(e) == want {
				return boolValue(true)
			}
		}
		return boolValue(false)
	case "first":
		if len(arr.Elements) > 0 {
			return arr.Elements[0]
		}
		return nil
	case "last":
		if len(arr.Elements) > 0 {
			return arr.Elements[len(arr.Elements)-1]
		}
		return nil
	case "reverse":
		out := &parser.ArrayValue{Position: arr.Position}
		for i := len(arr.Elements) - 1; i >= 0; i-- {
			out.Elements = append(out.Elements, arr.Elements[i])
		}
		return out
	case "sort":
		out := &parser.ArrayValue{Position: arr.Position}
		out.Elements = append(out.Elements, arr.Elements...)
		sort.SliceStable(out.Elements, func(i, j int) bool {
			return valueLess(out.Elements[i], out.Elements[j])
		})
		return out
	case "slice":
		n := len(arr.Elements)
		start := intArg(args, 0, 0)
		end := n
		if len(args) > 1 {
			end = intArg(args, 1, n)
		}
		start, end = clampRange(start, end, n)
		out := &parser.ArrayValue{Position: arr.Position}
		out.Elements = append(out.Elements, arr.Elements[start:end]...)
		return out
	case "join":
		sep := ""
		if len(args) > 0 {
			sep = renderScalar(args[0])
		}
		parts := make([]string, 0, len(arr.Elements))
		for _, e := range arr.Elements {
			parts = append(parts, renderScalar(e))
		}
		return strValue(strings.Join(parts, sep))
	case "length":
		return intValue(len(arr.Elements))
	case "isEmpty":
		return boolValue(len(arr.Elements) == 0)
	}
	return nil
}

func mapMethod(m *parser.MapValue, method string, args []parser.Value) parser.Value {
	switch method {
	case "keys":
		out := &parser.ArrayValue{Position: m.Position}
		for _, k := range m.Keys {
			out.Elements = append(out.Elements, strValue(k))
		}
		return out
	case "values":
		out := &parser.ArrayValue{Position: m.Position}
		for _, k := range m.Keys {
			out.Elements = append(out.Elements, m.Values[k])
		}
		return out
	case "contains":
		key := renderScalar(argsFirst(args))
		_, found := m.Lookup(key)
		return boolValue(found)
	case "length":
		return intValue(len(m.Keys))
	case "isEmpty":
		return boolValue(len(m.Keys) == 0)
	}
	return nil
}

// --- small value helpers ---

func strValue(s string) parser.Value {
	return &parser.StringValue{Value: s, Quoted: true}
}

func boolValue(b bool) parser.Value {
	return &parser.BoolValue{Value: b}
}

func intValue(n int) parser.Value {
	return &parser.IntValue{Value: int64(n), Raw: fmt.Sprintf("%d", n)}
}

// renderScalar renders a scalar value the way it appears in a configuration
// (numbers use their raw spelling, strings are unquoted).
func renderScalar(v parser.Value) string {
	switch t := v.(type) {
	case nil:
		return ""
	case *parser.StringValue:
		return t.Value
	case *parser.IntValue:
		return t.Raw
	case *parser.FloatValue:
		return t.Raw
	case *parser.BoolValue:
		if t.Value {
			return "true"
		}
		return "false"
	}
	return fmt.Sprintf("%v", v)
}

// valueLess orders scalars: numbers compare numerically (ints before floats
// of the same magnitude is irrelevant — both convert to float), everything
// else compares by rendered text.
func valueLess(a, b parser.Value) bool {
	an, aok := numericOf(a)
	bn, bok := numericOf(b)
	if aok && bok {
		return an < bn
	}
	return renderScalar(a) < renderScalar(b)
}

func numericOf(v parser.Value) (float64, bool) {
	switch t := v.(type) {
	case *parser.IntValue:
		return float64(t.Value), true
	case *parser.FloatValue:
		return t.Value, true
	}
	return 0, false
}

func argsFirst(args []parser.Value) parser.Value {
	if len(args) > 0 {
		return args[0]
	}
	return nil
}

// intArg returns args[i] as an int, or def when absent/non-numeric.
func intArg(args []parser.Value, i, def int) int {
	if i >= len(args) {
		return def
	}
	switch t := args[i].(type) {
	case *parser.IntValue:
		return int(t.Value)
	case *parser.FloatValue:
		return int(t.Value)
	}
	return def
}

// clampRange resolves start/end into a safe [start, end] window over n items.
func clampRange(start, end, n int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if end < start {
		end = start
	}
	return start, end
}
