package graphql_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/IodeSystems/graphql-go/v3"
	"github.com/IodeSystems/graphql-go/v3/language/parser"
)

// AllowLossyInputCoercion restores the v2.1.1 variable coercion of Int,
// Float and ID for values the default coercion rejects. Literals and
// PlanCache synthetic variables stay strict.

// runLossy runs a request through graphql.Do and PlanQuery +
// ExecutePlanAppend with the flag set as given, fails when they
// disagree, and returns the resolver output and the first error message.
func runLossy(t *testing.T, schema graphql.Schema, query string, vars map[string]interface{}, lossy bool) (string, string) {
	t.Helper()
	res := graphql.Do(graphql.Params{Schema: schema, RequestString: query, VariableValues: vars, AllowLossyInputCoercion: lossy})
	got, msg := "", ""
	if len(res.Errors) > 0 {
		msg = res.Errors[0].Message
	} else {
		for _, v := range res.Data.(map[string]interface{}) {
			got, _ = v.(string)
		}
	}

	doc, err := parser.Parse(parser.ParseParams{Source: query})
	if err != nil {
		t.Fatal(err)
	}
	if vr := graphql.ValidateDocument(&schema, doc, nil); !vr.IsValid {
		if vr.Errors[0].Message != msg {
			t.Errorf("%s: Do error %q, validation error %q", query, msg, vr.Errors[0].Message)
		}
		return got, msg
	}
	plan, err := graphql.PlanQuery(&schema, doc, "")
	if err != nil {
		t.Fatal(err)
	}
	out, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc, Args: vars, AllowLossyInputCoercion: lossy}, nil)
	appendGot, appendMsg := "", ""
	if len(specErrs) > 0 {
		appendMsg = specErrs[0].Message
	} else {
		var dec struct {
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(out, &dec); err != nil {
			t.Fatalf("decode %s: %v", out, err)
		}
		for _, v := range dec.Data {
			appendGot, _ = v.(string)
		}
	}
	if got != appendGot || msg != appendMsg {
		t.Errorf("%s %v lossy=%v: Do=(%q, %q), ExecutePlanAppend=(%q, %q)", query, vars, lossy, got, msg, appendGot, appendMsg)
	}
	return got, msg
}

func TestAllowLossyInputCoercion_Variables(t *testing.T) {
	schema := inputCoercionSchema(t)
	cases := []struct {
		field, varType string
		raw            string // JSON
		lossy          string // resolver output with the flag; "" means still rejected
	}{
		// Int: v2.1.1 truncates, maps booleans, parses strings with
		// strconv.ParseFloat, and rejects out-of-range values.
		{"int", "Int", `2.5`, "int:2"},
		{"int", "Int", `-2.5`, "int:-2"},
		{"int", "Int", `true`, "int:1"},
		{"int", "Int", `false`, "int:0"},
		{"int", "Int", `"2.5"`, "int:2"},
		{"int", "Int", `"+2"`, "int:2"},
		{"int", "Int", `"02"`, "int:2"},
		{"int", "Int", `"abc"`, ""},
		{"int", "Int", `" 2"`, ""},
		{"int", "Int", `3000000000`, ""},
		{"int", "Int", `"3000000000"`, ""},
		// Float: booleans and any strconv.ParseFloat string.
		{"float", "Float", `true`, "float64:1"},
		{"float", "Float", `false`, "float64:0"},
		{"float", "Float", `"+2.5"`, "float64:2.5"},
		{"float", "Float", `"Inf"`, "float64:+Inf"},
		{"float", "Float", `"NaN"`, ""},
		{"float", "Float", `"abc"`, ""},
		// ID: any value formatted with %v.
		{"id", "ID", `2.5`, "string:2.5"},
		{"id", "ID", `true`, "string:true"},
		{"id", "ID", `{"a":1}`, "string:map[a:1]"},
		// Nested positions use the same coercion.
		{"list", "[Int]", `[1, 2.5]`, "int:2"},
		{"obj", "CoercionInput", `{"i": true}`, "int:1"},
	}
	for _, c := range cases {
		t.Run(c.varType+"/"+c.raw, func(t *testing.T) {
			var vars map[string]interface{}
			if err := json.Unmarshal([]byte(`{"v":`+c.raw+`}`), &vars); err != nil {
				t.Fatal(err)
			}
			query := fmt.Sprintf(`query($v: %s) { %s(a: $v) }`, c.varType, c.field)

			got, msg := runLossy(t, schema, query, vars, false)
			if got != "" || !strings.HasPrefix(msg, `Variable "$v" got invalid value `) || !strings.Contains(msg, `Expected type "`) {
				t.Errorf("flag off: want the invalid-value error, got (%q, %q)", got, msg)
			}

			got, msg = runLossy(t, schema, query, vars, true)
			if c.lossy == "" {
				if got != "" || !strings.HasPrefix(msg, `Variable "$v" got invalid value `) {
					t.Errorf("flag on: want the invalid-value error, got (%q, %q)", got, msg)
				}
			} else if got != c.lossy || msg != "" {
				t.Errorf("flag on: want %q, got (%q, %q)", c.lossy, got, msg)
			}
		})
	}
}

