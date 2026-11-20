package graphql_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/IodeSystems/graphql-go/v3"
	"github.com/IodeSystems/graphql-go/v3/language/parser"
)

// Input coercion of the built-in scalars. Literals in the query text
// follow the spec and graphql-js strictly: Int accepts only integral
// numbers in 32-bit signed range, Float only numbers, ID only strings
// and integers. Variables are also accepted when they convert without
// loss: an integral float or a numeric string for Int, a numeric
// string for Float. Everything else is an error, never a silent
// truncation or conversion.

var inputCoercionInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "CoercionInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"i": &graphql.InputObjectFieldConfig{Type: graphql.Int},
	},
})

func inputCoercionSchema(t *testing.T) graphql.Schema {
	t.Helper()
	show := func(v interface{}) string { return fmt.Sprintf("%T:%v", v, v) }
	field := func(argType graphql.Input, pick func(interface{}) interface{}) *graphql.Field {
		return &graphql.Field{
			Type: graphql.String,
			Args: graphql.FieldConfigArgument{"a": &graphql.ArgumentConfig{Type: argType}},
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				return show(pick(p.Args["a"])), nil
			},
		}
	}
	same := func(v interface{}) interface{} { return v }
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"int":   field(graphql.Int, same),
				"float": field(graphql.Float, same),
				"id":    field(graphql.ID, same),
				"obj": field(inputCoercionInput, func(v interface{}) interface{} {
					m, _ := v.(map[string]interface{})
					return m["i"]
				}),
				"list": field(graphql.NewList(graphql.Int), func(v interface{}) interface{} {
					l, _ := v.([]interface{})
					if len(l) != 2 {
						return l
					}
					return l[1]
				}),
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

type coercionCase struct {
	name string
	raw  string // JSON for variables, GraphQL syntax for literals
	want string // resolver output; "" means a field/variable error is expected
}

// runCoercion runs one case through graphql.Do and through
// PlanQuery + ExecutePlanAppend, fails the test when the two disagree,
// and returns the resolver output, or "" with a non-zero error count.
func runCoercion(t *testing.T, schema graphql.Schema, query string, vars map[string]interface{}) (string, int) {
	t.Helper()
	res := graphql.Do(graphql.Params{Schema: schema, RequestString: query, VariableValues: vars})
	got, nerr := "", len(res.Errors)
	if nerr == 0 {
		data, _ := res.Data.(map[string]interface{})
		for _, v := range data {
			got, _ = v.(string)
		}
	}

	doc, err := parser.Parse(parser.ParseParams{Source: query})
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	if vr := graphql.ValidateDocument(&schema, doc, nil); !vr.IsValid {
		if nerr == 0 {
			t.Errorf("%s: Do ok, validation failed: %v", query, vr.Errors)
		}
		return "", nerr
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		t.Fatalf("plan %q: %v", query, err)
	}
	out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc, Args: vars}, nil)
	appendGot, appendErr := "", len(specErrs)
	if appendErr == 0 {
		var dec struct {
			Data   map[string]interface{} `json:"data"`
			Errors []interface{}          `json:"errors"`
		}
		if err := json.Unmarshal(out, &dec); err != nil {
			t.Fatalf("decode append %q: %v", out, err)
		}
		appendErr = len(dec.Errors)
		if appendErr == 0 {
			for _, v := range dec.Data {
				appendGot, _ = v.(string)
			}
		}
	}
	if (nerr > 0) != (appendErr > 0) || appendGot != got {
		t.Errorf("%s vars=%v: Do=%q (errors=%d), ExecutePlanAppend=%q (errors=%d)", query, vars, got, nerr, appendGot, appendErr)
	}
	return got, nerr
}

func checkCoercion(t *testing.T, schema graphql.Schema, field, varType string, wrap func(string) string, literal bool, c coercionCase) {
	t.Helper()
	var (
		query string
		vars  map[string]interface{}
	)
	if literal {
		query = fmt.Sprintf(`{ %s(a: %s) }`, field, wrap(c.raw))
	} else {
		query = fmt.Sprintf(`query($v: %s) { %s(a: $v) }`, varType, field)
		raw := `{"v":` + wrap(c.raw) + `}`
		if err := json.Unmarshal([]byte(raw), &vars); err != nil {
			t.Fatalf("bad case JSON %q: %v", c.raw, err)
		}
		// The same variables decoded with UseNumber (json.Number) must
		// coerce the same way.
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var numVars map[string]interface{}
		if err := dec.Decode(&numVars); err != nil {
			t.Fatalf("bad case JSON %q: %v", c.raw, err)
		}
		checkCoercionResult(t, schema, query+" (UseNumber)", query, numVars, c)
	}
	checkCoercionResult(t, schema, query, query, vars, c)
}

