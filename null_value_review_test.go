package graphql_test

import (
	"testing"

	"github.com/IodeSystems/graphql-go/v3"
)

// Variable defaults are part of the plan-cache key: two operations that
// differ only in a variable default must not share a plan.
func TestPlanCache_VariableDefaultsInKey(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	for _, c := range []struct{ query, want string }{
		{`query($v: Int = 5) { f(a: $v) }`, `{"data":{"f":"{\"a\":5,\"b\":5}"}}`},
		{`query($v: Int = null) { f(a: $v) }`, `{"data":{"f":"{\"a\":null,\"b\":5}"}}`},
		{`query($v: Int = 6) { f(a: $v) }`, `{"data":{"f":"{\"a\":6,\"b\":5}"}}`},
	} {
		if got := runAllPaths(t, &schema, cache, c.query, nil); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.query, got, c.want)
		}
	}
	if pr := cache.Get(&schema, `query($v: Int = 1) { req(a: $v) }`, ""); len(pr.Errors) > 0 {
		t.Fatalf("= 1: %v", pr.Errors)
	}
	if pr := cache.Get(&schema, `query($v: Int = null) { req(a: $v) }`, ""); len(pr.Errors) == 0 {
		t.Fatal("= null into Int! reused the cached valid plan")
	}
}

// Directives are part of the plan-cache key: the planner evaluates a
// literal @skip/@include at plan time.
func TestPlanCache_DirectivesInKey(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	for _, c := range []struct{ query, want string }{
		{`{ f @skip(if: true) dn }`, `{"data":{"dn":"{\"a\":null}"}}`},
		{`{ f @skip(if: false) dn }`, `{"data":{"dn":"{\"a\":null}","f":"{\"b\":5}"}}`},
		{`{ ... @include(if: false) { f } dn }`, `{"data":{"dn":"{\"a\":null}"}}`},
		{`{ ... @include(if: true) { f } dn }`, `{"data":{"dn":"{\"a\":null}","f":"{\"b\":5}"}}`},
		{`{ ...F @include(if: false) dn } fragment F on Query { f }`, `{"data":{"dn":"{\"a\":null}"}}`},
		{`{ ...F @include(if: true) dn } fragment F on Query { f }`, `{"data":{"dn":"{\"a\":null}","f":"{\"b\":5}"}}`},
	} {
		if got := runAllPaths(t, &schema, cache, c.query, nil); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.query, got, c.want)
		}
	}
}

// An omitted non-null input field with a default is optional.
func TestInputObject_OmittedNonNullFieldWithDefault(t *testing.T) {
	schema := nullTestSchema(t)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	want := `{"data":{"h":"{\"in\":{\"x\":5}}"}}`
	if got := runAllPaths(t, &schema, cache, `{ h(in: {}) }`, nil); got != want {
		t.Errorf("literal: got %s, want %s", got, want)
	}
	vars := map[string]interface{}{"o": map[string]interface{}{}}
	if got := runAllPaths(t, &schema, cache, `query($o: InDef) { h(in: $o) }`, vars); got != want {
		t.Errorf("variable: got %s, want %s", got, want)
	}
	// Explicit null is still rejected.
	if res := graphql.Do(graphql.Params{Schema: schema, RequestString: `{ h(in: {x: null}) }`}); len(res.Errors) == 0 {
		t.Error("{x: null} into Int! accepted")
	}
}

// "In element #N" names the element's index (0-based, as graphql-js
// does and as the ported rule tests expect: ["one", 2] reports #1), not
// the message's position.
func TestListLiteral_ErrorNamesElementIndex(t *testing.T) {
	schema := nullTestSchema(t)
	res := graphql.Do(graphql.Params{Schema: schema, RequestString: `{ f(list: ["x", 1, "y"]) }`})
	want := `Argument "list" has invalid value ["x", 1, "y"].` + "\n" +
		`In element #0: Expected type "Int", found "x".` + "\n" +
		`In element #2: Expected type "Int", found "y".`
	if !hasErrorMessage(res.Errors, want) {
		t.Errorf("want %q, got %v", want, res.Errors)
	}
	res = graphql.Do(graphql.Params{Schema: schema, RequestString: `query($l: [Int]) { f(list: $l) }`,
		VariableValues: map[string]interface{}{"l": []interface{}{"x", 1, "y"}}})
	want = `Variable "$l" got invalid value ["x",1,"y"].` + "\n" +
		`In element #0: Expected type "Int", found "x".` + "\n" +
		`In element #2: Expected type "Int", found "y".`
	if !hasErrorMessage(res.Errors, want) {
		t.Errorf("want %q, got %v", want, res.Errors)
	}
}

// A null default cannot satisfy a non-null argument.
func TestExplicitNullDefault_NonNullArgStillRequired(t *testing.T) {
	schema := nullTestSchema(t)
	res := graphql.Do(graphql.Params{Schema: schema, RequestString: `{ nd }`})
	want := `Field "nd" argument "a" of type "Int!" is required but not provided.`
	if !hasErrorMessage(res.Errors, want) {
		t.Errorf("want %q, got %v", want, res.Errors)
	}
	res = graphql.Do(graphql.Params{Schema: schema, RequestString: `query($v: Int) { nd(a: $v) }`})
	want = `Variable "$v" of type "Int" used in position expecting type "Int!".`
	if !hasErrorMessage(res.Errors, want) {
		t.Errorf("want %q, got %v", want, res.Errors)
	}
}
