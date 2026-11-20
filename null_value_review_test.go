package graphql_test

import (
	"testing"

	"github.com/IodeSystems/graphql-go/v2"
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
