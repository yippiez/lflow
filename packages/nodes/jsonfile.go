package nodes

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/lflow/lflow/packages/database"
	"github.com/pkg/errors"
)

// jsonCodec binds a .json config file to a node subtree — pure structure:
//
//	{"key": scalar}   ⇄ a text leaf `key: <literal>` (the literal stays raw
//	                    JSON, so 1, "1" and true keep their types)
//	{"key": {…}}      ⇄ a text node `key` with the entries as children
//	{"key": […]}      ⇄ a text node `key []` with the items as children
//	array item        ⇄ a leaf `<literal>`, or `{}` / `[]` container nodes
//
// JSON has no comments, so the format is restricted to text nodes only — the
// /type picker offers nothing else, and a foreign node type fails the save
// with a clear error instead of writing an invalid file. Formatting
// normalizes to 2-space indentation on save.
type jsonCodec struct{}

func init() { fileCodecs = append(fileCodecs, jsonCodec{}) }

func (jsonCodec) Name() string             { return "json" }
func (jsonCodec) Exts() []string           { return []string{".json"} }
func (jsonCodec) Allowed() map[string]bool { return allowed(database.TypeText) }

func (jsonCodec) Parse(src string) ([]*SrcNode, error) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(src))
	dec.UseNumber() // numbers keep their written form
	v, err := decodeOrdered(dec)
	if err != nil {
		return nil, errors.Wrap(err, "parsing json")
	}
	// exactly one document: trailing content (a second concatenated value,
	// JSONL) would be silently deleted on save if the open succeeded.
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing content after the JSON document")
	}
	// only containers have a node-tree reading; a legal top-level scalar
	// (`42`, `"s"`, `true`) would parse to an EMPTY doc and the next save
	// would replace the file with {} — refuse the open instead.
	switch v.(type) {
	case jobj, jarr:
	default:
		return nil, errors.New("only a top-level object or array can open as an outline")
	}
	root := &SrcNode{}
	jsonValueKids(root, v)
	// remember a top-level array through a marker leaf
	if _, isArr := v.(jarr); isArr {
		root.Kids = append([]*SrcNode{{Type: database.TypeText, Text: "[]"}}, root.Kids...)
	}
	return root.Kids, nil
}

// ordered is one key/value entry; a bare array item has Key == nil.
type ordered struct {
	Key *string
	Val any
}

// jobj / jarr keep container kind through the ordered decode — an empty
// object and an empty array must not collapse into each other.
type jobj []ordered
type jarr []ordered

// decodeOrdered reads one JSON value keeping object order: objects become
// jobj, arrays jarr, scalars stay json.Number/string/bool/nil.
func decodeOrdered(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch d := t.(type) {
	case json.Delim:
		switch d {
		case '{':
			out := jobj{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				out = append(out, ordered{Key: &k, Val: v})
			}
			_, err := dec.Token() // consume }
			return out, err
		case '[':
			out := jarr{}
			for dec.More() {
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				out = append(out, ordered{Val: v})
			}
			_, err := dec.Token() // consume ]
			return out, err
		}
	}
	return t, nil
}

