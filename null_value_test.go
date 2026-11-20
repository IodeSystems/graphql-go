package graphql_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/IodeSystems/graphql-go/v3"
	"github.com/IodeSystems/graphql-go/v3/gqlerrors"
	"github.com/IodeSystems/graphql-go/v3/language/parser"
	"github.com/IodeSystems/graphql-go/v3/language/source"
)

// argsJSON echoes ResolveParams.Args as JSON, so a key present with a
// nil value ("a":null) is told apart from an absent key.
func argsJSON(p graphql.ResolveParams) (interface{}, error) {
	b, err := json.Marshal(p.Args)
	return string(b), err
}

func nullTestSchema(t *testing.T) graphql.Schema {
	t.Helper()
	in := graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "In",
		Fields: graphql.InputObjectConfigFieldMap{
			"x": &graphql.InputObjectFieldConfig{Type: graphql.Int},
			"y": &graphql.InputObjectFieldConfig{Type: graphql.Int, DefaultValue: 7},
			"n": &graphql.InputObjectFieldConfig{Type: graphql.Int, DefaultValue: graphql.ExplicitNull},
		},
	})
	inDef := graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "InDef",
		Fields: graphql.InputObjectConfigFieldMap{
			"x": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Int), DefaultValue: 5},
		},
	})
	inReq := graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "InReq",
		Fields: graphql.InputObjectConfigFieldMap{
			"z": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Int)},
		},
	})
	q := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"f": &graphql.Field{
				Type: graphql.String,
				Args: graphql.FieldConfigArgument{
					"a":    &graphql.ArgumentConfig{Type: graphql.Int},
					"b":    &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 5},
					"list": &graphql.ArgumentConfig{Type: graphql.NewList(graphql.Int)},
					"obj":  &graphql.ArgumentConfig{Type: in},
				},
				Resolve: argsJSON,
			},
			"dn": &graphql.Field{
				Type: graphql.String,
				Args: graphql.FieldConfigArgument{
					"a": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: graphql.ExplicitNull},
				},
				Resolve: argsJSON,
			},
			"req": &graphql.Field{
				Type:    graphql.String,
				Args:    graphql.FieldConfigArgument{"a": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.Int)}},
				Resolve: argsJSON,
			},
			"nlist": &graphql.Field{
				Type:    graphql.String,
				Args:    graphql.FieldConfigArgument{"a": &graphql.ArgumentConfig{Type: graphql.NewList(graphql.NewNonNull(graphql.Int))}},
				Resolve: argsJSON,
			},
			"h": &graphql.Field{
				Type:    graphql.String,
				Args:    graphql.FieldConfigArgument{"in": &graphql.ArgumentConfig{Type: inDef}},
				Resolve: argsJSON,
			},
			"nd": &graphql.Field{
				Type: graphql.String,
				Args: graphql.FieldConfigArgument{
					"a": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.Int), DefaultValue: graphql.ExplicitNull},
				},
				Resolve: argsJSON,
			},
			"g": &graphql.Field{
				Type:    graphql.String,
				Args:    graphql.FieldConfigArgument{"o": &graphql.ArgumentConfig{Type: inReq}},
				Resolve: argsJSON,
			},
		},
	})
	s, err := graphql.NewSchema(graphql.SchemaConfig{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// runAllPaths executes query through graphql.Do, ExecutePlan and
// ExecutePlanAppend (each planned via a normalizing PlanCache) and
// returns the JSON bodies. It fails the test when they disagree.
func runAllPaths(t *testing.T, schemaPtr *graphql.Schema, cache *graphql.PlanCache, query string, vars map[string]interface{}) string {
	t.Helper()
	schema := *schemaPtr
	doRes := graphql.Do(graphql.Params{Schema: schema, RequestString: query, VariableValues: vars})
	doBytes, err := json.Marshal(envelope(doRes))
	if err != nil {
		t.Fatal(err)
	}

	src := source.NewSource(&source.Source{Body: []byte(query)})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	if vr := graphql.ValidateDocument(&schema, doc, nil); !vr.IsValid {
		return string(doBytes)
	}

	pr := cache.Get(schemaPtr, query, "")
	if len(pr.Errors) > 0 {
		t.Fatalf("cache.Get %q: %v", query, pr.Errors)
	}
	args := map[string]interface{}{}
	for k, v := range vars {
		args[k] = v
	}
	for k, v := range pr.SynthArgs {
		args[k] = v
	}
	params := graphql.ExecuteParams{Schema: schema, Args: args, Context: context.Background()}
	planBytes, err := json.Marshal(envelope(graphql.ExecutePlan(pr.Plan, params)))
	if err != nil {
		t.Fatal(err)
	}
	appendBytes, _ := graphql.ExecutePlanAppend(pr.Plan, params, nil)

	var a, b, c interface{}
	_ = json.Unmarshal(doBytes, &a)
	_ = json.Unmarshal(planBytes, &b)
	_ = json.Unmarshal(appendBytes, &c)
	if !jsonEqual(a, b) || !jsonEqual(a, c) {
		t.Fatalf("%q: paths disagree:\n  Do:                %s\n  ExecutePlan:       %s\n  ExecutePlanAppend: %s",
			query, doBytes, planBytes, appendBytes)
	}
	return string(doBytes)
}

func jsonEqual(a, b interface{}) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestNullLiteral_Arguments(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	cases := []struct{ query, want string }{
		{`{ f(a: null) }`, `{"data":{"f":"{\"a\":null,\"b\":5}"}}`},
		{`{ f }`, `{"data":{"f":"{\"b\":5}"}}`},
		{`{ f(a: 1) }`, `{"data":{"f":"{\"a\":1,\"b\":5}"}}`},
		// null overrides a default.
		{`{ f(b: null) }`, `{"data":{"f":"{\"b\":null}"}}`},
		{`{ f(list: null) }`, `{"data":{"f":"{\"b\":5,\"list\":null}"}}`},
		{`{ f(list: [1, null]) }`, `{"data":{"f":"{\"b\":5,\"list\":[1,null]}"}}`},
		{`{ f(list: [null]) }`, `{"data":{"f":"{\"b\":5,\"list\":[null]}"}}`},
		{`{ f(obj: null) }`, `{"data":{"f":"{\"b\":5,\"obj\":null}"}}`},
		{`{ f(obj: {x: null}) }`, `{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"x\":null,\"y\":7}}"}}`},
		{`{ f(obj: {y: null}) }`, `{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"y\":null}}"}}`},
		{`{ f(obj: {}) }`, `{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"y\":7}}"}}`},
		{`{ dn }`, `{"data":{"dn":"{\"a\":null}"}}`},
		{`{ dn(a: 3) }`, `{"data":{"dn":"{\"a\":3}"}}`},
		{`{ g(o: {z: 1}) }`, `{"data":{"g":"{\"o\":{\"z\":1}}"}}`},
		// Mixed static and variable args on one field.
		{`query($v: Int) { f(a: null, list: [$v]) }`, `{"data":{"f":"{\"a\":null,\"b\":5,\"list\":[null]}"}}`},
	}
	for _, c := range cases {
		if got := runAllPaths(t, &schema, cache, c.query, nil); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.query, got, c.want)
		}
	}
}

