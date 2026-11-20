package graphql

import (
	"reflect"
	"testing"

	"github.com/IodeSystems/graphql-go/v3/language/ast"
	"github.com/IodeSystems/graphql-go/v3/language/parser"
)

// coerceLiteral must agree with valueFromAST (value) and
// isValidLiteralValue (validity), which it stands in for on the
// plan-cache hit path.
func TestCoerceLiteralMatches(t *testing.T) {
	color := NewEnum(EnumConfig{Name: "Color", Values: EnumValueConfigMap{"RED": &EnumValueConfig{Value: 1}}})
	in := NewInputObject(InputObjectConfig{
		Name: "In",
		Fields: InputObjectConfigFieldMap{
			"a":  &InputObjectFieldConfig{Type: Int},
			"b":  &InputObjectFieldConfig{Type: Int, DefaultValue: 7},
			"n":  &InputObjectFieldConfig{Type: Int, DefaultValue: ExplicitNull},
			"r":  &InputObjectFieldConfig{Type: NewNonNull(Int)},
			"rd": &InputObjectFieldConfig{Type: NewNonNull(Int), DefaultValue: 5},
			"rn": &InputObjectFieldConfig{Type: NewNonNull(Int), DefaultValue: ExplicitNull},
			"l":  &InputObjectFieldConfig{Type: NewList(NewNonNull(String))},
		},
	})
	types := []Input{
		Int, NewNonNull(Int), String, Boolean, Float, ID, color, NewNonNull(color),
		NewList(Int), NewList(NewNonNull(Int)), NewNonNull(NewList(Int)), NewList(NewList(Int)),
		in, NewNonNull(in), NewList(in),
	}
	literals := []string{
		`null`, `1`, `1.5`, `"x"`, `true`, `RED`, `BLUE`,
		`[]`, `[1]`, `[1, null]`, `[null]`, `["x", 1]`, `[[1], [null], 2]`,
		`{}`, `{r: 1}`, `{r: null}`, `{r: 1, a: null}`, `{r: 1, b: null}`, `{r: 1, zz: 1}`,
		`{r: 1, rn: 2}`, `{rn: 2}`, `{r: 1, rn: 2, l: ["a", null]}`, `{r: 1, rn: 2, l: "a"}`,
		`{r: 1, r: 2, rn: 3}`, `[{r: 1, rn: 2}, null, {}]`,
	}
	for _, src := range literals {
		v, err := parser.ParseValue(parser.ParseParams{Source: src})
		if err != nil {
			t.Fatalf("parse %s: %v", src, err)
		}
		for _, typ := range types {
			got, gotValid := coerceLiteral(v.(ast.Value), typ)
			want := valueFromAST(v.(ast.Value), typ, nil)
			wantValid, _ := isValidLiteralValue(typ, v.(ast.Value))
			if gotValid != wantValid {
				t.Errorf("%s into %v: valid %v, isValidLiteralValue %v", src, typ, gotValid, wantValid)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s into %v: value %#v, valueFromAST %#v", src, typ, got, want)
			}
		}
	}
}
