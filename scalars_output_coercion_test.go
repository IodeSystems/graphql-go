package graphql_test

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/IodeSystems/graphql-go/v3"
	"github.com/IodeSystems/graphql-go/v3/language/parser"
)

// Result coercion of the built-in scalars: a value converts only when
// nothing is lost; otherwise the field is null with an error at its
// path, and the null propagates per the non-null rules. Both executors
// (ExecutePlan map tree and ExecutePlanAppend) must agree.

type outputCase struct {
	name  string
	typ   *graphql.Scalar
	value interface{}
	want  interface{} // decoded JSON value; ignored when err != ""
	err   string      // expected error message; "" means no error
}

type namedInt int
type namedString string

func outputCoercionCases() []outputCase {
	s := "x"
	n := 7
	return []outputCase{
		// Int
		{"int", graphql.Int, 2, 2.0, ""},
		{"int integral float", graphql.Int, 2.0, 2.0, ""},
		{"int numeric string", graphql.Int, "2", 2.0, ""},
		{"int bool", graphql.Int, true, 1.0, ""},
		{"int json.Number", graphql.Int, json.Number("42"), 42.0, ""},
		{"int named", graphql.Int, namedInt(5), 5.0, ""},
		{"int pointer", graphql.Int, &n, 7.0, ""},
		{"int fraction", graphql.Int, 2.5, nil, "Int cannot represent non-integer value: 2.5"},
		{"int range", graphql.Int, int64(3000000000), nil, "Int cannot represent non 32-bit signed integer value: 3000000000"},
		{"int range int", graphql.Int, 3000000000, nil, "Int cannot represent non 32-bit signed integer value: 3000000000"},
		{"int range float", graphql.Int, 3e9, nil, "Int cannot represent non 32-bit signed integer value: 3000000000"},
		{"int word", graphql.Int, "abc", nil, `Int cannot represent non-integer value: "abc"`},
		{"int fraction string", graphql.Int, "2.5", nil, `Int cannot represent non-integer value: "2.5"`},
		{"int range string", graphql.Int, "3000000000", nil, `Int cannot represent non 32-bit signed integer value: "3000000000"`},
		{"int inf", graphql.Int, math.Inf(1), nil, "Int cannot represent non-integer value: Infinity"},
		{"int slice", graphql.Int, []int{1}, nil, "Int cannot represent non-integer value: [1]"},
		// Float
		{"float", graphql.Float, 2.5, 2.5, ""},
		{"float int", graphql.Float, 2, 2.0, ""},
		{"float string", graphql.Float, "2.5", 2.5, ""},
		{"float bool", graphql.Float, false, 0.0, ""},
		{"float inf", graphql.Float, math.Inf(-1), nil, "Float cannot represent non numeric value: -Infinity"},
		{"float word", graphql.Float, "abc", nil, `Float cannot represent non numeric value: "abc"`},
		{"float map", graphql.Float, map[string]int{}, nil, "Float cannot represent non numeric value: map[]"},
		// String
		{"string", graphql.String, "a", "a", ""},
		{"string pointer", graphql.String, &s, "x", ""},
		{"string named", graphql.String, namedString("n"), "n", ""},
		{"string bool", graphql.String, true, "true", ""},
		{"string int", graphql.String, 7, "7", ""},
		{"string float", graphql.String, 2.5, "2.5", ""},
		{"string nan", graphql.String, math.Inf(1), nil, "String cannot represent value: Infinity"},
		{"string map", graphql.String, map[string]int{"a": 1}, nil, "String cannot represent value: map[a:1]"},
		// Boolean
		{"bool", graphql.Boolean, true, true, ""},
		{"bool int", graphql.Boolean, 0, false, ""},
		{"bool string", graphql.Boolean, "false", false, ""},
		{"bool word", graphql.Boolean, "yes", nil, `Boolean cannot represent a non boolean value: "yes"`},
		{"bool slice", graphql.Boolean, []bool{}, nil, "Boolean cannot represent a non boolean value: []"},
		// ID
		{"id", graphql.ID, "a", "a", ""},
		{"id int", graphql.ID, 7, "7", ""},
		{"id integral float", graphql.ID, 7.0, "7", ""},
		{"id json.Number", graphql.ID, json.Number("12345678901234567890"), "12345678901234567890", ""},
		{"id fraction", graphql.ID, 2.5, nil, "ID cannot represent value: 2.5"},
		{"id bool", graphql.ID, true, nil, "ID cannot represent value: true"},
	}
}