func TestNullLiteral_NonNullPositionsRejected(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	cases := []struct{ query, msg string }{
		{`{ req(a: null) }`, `Argument "a" has invalid value null.` + "\n" + `Expected "Int!", found null.`},
		{`{ nlist(a: [1, null]) }`, `Argument "a" has invalid value [1, null].` + "\n" + `In element #1: Expected "Int!", found null.`},
		{`{ g(o: {z: null}) }`, `Argument "o" has invalid value {z: null}.` + "\n" + `In field "z": Expected "Int!", found null.`},
		{`query($v: Int! = null) { f(a: $v) }`, `Variable "$v" has invalid default value: null.` + "\n" + `Expected "Int!", found null.`},
	}
	for _, c := range cases {
		res := graphql.Do(graphql.Params{Schema: schema, RequestString: c.query})
		if !hasErrorMessage(res.Errors, c.msg) {
			t.Errorf("Do %s: want error %q, got %v", c.query, c.msg, res.Errors)
		}
		// The cache must reject the literal at validation, not extract
		// it into a synthetic variable that slips past validation.
		pr := cache.Get(&schema, c.query, "")
		if !hasErrorMessage(pr.Errors, c.msg) {
			t.Errorf("cache.Get %s: want error %q, got %v", c.query, c.msg, pr.Errors)
		}
	}
}

func hasErrorMessage(errs []gqlerrors.FormattedError, msg string) bool {
	for _, e := range errs {
		if e.Message == msg {
			return true
		}
	}
	return false
}

func TestNullLiteral_VariableDefaults(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})

	// A `null` default does not make the variable effectively non-null.
	res := graphql.Do(graphql.Params{Schema: schema, RequestString: `query($v: Int = null) { req(a: $v) }`})
	want := `Variable "$v" of type "Int" used in position expecting type "Int!".`
	if !hasErrorMessage(res.Errors, want) {
		t.Errorf("want %q, got %v", want, res.Errors)
	}
	// A non-null default still does.
	got := runAllPaths(t, &schema, cache, `query($v: Int = 2) { req(a: $v) }`, nil)
	if got != `{"data":{"req":"{\"a\":2}"}}` {
		t.Errorf("got %s", got)
	}
	got = runAllPaths(t, &schema, cache, `query($v: In = {x: null}) { f(obj: $v) }`, nil)
	if got != `{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"x\":null,\"y\":7}}"}}` {
		t.Errorf("got %s", got)
	}
}

