package loader

import (
	"sort"
	"unicode/utf8"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// Builtin iterator functions, written `name(args)`:
//
//	enumerate(x)        list of [index, element] pairs (maps: [key, value])
//	zip(a, b, …)        list of [a[i], b[i], …] rows, up to the shortest input
//	range(n)            list 0 … n-1; range(start, stop) and
//	range(start, stop, step) follow the same half-open convention
//	keys(map)           list of the map's keys, sorted
//	values(map)         list of the map's values in key order
//	len(x)              element count of a list/map, rune count of a string
//	reversed(x)         list with the elements in reverse order
//	concat(a, b, …)     the arguments' elements spliced into one list
//
// Functions are pure and return new values. An unknown function or an
// incompatible argument returns nil, which callers keep as a symbolic call
// node (the same contract as MethodCall).
//
// Together with two-variable foreach destructuring (a row that is itself a
// two-element list binds its two elements), these make zipped/enumerated
// iteration possible:
//
//	foreach name, channel in @zip(@names, @channels)
//	  …
//	end
func FunctionCall(name string, args []parser.Value) parser.Value {
	switch name {
	case "enumerate":
		if len(args) != 1 {
			return nil
		}
		return enumerate(args[0])
	case "zip":
		return zipLists(args)
	case "range":
		return rangeList(args)
	case "keys":
		if len(args) != 1 {
			return nil
		}
		m, ok := args[0].(*parser.MapValue)
		if !ok {
			return nil
		}
		keys := append([]string(nil), m.Keys...)
		sort.Strings(keys)
		out := &parser.ArrayValue{}
		for _, k := range keys {
			out.Elements = append(out.Elements, strValue(k))
		}
		return out
	case "values":
		if len(args) != 1 {
			return nil
		}
		m, ok := args[0].(*parser.MapValue)
		if !ok {
			return nil
		}
		keys := append([]string(nil), m.Keys...)
		sort.Strings(keys)
		out := &parser.ArrayValue{}
		for _, k := range keys {
			out.Elements = append(out.Elements, m.Values[k])
		}
		return out
	case "len":
		if len(args) != 1 {
			return nil
		}
		switch t := args[0].(type) {
		case *parser.ArrayValue:
			return intValue(len(t.Elements))
		case *parser.MapValue:
			return intValue(len(t.Keys))
		case *parser.StringValue:
			return intValue(utf8.RuneCountInString(t.Value))
		}
		return nil
	case "reversed":
		if len(args) != 1 {
			return nil
		}
		switch t := args[0].(type) {
		case *parser.ArrayValue:
			out := &parser.ArrayValue{Elements: make([]parser.Value, len(t.Elements))}
			for i, e := range t.Elements {
				out.Elements[len(t.Elements)-1-i] = e
			}
			return out
		case *parser.StringValue:
			runes := []rune(t.Value)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return strValue(string(runes))
		}
		return nil
	case "concat":
		out := &parser.ArrayValue{}
		for _, a := range args {
			arr, ok := a.(*parser.ArrayValue)
			if !ok {
				return nil
			}
			out.Elements = append(out.Elements, arr.Elements...)
		}
		return out
	}
	return nil
}

// enumerate turns a list into [index, element] rows, and a dict into
// [key, value] rows.
func enumerate(v parser.Value) parser.Value {
	out := &parser.ArrayValue{}
	switch t := v.(type) {
	case *parser.ArrayValue:
		for i, e := range t.Elements {
			out.Elements = append(out.Elements, pair(intValue(i), e))
		}
		return out
	case *parser.MapValue:
		keys := append([]string(nil), t.Keys...)
		sort.Strings(keys)
		for _, k := range keys {
			out.Elements = append(out.Elements, pair(strValue(k), t.Values[k]))
		}
		return out
	}
	return nil
}

// zipLists combines lists positionally, stopping at the shortest one.
func zipLists(args []parser.Value) parser.Value {
	if len(args) < 2 {
		return nil
	}
	lists := make([]*parser.ArrayValue, len(args))
	n := -1
	for i, a := range args {
		arr, ok := a.(*parser.ArrayValue)
		if !ok {
			return nil
		}
		lists[i] = arr
		if n < 0 || len(arr.Elements) < n {
			n = len(arr.Elements)
		}
	}
	out := &parser.ArrayValue{}
	for i := 0; i < n; i++ {
		row := &parser.ArrayValue{}
		for _, l := range lists {
			row.Elements = append(row.Elements, l.Elements[i])
		}
		out.Elements = append(out.Elements, row)
	}
	return out
}

// rangeList builds the half-open integer sequence range(start, stop[, step]).
// A non-integer argument or a zero step yields nil.
func rangeList(args []parser.Value) parser.Value {
	if len(args) < 1 || len(args) > 3 {
		return nil
	}
	nums := make([]int, len(args))
	for i, a := range args {
		iv, ok := a.(*parser.IntValue)
		if !ok {
			return nil
		}
		nums[i] = int(iv.Value)
	}
	start, stop, step := 0, 0, 1
	switch len(nums) {
	case 1:
		stop = nums[0]
	case 2:
		start, stop = nums[0], nums[1]
	case 3:
		start, stop, step = nums[0], nums[1], nums[2]
	}
	if step == 0 {
		return nil
	}

	out := &parser.ArrayValue{}
	if step > 0 {
		for i := start; i < stop; i += step {
			out.Elements = append(out.Elements, intValue(i))
		}
	} else {
		for i := start; i > stop; i += step {
			out.Elements = append(out.Elements, intValue(i))
		}
	}
	return out
}

func pair(a, b parser.Value) *parser.ArrayValue {
	return &parser.ArrayValue{Elements: []parser.Value{a, b}}
}
