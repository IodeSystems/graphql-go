package graphql_test

import (
	"fmt"
	"testing"

	"github.com/IodeSystems/graphql-go/v3"
	"github.com/IodeSystems/graphql-go/v3/benchutil"
)

// BenchmarkPlanCache_GetHit_Literals isolates PlanCache.Get on a
// normalized cache hit: parse + normalize (literal extraction) + key
// lookup, no execution. 100 string-literal arguments per query; the
// literal values rotate so every call extracts fresh synth args.
func BenchmarkPlanCache_GetHit_Literals(b *testing.B) {
	schema := benchutil.WideArgedSchemaWithXFieldsAndYItems(100, 10)
	cache := graphql.NewPlanCache(graphql.PlanCacheOptions{Normalize: true})
	queries := make([]string, 64)
	for i := range queries {
		queries[i] = benchutil.WideArgedSchemaQueryWithLiteral(100, fmt.Sprintf("v-%d", i))
	}
	if pr := cache.Get(&schema, queries[0], ""); len(pr.Errors) > 0 {
		b.Fatal(pr.Errors)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if pr := cache.Get(&schema, queries[i%len(queries)], ""); pr.Plan == nil {
			b.Fatal(pr.Errors)
		}
	}
}
