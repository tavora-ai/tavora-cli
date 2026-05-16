package jsonc

import (
	"strings"
	"testing"
)

func TestStrip_LineComment(t *testing.T) {
	in := []byte(`{"a": 1, // trailing comment
"b": 2}`)
	out := Strip(in)
	if !strings.Contains(string(out), `"a": 1, `) {
		t.Fatalf("expected line comment blanked; got %q", out)
	}
	if strings.Contains(string(out), "trailing comment") {
		t.Fatalf("comment leaked through: %q", out)
	}
	if len(out) != len(in) {
		t.Fatalf("Strip must preserve byte length: got %d want %d", len(out), len(in))
	}
}

func TestStrip_BlockComment(t *testing.T) {
	in := []byte(`{"a": /* inline */ 1, "b": 2}`)
	out := Strip(in)
	if strings.Contains(string(out), "inline") {
		t.Fatalf("block comment leaked: %q", out)
	}
}

func TestStrip_TrailingComma(t *testing.T) {
	in := []byte(`{"a": 1, "b": 2,}`)
	out := Strip(in)
	v := map[string]int{}
	if err := Unmarshal(out, &v); err != nil {
		t.Fatalf("expected trailing comma stripped: %v (cleaned=%q)", err, out)
	}
}

func TestStrip_StringWithSlashes(t *testing.T) {
	in := []byte(`{"a": "// not a comment", "b": "/* nor this */"}`)
	v := map[string]string{}
	if err := Unmarshal(in, &v); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if v["a"] != "// not a comment" || v["b"] != "/* nor this */" {
		t.Fatalf("string contents got mangled: %#v", v)
	}
}

func TestUnmarshal_LineColOnError(t *testing.T) {
	in := []byte(`{
  "a": 1,
  "b": notjson,
}`)
	var v map[string]any
	err := Unmarshal(in, &v)
	if err == nil {
		t.Fatalf("expected decode error")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("expected line 3 in error, got: %s", err)
	}
}