type outputResult struct {
	Data   interface{} `json:"data"`
	Errors []struct {
		Message string        `json:"message"`
		Path    []interface{} `json:"path"`
	} `json:"errors"`
}

// runBothExecutors runs query through ExecutePlan and ExecutePlanAppend,
// fails when they disagree, and returns the decoded result.
func runBothExecutors(t *testing.T, schema graphql.Schema, query string) outputResult {
	t.Helper()
	doc, err := parser.Parse(parser.ParseParams{Source: query})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		t.Fatal(err)
	}
	params := graphql.ExecuteParams{Schema: schema, AST: doc}
	mapJSON, err := json.Marshal(graphql.ExecutePlan(plan, params))
	if err != nil {
		t.Fatal(err)
	}
	appendJSON, specErrs := graphql.ExecutePlanAppend(plan, params, nil)
	if len(specErrs) != 0 {
		t.Fatalf("append request errors: %v", specErrs)
	}
	var fromMap, fromAppend outputResult
	if err := json.Unmarshal(mapJSON, &fromMap); err != nil {
		t.Fatalf("decode %s: %v", mapJSON, err)
	}
	if err := json.Unmarshal(appendJSON, &fromAppend); err != nil {
		t.Fatalf("decode %s: %v", appendJSON, err)
	}
	if !reflect.DeepEqual(fromMap, fromAppend) {
		t.Fatalf("%s: executors disagree\n  ExecutePlan:       %s\n  ExecutePlanAppend: %s", query, mapJSON, appendJSON)
	}
	return fromMap
}