func checkCoercionResult(t *testing.T, schema graphql.Schema, label, query string, vars map[string]interface{}, c coercionCase) {
	t.Helper()
	got, nerr := runCoercion(t, schema, query, vars)
	if c.want == "" {
		if nerr == 0 {
			t.Errorf("%s: want error, got %q", label, got)
		}
		return
	}
	if nerr > 0 || got != c.want {
		t.Errorf("%s vars=%v: want %q, got %q (errors=%d)", label, vars, c.want, got, nerr)
	}
}

func TestInputCoercion_Int(t *testing.T) {
	schema := inputCoercionSchema(t)
	// The parser has no null literal (pre-existing, out of scope here),
	// so null is checked as a variable only.
	// JSON 2.0 decodes to float64(2), an integral number: accepted, as
	// graphql-js does (it cannot tell 2.0 from 2). The literal 2.0 is a
	// FloatValue and is rejected, as graphql-js does.
	vars := []coercionCase{
		{"int", `2`, "int:2"},
		{"integral float", `2.0`, "int:2"},
		{"exponent", `1e3`, "int:1000"},
		{"fraction", `2.5`, ""},
		{"string", `"2"`, "int:2"},
		{"negative string", `"-3"`, "int:-3"},
		{"integral float string", `"2.0"`, "int:2"},
		{"exponent string", `"1e3"`, "int:1000"},
		{"max int32 string", `"2147483647"`, "int:2147483647"},
		{"fraction string", `"2.5"`, ""},
		{"above int32 string", `"2147483648"`, ""},
		{"leading space string", `" 2"`, ""},
		{"trailing space string", `"2 "`, ""},
		{"plus string", `"+2"`, ""},
		{"hex string", `"0x10"`, ""},
		{"leading zero string", `"02"`, ""},
		{"empty string", `""`, ""},
		{"word string", `"abc"`, ""},
		{"bool string", `"true"`, ""},
		{"bool", `true`, ""},
		{"above int32", `2147483648`, ""},
		{"below int32", `-2147483649`, ""},
		{"max int32", `2147483647`, "int:2147483647"},
		{"min int32", `-2147483648`, "int:-2147483648"},
		{"null", `null`, "<nil>:<nil>"},
	}
	literals := []coercionCase{
		{"int", `2`, "int:2"},
		{"float literal", `2.0`, ""},
		{"fraction", `2.5`, ""},
		{"string", `"2"`, ""},
		{"bool", `true`, ""},
		{"above int32", `2147483648`, ""},
		{"below int32", `-2147483649`, ""},
		{"max int32", `2147483647`, "int:2147483647"},
		{"min int32", `-2147483648`, "int:-2147483648"},
	}
	shapes := []struct {
		field, varType string
		wrapVar        func(string) string
		wrapLit        func(string) string
	}{
		{"int", "Int", func(s string) string { return s }, func(s string) string { return s }},
		{"obj", "CoercionInput", func(s string) string { return `{"i":` + s + `}` }, func(s string) string { return `{i: ` + s + `}` }},
		{"list", "[Int]", func(s string) string { return `[1,` + s + `]` }, func(s string) string { return `[1, ` + s + `]` }},
	}
	for _, sh := range shapes {
		for _, c := range vars {
			t.Run(sh.field+"/variable/"+c.name, func(t *testing.T) {
				checkCoercion(t, schema, sh.field, sh.varType, sh.wrapVar, false, c)
			})
		}
		for _, c := range literals {
			t.Run(sh.field+"/literal/"+c.name, func(t *testing.T) {
				checkCoercion(t, schema, sh.field, sh.varType, sh.wrapLit, true, c)
			})
		}
	}
}

func TestInputCoercion_Float(t *testing.T) {
	schema := inputCoercionSchema(t)
	id := func(s string) string { return s }
	vars := []coercionCase{
		{"int", `2`, "float64:2"},
		{"fraction", `2.5`, "float64:2.5"},
		{"exponent", `1e3`, "float64:1000"},
		{"string", `"2.5"`, "float64:2.5"},
		{"int string", `"2"`, "float64:2"},
		{"exponent string", `"1e3"`, "float64:1000"},
		{"NaN string", `"NaN"`, ""},
		{"Inf string", `"Inf"`, ""},
		{"Infinity string", `"Infinity"`, ""},
		{"overflow string", `"1e400"`, ""},
		{"leading space string", `" 2.5"`, ""},
		{"word string", `"abc"`, ""},
		{"bool string", `"true"`, ""},
		{"bool", `true`, ""},
		{"null", `null`, "<nil>:<nil>"},
	}
	literals := []coercionCase{
		{"int", `2`, "float64:2"},
		{"fraction", `2.5`, "float64:2.5"},
		{"string", `"2.5"`, ""},
		{"bool", `true`, ""},
	}
	for _, c := range vars {
		t.Run("variable/"+c.name, func(t *testing.T) { checkCoercion(t, schema, "float", "Float", id, false, c) })
	}
	for _, c := range literals {
		t.Run("literal/"+c.name, func(t *testing.T) { checkCoercion(t, schema, "float", "Float", id, true, c) })
	}
}