// The flag changes nothing for values the default coercion accepts,
// for literals, or for custom scalars.
func TestAllowLossyInputCoercion_Unaffected(t *testing.T) {
	schema := inputCoercionSchema(t)
	for _, c := range []struct{ query, raw, want string }{
		{`query($v: Int) { int(a: $v) }`, `"2.0"`, "int:2"},
		{`query($v: Int) { int(a: $v) }`, `1e3`, "int:1000"},
		{`query($v: Float) { float(a: $v) }`, `"1e3"`, "float64:1000"},
		{`query($v: ID) { id(a: $v) }`, `2.0`, "string:2"},
	} {
		var vars map[string]interface{}
		if err := json.Unmarshal([]byte(`{"v":`+c.raw+`}`), &vars); err != nil {
			t.Fatal(err)
		}
		for _, lossy := range []bool{false, true} {
			if got, msg := runLossy(t, schema, c.query, vars, lossy); got != c.want {
				t.Errorf("%s %s lossy=%v: want %q, got (%q, %q)", c.query, c.raw, lossy, c.want, got, msg)
			}
		}
	}
	for _, query := range []string{`{ int(a: 2.5) }`, `{ int(a: true) }`, `{ float(a: true) }`, `{ id(a: 2.5) }`, `query($v: Int = 2.5) { int(a: $v) }`} {
		if got, msg := runLossy(t, schema, query, nil, true); got != "" || msg == "" {
			t.Errorf("%s: literal must stay strict with the flag, got (%q, %q)", query, got, msg)
		}
	}
}

func TestAllowLossyInputCoercion_CustomScalarRaw(t *testing.T) {
	var seen interface{}
	custom := graphql.NewScalar(graphql.ScalarConfig{
		Name:      "Custom",
		Serialize: func(v interface{}) interface{} { return "ok" },
		ParseValue: func(v interface{}) interface{} {
			seen = v
			return v
		},
		ParseLiteral: graphql.Int.ParseLiteral,
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"c": &graphql.Field{
					Type:    custom,
					Args:    graphql.FieldConfigArgument{"a": &graphql.ArgumentConfig{Type: custom}},
					Resolve: func(p graphql.ResolveParams) (interface{}, error) { return "ok", nil },
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, lossy := range []bool{false, true} {
		seen = nil
		res := graphql.Do(graphql.Params{Schema: schema, RequestString: `query($v: Custom) { c(a: $v) }`, VariableValues: map[string]interface{}{"v": 2.5}, AllowLossyInputCoercion: lossy})
		if len(res.Errors) != 0 || seen != 2.5 {
			t.Errorf("lossy=%v: custom ParseValue must get the raw value, got %#v (errors %v)", lossy, seen, res.Errors)
		}
	}
}

// PlanCache normalization hoists literals into synthetic variables
// (__pcv0, ...). Those stay strict with the flag on, so a caller that
// lets request Args override SynthArgs cannot smuggle a lossy value in
// through a literal's position, and an invalid literal is never hoisted.
func TestAllowLossyInputCoercion_HoistedLiteralStaysStrict(t *testing.T) {
	schema := inputCoercionSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})

	pr := cache.Get(&schema, `{ int(a: 2) }`, "")
	if len(pr.Errors) != 0 || pr.SynthArgs["__pcv0"] != 2 {
		t.Fatalf("want literal hoisted to __pcv0=2, got %+v", pr)
	}
	exec := func(args map[string]interface{}) *graphql.Result {
		return graphql.ExecutePlan(pr.Plan, graphql.ExecuteParams{Schema: schema, Args: args, AllowLossyInputCoercion: true})
	}
	if res := exec(pr.SynthArgs); len(res.Errors) != 0 || res.Data.(map[string]interface{})["int"] != "int:2" {
		t.Fatalf("hoisted literal: want int:2, got %+v", res)
	}
	res := exec(map[string]interface{}{"__pcv0": 2.5})
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0].Message, `Variable "$__pcv0" got invalid value 2.5`) {
		t.Fatalf("synthetic variable must stay strict with the flag on, got %+v", res)
	}

	// An invalid literal is not hoisted; validation rejects it.
	if pr := cache.Get(&schema, `{ int(a: 2.5) }`, ""); len(pr.Errors) == 0 || pr.Plan != nil {
		t.Fatalf("invalid literal must fail validation, got %+v", pr)
	}
}

// One cached plan serves flag-on and flag-off requests: variables are
// coerced per execution, not at plan time.
func TestAllowLossyInputCoercion_PlanCacheSharedPlan(t *testing.T) {
	schema := inputCoercionSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	const query = `query($v: Int) { int(a: $v) }`
	vars := map[string]interface{}{"v": 2.5}
	for i, lossy := range []bool{true, false, true, false} {
		pr := cache.Get(&schema, query, "")
		if len(pr.Errors) != 0 {
			t.Fatal(pr.Errors)
		}
		params := graphql.ExecuteParams{Schema: schema, Args: vars, AllowLossyInputCoercion: lossy}
		res := graphql.ExecutePlan(pr.Plan, params)
		out, specErrs := graphql.ExecutePlanAppend(pr.Plan, params, nil)
		if lossy {
			if len(res.Errors) != 0 || res.Data.(map[string]interface{})["int"] != "int:2" {
				t.Errorf("run %d lossy: ExecutePlan want int:2, got %+v", i, res)
			}
			if len(specErrs) != 0 || string(out) != `{"data":{"int":"int:2"}}` {
				t.Errorf("run %d lossy: ExecutePlanAppend want int:2, got %s %v", i, out, specErrs)
			}
		} else {
			if len(res.Errors) == 0 {
				t.Errorf("run %d strict: ExecutePlan want error, got %+v", i, res)
			}
			if len(specErrs) == 0 {
				t.Errorf("run %d strict: ExecutePlanAppend want error, got %s", i, out)
			}
		}
	}
	if hits, misses := cache.HitsMisses(); hits != 3 || misses != 1 {
		t.Errorf("want 3 hits and 1 miss, got %d / %d", hits, misses)
	}
}
