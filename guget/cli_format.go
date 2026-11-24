package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"text/template/parse"

	"golang.org/x/term"
)

// cliFormatKind is the rendering mode selected by --format.
type cliFormatKind int

const (
	formatTable cliFormatKind = iota
	formatTSV
	formatJSON
	formatJSONL
	formatTemplate
	formatTableTemplate
)

// cliFormatHelp describes --format once for help text and errors.
const cliFormatHelp = "table, tsv, json, jsonl, or a Go template such as '{{.package}}'"

// cliFormat is a parsed --format value. Templates are parsed and checked
// against the command's row fields before any workspace or network work runs.
type cliFormat struct {
	kind     cliFormatKind
	template *template.Template
	// fields holds the field chains a template references outside range and
	// with blocks, so table templates can derive their header row.
	fields [][]string
}

func (f cliFormat) String() string {
	switch f.kind {
	case formatTable:
		return "table"
	case formatTSV:
		return "tsv"
	case formatJSON:
		return "json"
	case formatJSONL:
		return "jsonl"
	case formatTableTemplate:
		return "table template"
	default:
		return "template"
	}
}

func (f cliFormat) isTemplate() bool {
	return f.kind == formatTemplate || f.kind == formatTableTemplate
}

// parseCLIFormat accepts the named formats case-insensitively. Anything else
// must be a Go text/template; a "table " prefix renders an aligned table with a
// header row derived from the referenced field names, like docker --format.
func parseCLIFormat(raw string) (cliFormat, error) {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "table":
		return cliFormat{kind: formatTable}, nil
	case "tsv":
		return cliFormat{kind: formatTSV}, nil
	case "json":
		return cliFormat{kind: formatJSON}, nil
	case "jsonl":
		return cliFormat{kind: formatJSONL}, nil
	}
	kind := formatTemplate
	body := raw
	if len(trimmed) > len("table") && strings.EqualFold(trimmed[:len("table")], "table") && (trimmed[5] == ' ' || trimmed[5] == '\t') {
		kind = formatTableTemplate
		body = strings.TrimLeft(trimmed[len("table"):], " \t")
	}
	if !strings.Contains(body, "{{") {
		return cliFormat{}, fmt.Errorf("invalid --format %q (expected %s)", raw, cliFormatHelp)
	}
	tmpl, err := template.New("format").Funcs(cliTemplateFuncs).Option("missingkey=error").Parse(unescapeTemplate(body))
	if err != nil {
		return cliFormat{}, fmt.Errorf("invalid --format template: %v", err)
	}
	return cliFormat{kind: kind, template: tmpl, fields: templateFields(tmpl)}, nil
}

// defaultCLIFormat chooses table only for a terminal: stdout must be a TTY and
// no --output file may be named. Files and pipes default to TSV.
func defaultCLIFormat(stdout io.Writer, output string) cliFormat {
	if (output == "" || output == "-") && isTerminalWriter(stdout) {
		return cliFormat{kind: formatTable}
	}
	return cliFormat{kind: formatTSV}
}