// scalarLiteral renders a scalar as raw JSON.
func scalarLiteral(v any) string {
	switch s := v.(type) {
	case json.Number:
		return s.String()
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// containerEntries unwraps a container value; ok is false for scalars.
func containerEntries(v any) (entries []ordered, arr, ok bool) {
	switch c := v.(type) {
	case jobj:
		return []ordered(c), false, true
	case jarr:
		return []ordered(c), true, true
	}
	return nil, false, false
}

// jsonValueKids appends v's entries as n's children.
func jsonValueKids(n *SrcNode, v any) {
	entries, _, ok := containerEntries(v)
	if !ok {
		return
	}
	for _, e := range entries {
		sub, arr, isContainer := containerEntries(e.Val)
		var text string
		switch {
		case e.Key != nil && !isContainer:
			text = jsonKeyText(*e.Key) + ": " + scalarLiteral(e.Val)
		case e.Key != nil && arr:
			text = jsonKeyText(*e.Key) + " []"
		case e.Key != nil && len(sub) == 0:
			text = jsonKeyText(*e.Key) + " {}" // an empty object must not read as a scalar leaf
		case e.Key != nil:
			text = jsonKeyText(*e.Key)
		case !isContainer:
			text = scalarLiteral(e.Val)
		case arr:
			text = "[]"
		default:
			text = "{}"
		}
		k := n.Kid(&SrcNode{Type: database.TypeText, Text: text})
		jsonValueKids(k, e.Val)
	}
}

// keyNeedsQuote reports whether a raw key would misparse in the node-text
// encoding: `note: important` truncates on the ": " that belongs to the key
// itself, and a key ending " []"/" {}" is indistinguishable from a container
// marker.
func keyNeedsQuote(key string) bool {
	return strings.Contains(key, ": ") || strings.HasSuffix(key, " []") ||
		strings.HasSuffix(key, " {}") || strings.HasPrefix(key, `"`)
}

// jsonKeyText renders a key for the node-text encoding: JSON-quoted when the
// raw form would be ambiguous (see keyNeedsQuote / nodeJSONShape), the clean
// unquoted form otherwise.
func jsonKeyText(key string) string {
	if !keyNeedsQuote(key) {
		return key
	}
	b, _ := json.Marshal(key)
	return string(b)
}

func (c jsonCodec) Render(doc []*SrcNode) (string, error) {
	kind := "object"
	if len(doc) > 0 && doc[0].Text == "[]" && len(doc[0].Kids) == 0 {
		kind = "array"
		doc = doc[1:]
	}
	var b strings.Builder
	if err := renderJSONContainer(&b, doc, kind, 0); err != nil {
		return "", err
	}
	return b.String() + "\n", nil
}

// nodeJSONShape reads a node's text back into (key, literal, container kind).
func nodeJSONShape(n *SrcNode) (key, literal, kind string, err error) {
	if n.Type != database.TypeText {
		return "", "", "", errors.Errorf("json cannot hold a %s node (%q) — text nodes only", n.Type, n.Text)
	}
	text := strings.TrimSpace(n.Text)
	// a key that was ambiguous in its raw form was JSON-quoted on write
	// (jsonKeyText) — decode the string literal first, then read whatever
	// suffix follows it. ok is false for a line that only LOOKS like a
	// quoted key (a bare quoted scalar, e.g. a `"a: b"` array item, decodes
	// clean but has no suffix and no children of its own) — that falls
	// through to the plain-literal handling below.
	if strings.HasPrefix(text, `"`) {
		if k, l, kd, ok := jsonQuotedKeyShape(text, len(n.Kids) > 0); ok {
			return k, l, kd, nil
		}
	}
	// container markers work with or without children, so an emptied
	// container still renders as its kind
	if k, ok := strings.CutSuffix(text, " []"); ok {
		return k, "", "array", nil
	}
	if k, ok := strings.CutSuffix(text, " {}"); ok {
		return k, "", "object", nil
	}
	if text == "[]" {
		return "", "", "array", nil
	}
	if text == "{}" {
		return "", "", "object", nil
	}
	if len(n.Kids) > 0 {
		return text, "", "object", nil
	}
	if i := strings.Index(text, ": "); i > 0 {
		return text[:i], text[i+2:], "", nil
	}
	return "", text, "", nil
}

// jsonQuotedKeyShape decodes a `"key"…` line's leading JSON string, then
// classifies what follows it: `: <literal>` a scalar, ` []`/` {}` a
// container marker, or nothing at all — a bare key, only valid when the node
// actually has children (hasKids), since that's the only shape jsonKeyText
// ever writes without a suffix; otherwise this isn't a key line at all (a
// bare quoted scalar) and ok comes back false so the caller falls back to
// treating the text as a plain literal.
func jsonQuotedKeyShape(text string, hasKids bool) (key, literal, kind string, ok bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	if dec.Decode(&key) != nil {
		return "", "", "", false
	}
	rest := text[dec.InputOffset():]
	switch {
	case rest == "" && hasKids:
		return key, "", "object", true
	case rest == " []":
		return key, "", "array", true
	case rest == " {}":
		return key, "", "object", true
	}
	if lit, ok := strings.CutPrefix(rest, ": "); ok {
		return key, lit, "", true
	}
	return "", "", "", false
}

func renderJSONContainer(b *strings.Builder, kids []*SrcNode, kind string, depth int) error {
	open, close := "{", "}"
	if kind == "array" {
		open, close = "[", "]"
	}
	if len(kids) == 0 {
		b.WriteString(open + close)
		return nil
	}
	pad := strings.Repeat("  ", depth+1)
	b.WriteString(open + "\n")
	for i, k := range kids {
		key, literal, childKind, err := nodeJSONShape(k)
		if err != nil {
			return err
		}
		b.WriteString(pad)
		if kind == "object" {
			kb, _ := json.Marshal(key)
			b.Write(kb)
			b.WriteString(": ")
		}
		if childKind != "" {
			if err := renderJSONContainer(b, k.Kids, childKind, depth+1); err != nil {
				return err
			}
		} else {
			if !json.Valid([]byte(literal)) {
				// a hand-typed bare word saves as a string
				lb, _ := json.Marshal(literal)
				literal = string(lb)
			}
			b.WriteString(literal)
		}
		if i < len(kids)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("  ", depth) + close)
	return nil
}

// DefaultType: everything in a .json session is a text node.
func (jsonCodec) DefaultType() string { return database.TypeText }
