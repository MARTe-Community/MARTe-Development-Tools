package loader

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// loadXLSX reads the first worksheet of an .xlsx (Office Open XML) workbook
// and returns the rows like loadCSV does: the first row names the columns and
// every following row becomes a dict of strings.
//
// The reader is implemented with the standard library only: it opens the
// workbook zip, resolves the first sheet through the workbook relationships,
// expands shared strings and maps cell references to columns so gaps in a row
// do not shift the values.
func loadXLSX(path string) (parser.Value, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		if strings.Contains(err.Error(), "zip") {
			return nil, fmt.Errorf("%s: not an xlsx (Office Open XML) workbook; legacy binary .xls files are not supported, save the sheet as .xlsx", path)
		}
		return nil, err
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[filepath.ToSlash(filepath.Clean(f.Name))] = f
	}
	read := func(name string) ([]byte, bool) {
		f, ok := files[filepath.ToSlash(filepath.Clean(name))]
		if !ok {
			return nil, false
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, false
		}
		return data, true
	}

	shared := []string{}
	if data, ok := read("xl/sharedStrings.xml"); ok {
		shared = parseSharedStrings(data)
	}

	sheetName, ok := firstSheetPath(read)
	if !ok {
		sheetName = "xl/worksheets/sheet1.xml"
	}
	sheet, ok := read(sheetName)
	if !ok {
		return nil, fmt.Errorf("%s: workbook has no readable worksheet (%s)", path, sheetName)
	}

	rows, err := parseSheetRows(sheet, shared)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid xlsx: %v", path, err)
	}
	if len(rows) == 0 {
		return &parser.ArrayValue{}, nil
	}

	header := rows[0]
	arr := &parser.ArrayValue{}
	for _, row := range rows[1:] {
		empty := true
		values := make(map[string]parser.Value, len(header))
		for i, cell := range row {
			if i < len(header) && cell != "" {
				values[header[i]] = &parser.StringValue{Value: cell, Quoted: true}
				empty = false
			}
		}
		if empty {
			continue
		}
		arr.Elements = append(arr.Elements, parser.NewMapValue(parser.Position{}, values))
	}
	return arr, nil
}

// firstSheetPath resolves the first worksheet's part name through
// xl/workbook.xml and its relationships.
func firstSheetPath(read func(string) ([]byte, bool)) (string, bool) {
	wb, ok := read("xl/workbook.xml")
	if !ok {
		return "", false
	}
	relID := ""
	dec := xml.NewDecoder(bytes.NewReader(wb))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		if se, isStart := tok.(xml.StartElement); isStart && se.Name.Local == "sheet" {
			for _, a := range se.Attr {
				if a.Name.Local == "id" {
					relID = a.Value
				}
			}
			break
		}
	}
	if relID == "" {
		return "", false
	}

	rels, ok := read("xl/_rels/workbook.xml.rels")
	if !ok {
		return "", false
	}
	dec = xml.NewDecoder(bytes.NewReader(rels))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		if se, isStart := tok.(xml.StartElement); isStart && se.Name.Local == "Relationship" {
			id, target := "", ""
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "Id":
					id = a.Value
				case "Target":
					target = a.Value
				}
			}
			if id == relID && target != "" {
				target = strings.TrimPrefix(target, "/")
				if !strings.HasPrefix(target, "xl/") {
					target = path.Join("xl", target)
				}
				if _, readable := read(target); readable {
					return target, true
				}
				return "", false
			}
		}
	}
}

// parseSharedStrings extracts the concatenated text of every <si> entry.
func parseSharedStrings(data []byte) []string {
	var out []string
	if len(data) == 0 {
		return out
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	inSI, inT := false, false
	var cur strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI, inT = true, false
				cur.Reset()
			case "t":
				if inSI {
					inT = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				if inSI {
					out = append(out, cur.String())
					inSI = false
				}
			case "t":
				inT = false
			}
		case xml.CharData:
			if inSI && inT {
				cur.Write(t)
			}
		}
	}
	return out
}

// parseSheetRows reads every row of the worksheet. Cell references
// (e.g. "B3") place values at the right column index, so skipped cells do not
// shift the row.
func parseSheetRows(data []byte, sharedStrings []string) ([][]string, error) {
	var rows [][]string
	var curRow []string
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return rows, nil
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				curRow = nil
			case "c":
				ref, cellType := "", ""
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						ref = a.Value
					case "t":
						cellType = a.Value
					}
				}
				cellVal, inValue := "", false
				for {
					tok, err := dec.Token()
					if err != nil {
						return nil, err
					}
					if te, isEnd := tok.(xml.EndElement); isEnd && te.Name.Local == "c" {
						break
					}
					switch inner := tok.(type) {
					case xml.StartElement:
						if inner.Name.Local == "v" || inner.Name.Local == "t" {
							inValue = true
						}
					case xml.CharData:
						if inValue {
							cellVal += string(inner)
						}
					}
				}
				col := columnIndex(ref)
				for len(curRow) <= col {
					curRow = append(curRow, "")
				}
				curRow[col] = cellValue(cellVal, cellType, sharedStrings)
			}
		case xml.EndElement:
			if t.Name.Local == "row" {
				rows = append(rows, curRow)
			}
		}
	}
}

// cellValue converts a raw cell value to its text form.
func cellValue(raw, cellType string, sharedStrings []string) string {
	switch cellType {
	case "s": // shared string: the value is an index
		idx, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && idx >= 0 && idx < len(sharedStrings) {
			return sharedStrings[idx]
		}
		return ""
	case "b": // boolean
		if strings.TrimSpace(raw) == "1" {
			return "TRUE"
		}
		return "FALSE"
	default: // "n" numbers, "str" formula results, "inlineStr" text, empty
		return strings.TrimSpace(raw)
	}
}

// columnIndex converts a spreadsheet column reference ("A", "B", …, "AA") to
// a zero-based index.
func columnIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A') + 1
	}
	if n > 0 {
		return n - 1
	}
	return 0
}