var isTerminalWriter = func(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// Shell users type \t and \n literally inside single quotes, as with docker.
func unescapeTemplate(body string) string {
	return strings.NewReplacer(`\t`, "\t", `\n`, "\n").Replace(body)
}

// validateTemplateFields rejects references to fields that a command's rows do
// not have, so a typo fails before evaluation or network access.
func (f cliFormat) validateTemplateFields(keys []string) error {
	if !f.isTemplate() || keys == nil {
		return nil
	}
	known := make(map[string]bool, len(keys))
	for _, key := range keys {
		known[key] = true
	}
	for _, chain := range f.fields {
		if !known[chain[0]] {
			sorted := append([]string(nil), keys...)
			sort.Strings(sorted)
			return fmt.Errorf("invalid --format template: unknown field %q (fields: %s)", chain[0], strings.Join(sorted, ", "))
		}
	}
	return nil
}

func templateFields(tmpl *template.Template) [][]string {
	var fields [][]string
	var walk func(parse.Node)
	walkPipe := func(pipe *parse.PipeNode) {
		if pipe == nil {
			return
		}
		for _, command := range pipe.Cmds {
			for _, arg := range command.Args {
				walk(arg)
			}
		}
	}
	walk = func(node parse.Node) {
		switch n := node.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, child := range n.Nodes {
				walk(child)
			}
		case *parse.ActionNode:
			walkPipe(n.Pipe)
		case *parse.FieldNode:
			fields = append(fields, append([]string(nil), n.Ident...))
		case *parse.PipeNode:
			walkPipe(n)
		case *parse.IfNode:
			walkPipe(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			// Dot changes inside range and with bodies; only the pipeline refers
			// to the row itself.
			walkPipe(n.Pipe)
		case *parse.WithNode:
			walkPipe(n.Pipe)
		case *parse.TemplateNode:
			walkPipe(n.Pipe)
		}
	}
	if tmpl.Tree != nil {
		walk(tmpl.Tree.Root)
	}
	return fields
}

// templateNull stands in for JSON null in template data: it prints as empty
// text, is false in conditions, and encodes back to null with the json func.
type templateNull string

func (templateNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// templateHeader is the value every field takes while a table template renders
// its header row. Template funcs pass it through unchanged.
type templateHeader string

var cliTemplateFuncs = template.FuncMap{
	"json": func(value any) (string, error) {
		if header, ok := value.(templateHeader); ok {
			return string(header), nil
		}
		data, err := marshalCLIJSON(value, false)
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(string(data), "\n"), nil
	},
	"join": func(value any, separator string) string {
		switch v := value.(type) {
		case templateHeader:
			return string(v)
		case []any:
			parts := make([]string, len(v))
			for i, item := range v {
				parts[i] = formatCell(item)
			}
			return strings.Join(parts, separator)
		default:
			return formatCell(value)
		}
	},
	"lower": func(value any) string {
		if header, ok := value.(templateHeader); ok {
			return string(header)
		}
		return strings.ToLower(formatCell(value))
	},
	"upper": func(value any) string {
		return strings.ToUpper(formatCell(value))
	},
}

// cliColumn maps a JSON field to a table or TSV column.
type cliColumn struct {
	Header string
	Key    string
}

// cliResult is everything a command can render. Items are the JSONL lines and
// template rows; Rows feed table and TSV output and default to Items.
type cliResult struct {
	Document any
	Items    any
	Rows     any
	Columns  []cliColumn
}

func renderCLIResult(format cliFormat, noHeaders bool, result cliResult) ([]byte, error) {
	var buffer bytes.Buffer
	switch format.kind {
	case formatJSON:
		data, err := marshalCLIJSON(result.Document, true)
		if err != nil {
			return nil, err
		}
		buffer.Write(data)
	case formatJSONL:
		items, err := cliJSONItems(result.Items)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			data, err := marshalCLIJSON(item, false)
			if err != nil {
				return nil, err
			}
			buffer.Write(data)
		}
	case formatTable, formatTSV:
		source := result.Rows
		if source == nil {
			source = result.Items
		}
		rows, err := cliRowMaps(source)
		if err != nil {
			return nil, err
		}
		writeColumns(&buffer, format.kind == formatTSV, noHeaders, result.Columns, rows)
	case formatTemplate, formatTableTemplate:
		rows, err := cliRowMaps(result.Items)
		if err != nil {
			return nil, err
		}
		if err := writeTemplateRows(&buffer, format, noHeaders, rows); err != nil {
			return nil, err
		}
	}
	return []byte(redactCredentials(buffer.String())), nil
}

func marshalCLIJSON(value any, indent bool) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// cliJSONItems returns the elements of a slice of rows without changing them.
func cliJSONItems(items any) ([]json.RawMessage, error) {
	if items == nil {
		return nil, nil
	}
	data, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("render rows: %w", err)
	}
	return raw, nil
}