func TestInputCoercion_ID(t *testing.T) {
	schema := inputCoercionSchema(t)
	id := func(s string) string { return s }
	cases := []coercionCase{
		{"string", `"abc"`, "string:abc"},
		{"numeric string", `"2"`, "string:2"},
		{"int", `2`, "string:2"},
		{"big int", `9007199254740991`, "string:9007199254740991"},
		{"fraction", `2.5`, ""},
		{"bool", `true`, ""},
		{"null", `null`, "<nil>:<nil>"},
	}
	for _, c := range cases {
		t.Run("variable/"+c.name, func(t *testing.T) { checkCoercion(t, schema, "id", "ID", id, false, c) })
		if c.raw != `null` {
			t.Run("literal/"+c.name, func(t *testing.T) { checkCoercion(t, schema, "id", "ID", id, true, c) })
		}
	}
	// JSON 2.0 is integral: accepted as "2" (graphql-js: String(2.0) === "2").
	t.Run("variable/integral float", func(t *testing.T) {
		checkCoercion(t, schema, "id", "ID", id, false, coercionCase{"integral float", `2.0`, "string:2"})
	})
	t.Run("variable/exponent", func(t *testing.T) {
		checkCoercion(t, schema, "id", "ID", id, false, coercionCase{"exponent", `1e3`, "string:1000"})
	})
	t.Run("literal/float", func(t *testing.T) {
		checkCoercion(t, schema, "id", "ID", id, true, coercionCase{"float", `2.0`, ""})
	})
}

func TestInputCoercion_ParseValueDirect(t *testing.T) {
	// Go-typed values that never come from JSON but reach ParseValue from
	// callers building VariableValues by hand.
	two := 2
	s := "2"
	cases := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"Int int64", graphql.Int.ParseValue(int64(2)), 2},
		{"Int *int", graphql.Int.ParseValue(&two), 2},
		{"Int float32 fraction", graphql.Int.ParseValue(float32(2.5)), nil},
		{"Int *string", graphql.Int.ParseValue(&s), 2},
		{"Int nil *string", graphql.Int.ParseValue((*string)(nil)), nil},
		{"Int string 2.0", graphql.Int.ParseValue("2.0"), 2},
		{"Int string fraction", graphql.Int.ParseValue("2.5"), nil},
		{"Int string overflow", graphql.Int.ParseValue("9876504321"), nil},
		{"Int string trailing dot", graphql.Int.ParseValue("2."), nil},
		{"Int string bare exponent", graphql.Int.ParseValue("2e"), nil},
		{"Int uint64 overflow", graphql.Int.ParseValue(uint64(1 << 40)), nil},
		{"Int json.Number", graphql.Int.ParseValue(json.Number("42")), 42},
		{"Int json.Number 2.0", graphql.Int.ParseValue(json.Number("2.0")), 2},
		{"Int json.Number fraction", graphql.Int.ParseValue(json.Number("2.5")), nil},
		{"Int json.Number overflow", graphql.Int.ParseValue(json.Number("9876504321")), nil},
		{"Int json.Number junk", graphql.Int.ParseValue(json.Number("x")), nil},
		{"Float json.Number", graphql.Float.ParseValue(json.Number("2.5")), 2.5},
		{"Float json.Number junk", graphql.Float.ParseValue(json.Number("x")), nil},
		{"Float *string", graphql.Float.ParseValue(&s), 2.0},
		{"Float string", graphql.Float.ParseValue("-2.5E-1"), -0.25},
		{"Float string NaN", graphql.Float.ParseValue("NaN"), nil},
		{"Float json.Number NaN", graphql.Float.ParseValue(json.Number("NaN")), nil},
		{"Float bool", graphql.Float.ParseValue(true), nil},
		{"Float int", graphql.Float.ParseValue(2), 2.0},
		{"ID *string", graphql.ID.ParseValue(&s), "2"},
		{"ID int64", graphql.ID.ParseValue(int64(2)), "2"},
		{"ID map", graphql.ID.ParseValue(map[string]interface{}{}), nil},
		{"ID json.Number", graphql.ID.ParseValue(json.Number("42")), "42"},
		{"ID json.Number beyond int64", graphql.ID.ParseValue(json.Number("-123456789012345678901234567890")), "-123456789012345678901234567890"},
		{"ID json.Number 2.0", graphql.ID.ParseValue(json.Number("2.0")), "2"},
		{"ID json.Number 1e3", graphql.ID.ParseValue(json.Number("1e3")), "1000"},
		{"ID json.Number fraction", graphql.ID.ParseValue(json.Number("2.5")), nil},
		{"ID json.Number inexact", graphql.ID.ParseValue(json.Number("1e300")), nil},
		{"ID json.Number junk", graphql.ID.ParseValue(json.Number("x")), nil},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: want %#v, got %#v", c.name, c.want, c.got)
		}
	}
}
