package parser

import (
	"testing"

	"github.com/IodeSystems/graphql-go/v2/language/ast"
	"github.com/IodeSystems/graphql-go/v2/language/kinds"
	"github.com/IodeSystems/graphql-go/v2/language/printer"
)

// `null` parses as a NullValue in every value position and prints back
// as `null` (spec 2.9.5).
func TestNullValue_RoundTrip(t *testing.T) {
	for _, src := range []string{
		"{\n  f(a: null)\n}\n",
		"{\n  f(a: [1, null, [null]])\n}\n",
		"{\n  f(a: {x: null, y: {z: null}})\n}\n",
		"{\n  f @d(a: null)\n}\n",
		"query Q($v: Int = null, $w: [Int] = [null], $o: In = {x: null}) {\n  f(a: $v)\n}\n",
		"type Q {\n  f(a: Boolean = null): Int\n}\n",
		"input In {\n  x: Int = null\n}\n",
		"directive @d(a: Int = null) on FIELD\n",
	} {
		doc, err := Parse(ParseParams{Source: src})
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		if got := printer.Print(doc); got != src {
			t.Errorf("round trip:\n  got  %q\n  want %q", got, src)
		}
	}
}

func TestNullValue_Node(t *testing.T) {
	doc, err := Parse(ParseParams{Source: `{ f(a: null, b: nullable, c: [null]) }`})
	if err != nil {
		t.Fatal(err)
	}
	args := doc.Definitions[0].(*ast.OperationDefinition).SelectionSet.Selections[0].(*ast.Field).Arguments

	n, ok := args[0].Value.(*ast.NullValue)
	if !ok {
		t.Fatalf("a: got %T, want *ast.NullValue", args[0].Value)
	}
	if n.Kind != kinds.NullValue || n.GetValue() != nil {
		t.Errorf("a: Kind %q, GetValue %v", n.Kind, n.GetValue())
	}
	if n.Loc == nil || n.Loc.Start != 7 || n.Loc.End != 11 {
		t.Errorf("a: Loc %+v, want 7..11", n.Loc)
	}
	// Only the exact name `null` is the null literal.
	if e, ok := args[1].Value.(*ast.EnumValue); !ok || e.Value != "nullable" {
		t.Errorf("b: got %#v, want EnumValue nullable", args[1].Value)
	}
	if _, ok := args[2].Value.(*ast.ListValue).Values[0].(*ast.NullValue); !ok {
		t.Errorf("c[0]: got %T", args[2].Value.(*ast.ListValue).Values[0])
	}
}

func TestNullValue_ParseValueConst(t *testing.T) {
	v, err := ParseValue(ParseParams{Source: `{a: null, b: [null]}`})
	if err != nil {
		t.Fatal(err)
	}
	if got := printer.Print(v); got != `{a: null, b: [null]}` {
		t.Errorf("got %q", got)
	}
}
