package agentic

import (
	"fmt"
	"reflect"
	"strings"
)

// The provider adapters do not agree on structured output. The OpenAI adapter
// forwards Request.StructuredOutput as a response_format json_schema; the
// Anthropic adapter in trpc-agent-go v1.11.0 ignores the field entirely — it
// never reads Request.StructuredOutput at all.
//
// That difference is silent and it is dangerous. Against Anthropic the schema
// derived from the Go struct was never transmitted, so the model only had the
// prompt's word that it should "answer in JSON per the schema" and had to guess
// the field names. Go's json.Unmarshal ignores unknown fields, so a reply using
// different names decoded "successfully" into a zero-valued struct: a triage
// verdict of Substantive=false with an empty reason, every time, which routed
// the graph straight past the analyze and recommend nodes. The AI tier appeared
// to run and produced nothing.
//
// The fix is to stop depending on the transport for the contract. The expected
// shape is rendered from the same Go struct and appended to the prompt, so the
// model sees the exact field names whatever the provider does. Request-level
// structured output is still set, because where it is honoured it enforces the
// shape more strictly than instructions can.

// shapeOf renders a Go struct as an annotated JSON skeleton for a prompt.
//
// It is deliberately a shape, not a JSON Schema: schemas are verbose, and the
// tokens spent describing "type": "string" for every field are tokens not spent
// on the document. A skeleton with the real field names and a short note per
// field conveys the contract in a fraction of the space.
func shapeOf(v any) string {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return ""
	}
	var b strings.Builder
	writeStruct(&b, t, 0)
	return b.String()
}

func writeStruct(b *strings.Builder, t reflect.Type, depth int) {
	pad := strings.Repeat("  ", depth+1)

	// Fields are rendered first and joined afterwards so the separating comma
	// lands before the trailing comment rather than after it. With the comma
	// last, a line reads as though the comment text ends in one — exactly the
	// kind of ambiguity that makes a model reproduce the comment as data.
	type line struct{ text, note string }
	var lines []line

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := jsonName(f)
		if name == "" {
			continue
		}
		var val strings.Builder
		fmt.Fprintf(&val, "%s%q: ", pad, name)
		writeValue(&val, f.Type, depth+1)
		lines = append(lines, line{text: val.String(), note: fieldNote(f)})
	}

	b.WriteString("{\n")
	for i, l := range lines {
		b.WriteString(l.text)
		if i < len(lines)-1 {
			b.WriteString(",")
		}
		if l.note != "" {
			fmt.Fprintf(b, "   // %s", l.note)
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("  ", depth) + "}")
}

func writeValue(b *strings.Builder, t reflect.Type, depth int) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		b.WriteString(`"..."`)
	case reflect.Bool:
		b.WriteString("true|false")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString("0")
	case reflect.Float32, reflect.Float64:
		b.WriteString("0.0")
	case reflect.Slice, reflect.Array:
		b.WriteString("[")
		writeValue(b, t.Elem(), depth)
		b.WriteString(", ...]")
	case reflect.Struct:
		writeStruct(b, t, depth)
	default:
		b.WriteString("null")
	}
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	if tag == "" {
		return f.Name
	}
	if i := strings.Index(tag, ","); i >= 0 {
		tag = tag[:i]
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// fieldNote pulls the human hint out of the jsonschema tag, which is where the
// per-field guidance already lives on these structs.
//
// The description runs to the end of the tag rather than to the next comma:
// these descriptions enumerate allowed values ("critical, major, minor, or
// info"), and splitting on commas truncated exactly the field whose permitted
// values the model most needs to see.
func fieldNote(f reflect.StructField) string {
	tag := f.Tag.Get("jsonschema")
	i := strings.Index(tag, "description=")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(tag[i+len("description="):])
}

// contractFor builds the instruction block appended to every structured
// request.
func contractFor(out any) string {
	shape := shapeOf(out)
	if shape == "" {
		return ""
	}
	return "\n\nBalas HANYA dengan JSON yang cocok PERSIS dengan bentuk di bawah ini. " +
		"Gunakan nama field yang sama persis — jangan menerjemahkan atau mengganti namanya. " +
		"Jangan menambahkan teks apa pun di luar JSON.\n\n" + shape
}