// cliRowMaps decodes rows into their JSON objects so json, jsonl, table
// columns, and templates share one field vocabulary.
func cliRowMaps(items any) ([]map[string]any, error) {
	raw, err := cliJSONItems(items)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		decoder := json.NewDecoder(bytes.NewReader(item))
		decoder.UseNumber()
		var row map[string]any
		if err := decoder.Decode(&row); err != nil {
			return nil, fmt.Errorf("render rows: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// cliRowKeys lists the JSON fields of a row value in document order; rows
// always carry every key.
func cliRowKeys(row any) []string {
	if row == nil {
		return nil
	}
	data, err := json.Marshal(row)
	if err != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	keys := []string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil
		}
		key, _ := token.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			return nil
		}
	}
	return keys
}

func formatCell(value any) string {
	switch v := value.(type) {
	case nil, templateNull:
		return ""
	case templateHeader:
		return string(v)
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = formatCell(item)
		}
		return strings.Join(parts, ",")
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

var (
	tableCellCleaner = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")
	tsvCellCleaner   = strings.NewReplacer("\t", " ", "\r\n", " ", "\r", " ", "\n", " ")
)

func writeColumns(w io.Writer, tsv, noHeaders bool, columns []cliColumn, rows []map[string]any) {
	out := w
	var table *tabwriter.Writer
	if !tsv {
		table = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		out = table
	}
	if !noHeaders {
		headers := make([]string, len(columns))
		for i, column := range columns {
			headers[i] = column.Header
			if tsv {
				headers[i] = strings.ReplaceAll(column.Header, " ", "_")
			}
		}
		fmt.Fprintln(out, strings.Join(headers, "\t"))
	}
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, column := range columns {
			if tsv {
				cells[i] = tsvCellCleaner.Replace(formatCell(row[column.Key]))
			} else {
				cells[i] = strings.ReplaceAll(tableCellCleaner.Replace(formatCell(row[column.Key])), "\t", " ")
			}
		}
		fmt.Fprintln(out, strings.Join(cells, "\t"))
	}
	if table != nil {
		table.Flush()
	}
}

func writeTemplateRows(w io.Writer, format cliFormat, noHeaders bool, rows []map[string]any) error {
	out := w
	var table *tabwriter.Writer
	if format.kind == formatTableTemplate {
		table = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		out = table
		if !noHeaders {
			var header bytes.Buffer
			// A header the template cannot render, for example one that ranges
			// over a field, is omitted rather than failing the command.
			if err := format.template.Execute(&header, headerData(format.fields)); err == nil {
				fmt.Fprintln(out, strings.TrimRight(header.String(), "\n"))
			}
		}
	}
	for _, row := range rows {
		var line bytes.Buffer
		if err := format.template.Execute(&line, templateData(row)); err != nil {
			return fmt.Errorf("render --format template: %v", err)
		}
		fmt.Fprintln(out, strings.TrimRight(line.String(), "\n"))
	}
	if table != nil {
		return table.Flush()
	}
	return nil
}

func headerData(fields [][]string) map[string]any {
	data := make(map[string]any)
	for _, chain := range fields {
		current := data
		for i, ident := range chain {
			if i == len(chain)-1 {
				if _, nested := current[ident].(map[string]any); !nested {
					current[ident] = templateHeader(strings.ToUpper(strings.Join(chain, ".")))
				}
				break
			}
			next, ok := current[ident].(map[string]any)
			if !ok {
				next = make(map[string]any)
				current[ident] = next
			}
			current = next
		}
	}
	return data
}

func templateData(value any) any {
	switch v := value.(type) {
	case nil:
		return templateNull("")
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = templateData(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = templateData(item)
		}
		return out
	default:
		return value
	}
}
