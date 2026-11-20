package graphql_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/IodeSystems/graphql-go/v2"
)

// Input coercion of the built-in scalars, per the spec (Type System ›
// Scalars › Input Coercion) and graphql-js: Int accepts only integral
// numbers in 32-bit signed range, Float only numbers, ID only strings
// and integral numbers. Everything else is a field error, never a
// silent truncation or conversion.

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

// runCoercion runs one case and returns the resolver output, or "" with
// a non-empty error list.
func runCoercion(t *testing.T, schema graphql.Schema, query string, vars map[string]interface{}) (string, int) {
	t.Helper()
	res := graphql.Do(graphql.Params{Schema: schema, RequestString: query, VariableValues: vars})
	if len(res.Errors) > 0 {
		return "", len(res.Errors)
	}
	data, _ := res.Data.(map[string]interface{})
	for _, v := range data {
		s, _ := v.(string)
		return s, 0
	}
	return "", 0
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
		if err := json.Unmarshal([]byte(`{"v":`+wrap(c.raw)+`}`), &vars); err != nil {
			t.Fatalf("bad case JSON %q: %v", c.raw, err)
		}
	}
	got, nerr := runCoercion(t, schema, query, vars)
	if c.want == "" {
		if nerr == 0 {
			t.Errorf("%s: want error, got %q", query, got)
		}
		return
	}
	if nerr > 0 || got != c.want {
		t.Errorf("%s vars=%v: want %q, got %q (errors=%d)", query, vars, c.want, got, nerr)
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
		{"fraction", `2.5`, ""},
		{"string", `"2"`, ""},
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
	cases := []coercionCase{
		{"int", `2`, "float64:2"},
		{"fraction", `2.5`, "float64:2.5"},
		{"string", `"2.5"`, ""},
		{"bool", `true`, ""},
		{"null", `null`, "<nil>:<nil>"},
	}
	for _, c := range cases {
		t.Run("variable/"+c.name, func(t *testing.T) { checkCoercion(t, schema, "float", "Float", id, false, c) })
		if c.raw != `null` {
			t.Run("literal/"+c.name, func(t *testing.T) { checkCoercion(t, schema, "float", "Float", id, true, c) })
		}
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
		{"Int *string", graphql.Int.ParseValue(&s), nil},
		{"Int uint64 overflow", graphql.Int.ParseValue(uint64(1 << 40)), nil},
		{"Int json.Number", graphql.Int.ParseValue(json.Number("42")), 42},
		{"Int json.Number 2.0", graphql.Int.ParseValue(json.Number("2.0")), 2},
		{"Int json.Number fraction", graphql.Int.ParseValue(json.Number("2.5")), nil},
		{"Int json.Number overflow", graphql.Int.ParseValue(json.Number("9876504321")), nil},
		{"Int json.Number junk", graphql.Int.ParseValue(json.Number("x")), nil},
		{"Float json.Number", graphql.Float.ParseValue(json.Number("2.5")), 2.5},
		{"Float json.Number junk", graphql.Float.ParseValue(json.Number("x")), nil},
		{"Float *string", graphql.Float.ParseValue(&s), nil},
		{"Float int", graphql.Float.ParseValue(2), 2.0},
		{"ID *string", graphql.ID.ParseValue(&s), "2"},
		{"ID int64", graphql.ID.ParseValue(int64(2)), "2"},
		{"ID map", graphql.ID.ParseValue(map[string]interface{}{}), nil},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: want %#v, got %#v", c.name, c.want, c.got)
		}
	}
}
