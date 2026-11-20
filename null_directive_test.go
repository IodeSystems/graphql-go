package graphql_test

import (
	"testing"

	"github.com/IodeSystems/graphql-go/v3"
)

func TestNullVariable_SkipIncludeIf(t *testing.T) {
	schema := nullTestSchema(t)
	caches := map[string]*graphql.PlanCache{
		"normalized": graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true}),
		"plain":      graphql.NewPlanCache(graphql.PlanCacheOptions{}),
	}
	const msg = `Argument \"if\" of non-null type \"Boolean!\" must not be null.`
	null := map[string]interface{}{"v": nil}
	cases := []struct {
		name, query string
		vars        map[string]interface{}
		want        string
	}{
		{"skip field", `query($v: Boolean = true){ f @skip(if: $v) }`, null,
			`{"data":null,"errors":[{"message":"` + msg + `","locations":[{"line":1,"column":40}]}]}`},
		{"include field", `query($v: Boolean = true){ f @include(if: $v) }`, null,
			`{"data":null,"errors":[{"message":"` + msg + `","locations":[{"line":1,"column":43}]}]}`},
		{"skip inline fragment", `query($v: Boolean = true){ ... @skip(if: $v) { f } }`, null,
			`{"data":null,"errors":[{"message":"` + msg + `","locations":[{"line":1,"column":42}]}]}`},
		{"include inline fragment", `query($v: Boolean = true){ ... @include(if: $v) { f } }`, null,
			`{"data":null,"errors":[{"message":"` + msg + `","locations":[{"line":1,"column":45}]}]}`},
		{"skip spread", `query($v: Boolean = true){ ...F @skip(if: $v) } fragment F on Query { f }`, null,
			`{"data":null,"errors":[{"message":"` + msg + `","locations":[{"line":1,"column":43}]}]}`},
		{"include spread", `query($v: Boolean = true){ ...F @include(if: $v) } fragment F on Query { f }`, null,
			`{"data":null,"errors":[{"message":"` + msg + `","locations":[{"line":1,"column":46}]}]}`},
		// Omitted: the variable default applies.
		{"skip omitted", `query($v: Boolean = true){ f @skip(if: $v) }`, nil, `{"data":{}}`},
		{"include omitted", `query($v: Boolean = true){ f @include(if: $v) }`, nil, `{"data":{"f":"{\"b\":5}"}}`},
		{"skip false", `query($v: Boolean = true){ f @skip(if: $v) }`, map[string]interface{}{"v": false}, `{"data":{"f":"{\"b\":5}"}}`},
		{"skip true", `query($v: Boolean = true){ f @skip(if: $v) }`, map[string]interface{}{"v": true}, `{"data":{}}`},
		{"include true", `query($v: Boolean = true){ f @include(if: $v) }`, map[string]interface{}{"v": true}, `{"data":{"f":"{\"b\":5}"}}`},
		{"include false", `query($v: Boolean = true){ f @include(if: $v) }`, map[string]interface{}{"v": false}, `{"data":{}}`},
	}
	for _, c := range cases {
		for cn, cache := range caches {
			t.Run(c.name+"/"+cn, func(t *testing.T) {
				// Twice: the error must fire per request, not once per plan.
				for i := 0; i < 2; i++ {
					got := runAllPaths(t, &schema, cache, c.query, c.vars)
					if got != c.want {
						t.Fatalf("got  %s\nwant %s", got, c.want)
					}
				}
				// Same plan, other variables: a cached plan must not
				// remember the null.
				if c.vars != nil && c.vars["v"] == nil {
					got := runAllPaths(t, &schema, cache, c.query, map[string]interface{}{"v": false})
					if got == c.want {
						t.Fatalf("plan cached the error: %s", got)
					}
				}
			})
		}
	}
}

func TestNullLiteral_SkipIncludeIfRejected(t *testing.T) {
	schema := nullTestSchema(t)
	for _, q := range []string{`{ f @skip(if: null) }`, `{ f @include(if: null) }`} {
		res := graphql.Do(graphql.Params{Schema: schema, RequestString: q})
		if len(res.Errors) == 0 {
			t.Errorf("%s: want validation error", q)
		}
	}
}