// The normalizing cache must not share a plan between `f(a: null)` and
// `f`: the first passes a: null, the second leaves a out.
func TestNullLiteral_PlanCacheKeepsNullApartFromAbsent(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	withNull := cache.Get(&schema, `{ f(a: null) }`, "")
	absent := cache.Get(&schema, `{ f }`, "")
	withOne := cache.Get(&schema, `{ f(a: 1) }`, "")
	if withNull.Plan == nil || absent.Plan == nil || withOne.Plan == nil {
		t.Fatalf("errors: %v %v %v", withNull.Errors, absent.Errors, withOne.Errors)
	}
	if withNull.Plan == absent.Plan {
		t.Fatal("f(a: null) and f share a plan")
	}
	if withNull.Plan == withOne.Plan {
		t.Fatal("f(a: null) and f(a: 1) share a plan")
	}
	// Lists holding null still collapse with other list literals.
	l1 := cache.Get(&schema, `{ f(list: [1, null]) }`, "")
	l2 := cache.Get(&schema, `{ f(list: [3, 4]) }`, "")
	if l1.Plan == nil || l1.Plan != l2.Plan {
		t.Fatalf("list literals with and without null should share a plan: %v %v", l1.Errors, l2.Errors)
	}
}

func TestNullLiteral_IntrospectionDefaultValue(t *testing.T) {
	schema := nullTestSchema(t)
	res := graphql.Do(graphql.Params{Schema: schema, RequestString: `{
		__type(name: "Query") { fields { name args { name defaultValue } } }
		in: __type(name: "In") { inputFields { name defaultValue } }
	}`})
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	b, _ := json.Marshal(res.Data)
	for _, want := range []string{
		`{"args":[{"defaultValue":"null","name":"a"}],"name":"dn"}`,
		`{"defaultValue":"5","name":"b"}`,
		`{"defaultValue":null,"name":"a"}`,
		`{"defaultValue":"null","name":"n"}`,
		`{"defaultValue":"7","name":"y"}`,
		`{"defaultValue":null,"name":"x"}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("introspection lacks %s\n%s", want, b)
		}
	}
}

// A variable provided as null reaches the resolver as null; a variable
// not provided leaves the argument absent (spec CoerceVariableValues,
// CoerceArgumentValues).
func TestNullVariable_ProvidedVersusAbsent(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	null := map[string]interface{}{"v": nil}
	cases := []struct {
		query string
		vars  map[string]interface{}
		want  string
	}{
		{`query($v: Int) { f(a: $v) }`, nil, `{"data":{"f":"{\"b\":5}"}}`},
		{`query($v: Int) { f(a: $v) }`, null, `{"data":{"f":"{\"a\":null,\"b\":5}"}}`},
		// Not provided: the argument default applies. Null: it does not.
		{`query($v: Int) { f(b: $v) }`, nil, `{"data":{"f":"{\"b\":5}"}}`},
		{`query($v: Int) { f(b: $v) }`, null, `{"data":{"f":"{\"b\":null}"}}`},
		// Variable defaults apply only when the variable is not provided.
		{`query($v: Int = null) { f(a: $v) }`, nil, `{"data":{"f":"{\"a\":null,\"b\":5}"}}`},
		{`query($v: Int = 3) { f(a: $v) }`, nil, `{"data":{"f":"{\"a\":3,\"b\":5}"}}`},
		{`query($v: Int = 3) { f(a: $v) }`, null, `{"data":{"f":"{\"a\":null,\"b\":5}"}}`},
		// Inside an input object literal.
		{`query($v: Int) { f(obj: {y: $v}) }`, nil, `{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"y\":7}}"}}`},
		{`query($v: Int) { f(obj: {y: $v}) }`, null, `{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"y\":null}}"}}`},
		// Inside an input object variable.
		{`query($o: In) { f(obj: $o) }`, map[string]interface{}{"o": map[string]interface{}{"y": nil}},
			`{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"y\":null}}"}}`},
		{`query($o: In) { f(obj: $o) }`, map[string]interface{}{"o": map[string]interface{}{}},
			`{"data":{"f":"{\"b\":5,\"obj\":{\"n\":null,\"y\":7}}"}}`},
	}
	for _, c := range cases {
		if got := runAllPaths(t, &schema, cache, c.query, c.vars); got != c.want {
			t.Errorf("%s %v\n  got  %s\n  want %s", c.query, c.vars, got, c.want)
		}
	}
}
