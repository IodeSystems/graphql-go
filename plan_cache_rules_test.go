package graphql_test

import (
	"testing"

	"github.com/IodeSystems/graphql-go/v2"
	"github.com/IodeSystems/graphql-go/v2/gqlerrors"
	"github.com/IodeSystems/graphql-go/v2/language/ast"
	"github.com/IodeSystems/graphql-go/v2/language/kinds"
	"github.com/IodeSystems/graphql-go/v2/language/visitor"
)

func rulesTestSchema(t *testing.T) *graphql.Schema {
	t.Helper()
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: graphql.Fields{
			"hello": &graphql.Field{Type: graphql.String},
		}}),
		Mutation: graphql.NewObject(graphql.ObjectConfig{Name: "Mutation", Fields: graphql.Fields{
			"poke": &graphql.Field{Type: graphql.String},
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &schema
}

// noFieldNamed rejects any selection of the named field, and counts how
// often it runs.
func noFieldNamed(name string, runs *int) graphql.ValidationRuleFn {
	return func(ctx *graphql.ValidationContext) *graphql.ValidationRuleInstance {
		*runs++
		return &graphql.ValidationRuleInstance{VisitorOpts: &visitor.VisitorOptions{
			KindFuncMap: map[string]visitor.NamedVisitFuncs{
				kinds.Field: {Kind: func(p visitor.VisitFuncParams) (string, interface{}) {
					if f, ok := p.Node.(*ast.Field); ok && f.Name.Value == name {
						ctx.ReportError(gqlerrors.NewError("field "+name+" is not allowed", []ast.Node{f}, "", nil, []int{}, nil))
					}
					return visitor.ActionNoChange, nil
				}},
			},
		}}
	}
}

func TestPlanCache_RulesRunWithSpecifiedRulesAndAreCached(t *testing.T) {
	schema := rulesTestSchema(t)
	var runs int
	for _, normalize := range []bool{false, true} {
		runs = 0
		c := graphql.NewPlanCache(graphql.PlanCacheOptions{
			Normalize: normalize,
			Rules:     []graphql.ValidationRuleFn{noFieldNamed("hello", &runs)},
		})
		for i := 0; i < 3; i++ {
			pr := c.Get(schema, `{ hello }`, "")
			if pr.Plan != nil || len(pr.Errors) != 1 || pr.Errors[0].Message != "field hello is not allowed" {
				t.Fatalf("normalize=%v: got %+v", normalize, pr)
			}
		}
		if runs != 1 {
			t.Errorf("normalize=%v: rule ran %d times for one distinct query, want 1", normalize, runs)
		}
		// Spec rules still apply alongside.
		if pr := c.Get(schema, `{ nope }`, ""); pr.Plan != nil {
			t.Errorf("normalize=%v: unknown field validated", normalize)
		}
	}
}

func TestPlanCache_RulesApplyToOverCapQueries(t *testing.T) {
	schema := rulesTestSchema(t)
	var runs int
	c := graphql.NewPlanCache(graphql.PlanCacheOptions{
		MaxQueryBytes: 4,
		Rules:         []graphql.ValidationRuleFn{noFieldNamed("hello", &runs)},
	})
	if pr := c.Get(schema, `{ hello }`, ""); pr.Plan != nil {
		t.Error("an uncached (over-cap) query skipped the extra rules")
	}
}

func TestPlan_IsMutation(t *testing.T) {
	schema := rulesTestSchema(t)
	c := graphql.NewPlanCache(graphql.PlanCacheOptions{})
	if pr := c.Get(schema, `{ hello }`, ""); pr.Plan == nil || pr.Plan.IsMutation() {
		t.Errorf("query: %+v", pr)
	}
	if pr := c.Get(schema, `mutation { poke }`, ""); pr.Plan == nil || !pr.Plan.IsMutation() {
		t.Errorf("mutation: %+v", pr)
	}
}
