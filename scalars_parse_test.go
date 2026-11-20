package graphql_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/IodeSystems/graphql-go/v3"
	"github.com/IodeSystems/graphql-go/v3/language/ast"
	"github.com/IodeSystems/graphql-go/v3/language/parser"
)

func TestTypeSystem_Scalar_ParseValueOutputDateTime(t *testing.T) {
	t1, _ := time.Parse(time.RFC3339, "2017-07-23T03:46:56.647Z")
	tests := []dateTimeSerializationTest{
		{nil, nil},
		{"", nil},
		{(*string)(nil), nil},
		{"2017-07-23", nil},
		{"2017-07-23T03:46:56.647Z", t1},
	}
	for _, test := range tests {
		val := graphql.DateTime.ParseValue(test.Value)
		if val != test.Expected {
			reflectedValue := reflect.ValueOf(test.Value)
			t.Fatalf("failed DateTime.ParseValue(%v(%v)), expected: %v, got %v", reflectedValue.Type(), test.Value, test.Expected, val)
		}
	}
}

func TestTypeSystem_Scalar_ParseLiteralOutputDateTime(t *testing.T) {
	t1, _ := time.Parse(time.RFC3339, "2017-07-23T03:46:56.647Z")
	for name, testCase := range map[string]struct {
		Literal  ast.Value
		Expected interface{}
	}{
		"String": {
			Literal: &ast.StringValue{
				Value: "2017-07-23T03:46:56.647Z",
			},
			Expected: t1,
		},
		"NotAString": {
			Literal:  &ast.IntValue{},
			Expected: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			parsed := graphql.DateTime.ParseLiteral(testCase.Literal)
			if parsed != testCase.Expected {
				t.Fatalf("failed DateTime.ParseLiteral(%T(%v)), expected: %v, got %v", testCase.Literal, testCase.Literal, parsed, testCase.Expected)
			}
		})
	}
}

func panickingScalarSchema(t *testing.T) graphql.Schema {
	t.Helper()
	sc := graphql.NewScalar(graphql.ScalarConfig{
		Name:         "Boom",
		Serialize:    func(v interface{}) interface{} { return v },
		ParseValue:   func(v interface{}) interface{} { panic("bad value") },
		ParseLiteral: func(v ast.Value) interface{} { panic("bad literal") },
	})
	s, err := graphql.NewSchema(graphql.SchemaConfig{Query: graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{"f": &graphql.Field{
			Type:    graphql.String,
			Args:    graphql.FieldConfigArgument{"a": &graphql.ArgumentConfig{Type: sc}},
			Resolve: func(p graphql.ResolveParams) (interface{}, error) { return "x", nil },
		}},
	})})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// upstream #679: a panic in a custom scalar's ParseValue or ParseLiteral
// must surface as a GraphQL error on every entry point, not crash.
func TestTypeSystem_Scalar_ParsePanicBecomesError(t *testing.T) {
	s := panickingScalarSchema(t)
	cases := []struct {
		name, query string
		vars        map[string]interface{}
	}{
		{"ParseValue", `query($a: Boom){f(a:$a)}`, map[string]interface{}{"a": 1}},
		{"ParseLiteral", `{f(a:1)}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/Do", func(t *testing.T) {
			r := graphql.Do(graphql.Params{Schema: s, RequestString: tc.query, VariableValues: tc.vars})
			if len(r.Errors) == 0 {
				t.Fatalf("expected error, got data %v", r.Data)
			}
		})
		t.Run(tc.name+"/PlanCache", func(t *testing.T) {
			for _, normalize := range []bool{false, true} {
				cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: normalize})
				pr := cache.Get(&s, tc.query, "")
				if pr.Plan == nil {
					if len(pr.Errors) == 0 {
						t.Fatalf("normalize=%v: no plan and no errors", normalize)
					}
					continue
				}
				// A plan is acceptable (ParseValue panics at execute
				// time); executing it must report an error.
				r := graphql.ExecutePlan(pr.Plan, graphql.ExecuteParams{Schema: s, Args: tc.vars})
				if len(r.Errors) == 0 {
					t.Fatalf("normalize=%v: ExecutePlan: expected error", normalize)
				}
				_, errs := graphql.ExecutePlanAppend(pr.Plan, graphql.ExecuteParams{Schema: s, Args: tc.vars}, nil)
				if len(errs) == 0 {
					t.Fatalf("normalize=%v: ExecutePlanAppend: expected error", normalize)
				}
			}
		})
	}

	t.Run("PlanQuery", func(t *testing.T) {
		doc, err := parser.Parse(parser.ParseParams{Source: `{f(a:1)}`})
		if err != nil {
			t.Fatal(err)
		}
		// Must not panic; an error is acceptable.
		_, _ = graphql.PlanQuery(&s, doc, "")
	})
}