func outputSchema(t *testing.T, typ *graphql.Scalar, value interface{}) graphql.Schema {
	t.Helper()
	val := func(p graphql.ResolveParams) (interface{}, error) { return value, nil }
	list := func(p graphql.ResolveParams) (interface{}, error) {
		return []interface{}{value, value}, nil
	}
	inner := graphql.NewObject(graphql.ObjectConfig{
		Name: "Inner",
		Fields: graphql.Fields{
			"nn": &graphql.Field{Type: graphql.NewNonNull(typ), Resolve: val},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"f":       &graphql.Field{Type: typ, Resolve: val},
				"list":    &graphql.Field{Type: graphql.NewList(typ), Resolve: list},
				"nnItems": &graphql.Field{Type: graphql.NewList(graphql.NewNonNull(typ)), Resolve: list},
				"inner":   &graphql.Field{Type: inner, Resolve: func(graphql.ResolveParams) (interface{}, error) { return struct{}{}, nil }},
				"ok":      &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "ok", nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestOutputCoercion_BuiltinScalars(t *testing.T) {
	for _, c := range outputCoercionCases() {
		t.Run(c.name, func(t *testing.T) {
			schema := outputSchema(t, c.typ, c.value)
			res := runBothExecutors(t, schema, `{ f ok list nnItems inner { nn } }`)
			data, _ := res.Data.(map[string]interface{})
			if data["ok"] != "ok" {
				t.Fatalf("sibling field lost: %v", res.Data)
			}
			if c.err == "" {
				if len(res.Errors) != 0 {
					t.Fatalf("unexpected errors: %+v", res.Errors)
				}
				want := map[string]interface{}{
					"f": c.want, "ok": "ok",
					"list":    []interface{}{c.want, c.want},
					"nnItems": []interface{}{c.want, c.want},
					"inner":   map[string]interface{}{"nn": c.want},
				}
				if !reflect.DeepEqual(data, want) {
					t.Fatalf("want %v, got %v", want, data)
				}
				return
			}
			// f: null with error. list: each item null with its own
			// error. nnItems: a non-null item nulls the whole list (the
			// first item's error). inner.nn: non-null, so inner is null.
			want := map[string]interface{}{
				"f": nil, "ok": "ok",
				"list":    []interface{}{nil, nil},
				"nnItems": nil,
				"inner":   nil,
			}
			if !reflect.DeepEqual(data, want) {
				t.Fatalf("want data %v, got %v", want, data)
			}
			wantPaths := [][]interface{}{
				{"f"},
				{"list", 0.0},
				{"list", 1.0},
				{"nnItems", 0.0},
				{"inner", "nn"},
			}
			if len(res.Errors) != len(wantPaths) {
				t.Fatalf("want %d errors, got %+v", len(wantPaths), res.Errors)
			}
			for i, e := range res.Errors {
				if e.Message != c.err {
					t.Errorf("error %d: want message %q, got %q", i, c.err, e.Message)
				}
				if !reflect.DeepEqual(e.Path, wantPaths[i]) {
					t.Errorf("error %d: want path %v, got %v", i, wantPaths[i], e.Path)
				}
			}
		})
	}
}

// Typed-slice fast paths in the append walker ([]int, []float64,
// []string, []bool) must raise the same item errors.
func TestOutputCoercion_TypedLists(t *testing.T) {
	cases := []struct {
		name  string
		typ   *graphql.Scalar
		value interface{}
		err   string
	}{
		{"int overflow", graphql.Int, []int{1, 3000000000}, "Int cannot represent non 32-bit signed integer value: 3000000000"},
		{"int fraction", graphql.Int, []float64{1, 2.5}, "Int cannot represent non-integer value: 2.5"},
		{"float inf", graphql.Float, []float64{1, math.Inf(1)}, "Float cannot represent non numeric value: Infinity"},
		{"int word", graphql.Int, []string{"1", "abc"}, `Int cannot represent non-integer value: "abc"`},
		{"bool word", graphql.Boolean, []string{"true", "yes"}, `Boolean cannot represent a non boolean value: "yes"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema, err := graphql.NewSchema(graphql.SchemaConfig{
				Query: graphql.NewObject(graphql.ObjectConfig{
					Name: "Query",
					Fields: graphql.Fields{
						"list": &graphql.Field{Type: graphql.NewList(c.typ), Resolve: func(graphql.ResolveParams) (interface{}, error) { return c.value, nil }},
					},
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			res := runBothExecutors(t, schema, `{ list }`)
			items := res.Data.(map[string]interface{})["list"].([]interface{})
			if items[0] == nil || items[1] != nil {
				t.Fatalf("want [value, null], got %v", items)
			}
			if len(res.Errors) != 1 || res.Errors[0].Message != c.err || !reflect.DeepEqual(res.Errors[0].Path, []interface{}{"list", 1.0}) {
				t.Fatalf("want one error %q at [list 1], got %+v", c.err, res.Errors)
			}
		})
	}
}

// A custom scalar whose Serialize returns nil still means null, with no
// error.
func TestOutputCoercion_CustomScalarNilIsNull(t *testing.T) {
	custom := graphql.NewScalar(graphql.ScalarConfig{
		Name:      "Custom",
		Serialize: func(interface{}) interface{} { return nil },
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"c": &graphql.Field{Type: custom, Resolve: func(graphql.ResolveParams) (interface{}, error) { return 2.5, nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	res := runBothExecutors(t, schema, `{ c }`)
	if len(res.Errors) != 0 || res.Data.(map[string]interface{})["c"] != nil {
		t.Fatalf("want c: null and no errors, got %+v", res)
	}
}

// The public Serialize has no error channel: an invalid value is nil.
func TestOutputCoercion_SerializeReturnsNil(t *testing.T) {
	cases := []struct {
		name string
		got  interface{}
	}{
		{"Int fraction", graphql.Int.Serialize(2.5)},
		{"Float NaN", graphql.Float.Serialize(math.NaN())},
		{"String map", graphql.String.Serialize(map[string]int{})},
		{"Boolean word", graphql.Boolean.Serialize("yes")},
		{"ID bool", graphql.ID.Serialize(true)},
		{"String nil *T Stringer", graphql.String.Serialize((*stringerT)(nil))},
	}
	for _, c := range cases {
		if c.got != nil {
			t.Errorf("%s: want nil, got %#v", c.name, c.got)
		}
	}
	if got := graphql.String.Serialize(stringerT{}); got != "stringer" {
		t.Errorf("String Stringer: want %q, got %#v", "stringer", got)
	}
	if got := graphql.ID.Serialize(&stringerT{}); got != "stringer" {
		t.Errorf("ID *Stringer: want %q, got %#v", "stringer", got)
	}
}

type stringerT struct{}

func (stringerT) String() string { return "stringer" }
