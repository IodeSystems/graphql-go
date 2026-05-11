package graphql_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
)

// runParity runs both ExecutePlan and ExecutePlanAppend and asserts
// they produce equivalent JSON-decoded responses. Map iteration
// order makes byte-equality unreliable for the marshaled-Result
// path, so we compare canonical decoded form.
func runParity(t *testing.T, schema graphql.Schema, query, opName string, args map[string]interface{}, root interface{}) {
	t.Helper()

	src := source.NewSource(&source.Source{Body: []byte(query), Name: "test"})
	doc, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	plan, err := graphql.PlanQuery(&schema, doc, opName)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	mapResult := graphql.ExecutePlan(plan, graphql.ExecuteParams{
		Schema:        schema,
		AST:           doc,
		OperationName: opName,
		Args:          args,
		Root:          root,
		Context:       context.Background(),
	})

	mapBytes, err := json.Marshal(envelope(mapResult))
	if err != nil {
		t.Fatalf("marshal map result: %v", err)
	}

	appendBytes, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
		Schema:        schema,
		AST:           doc,
		OperationName: opName,
		Args:          args,
		Root:          root,
		Context:       context.Background(),
	}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("append spec errors: %v", specErrs)
	}

	lazyBytes, lazySpecErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{
		Schema:        schema,
		AST:           doc,
		OperationName: opName,
		Args:          args,
		Root:          root,
		Context:       context.Background(),
		LazyPath:      true,
	}, nil)
	if len(lazySpecErrs) > 0 {
		t.Fatalf("append (lazyPath) spec errors: %v", lazySpecErrs)
	}

	var mapDecoded, appendDecoded, lazyDecoded interface{}
	if err := json.Unmarshal(mapBytes, &mapDecoded); err != nil {
		t.Fatalf("decode map: %v\nbytes: %s", err, mapBytes)
	}
	if err := json.Unmarshal(appendBytes, &appendDecoded); err != nil {
		t.Fatalf("decode append: %v\nbytes: %s", err, appendBytes)
	}
	if err := json.Unmarshal(lazyBytes, &lazyDecoded); err != nil {
		t.Fatalf("decode append (lazyPath): %v\nbytes: %s", err, lazyBytes)
	}
	if !reflect.DeepEqual(mapDecoded, appendDecoded) {
		t.Fatalf("parity mismatch:\n  ExecutePlan:       %s\n  ExecutePlanAppend: %s", mapBytes, appendBytes)
	}
	if !reflect.DeepEqual(appendDecoded, lazyDecoded) {
		t.Fatalf("LazyPath parity mismatch:\n  ExecutePlanAppend:           %s\n  ExecutePlanAppend(LazyPath):  %s", appendBytes, lazyBytes)
	}
}

// envelope wraps a *graphql.Result so it marshals to the same shape
// ExecutePlanAppend emits: `{"data":...,"errors":[...]}` with errors
// omitted when empty.
func envelope(r *graphql.Result) map[string]interface{} {
	out := map[string]interface{}{"data": r.Data}
	if len(r.Errors) > 0 {
		out["errors"] = r.Errors
	}
	return out
}

func TestAppendParity_Scalars(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"i": &graphql.Field{Type: graphql.Int, Resolve: func(graphql.ResolveParams) (interface{}, error) { return 42, nil }},
				"f": &graphql.Field{Type: graphql.Float, Resolve: func(graphql.ResolveParams) (interface{}, error) { return 3.14, nil }},
				"s": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "hi \"world\"\n<tag>&", nil }},
				"b": &graphql.Field{Type: graphql.Boolean, Resolve: func(graphql.ResolveParams) (interface{}, error) { return true, nil }},
				"id": &graphql.Field{Type: graphql.ID, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "abc-123", nil }},
				"sNull": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return nil, nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ i f s b id sNull }`, "", nil, nil)
}

func TestAppendParity_NestedObjects(t *testing.T) {
	address := graphql.NewObject(graphql.ObjectConfig{
		Name: "Address",
		Fields: graphql.Fields{
			"city": &graphql.Field{Type: graphql.String},
			"zip":  &graphql.Field{Type: graphql.String},
		},
	})
	person := graphql.NewObject(graphql.ObjectConfig{
		Name: "Person",
		Fields: graphql.Fields{
			"name":    &graphql.Field{Type: graphql.String},
			"address": &graphql.Field{Type: address},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"me": &graphql.Field{
					Type: person,
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return map[string]interface{}{
							"name": "Ada",
							"address": map[string]interface{}{
								"city": "Westminster",
								"zip":  "SW1",
							},
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ me { name address { city zip } } }`, "", nil, nil)
}

func TestAppendParity_Lists(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"nums": &graphql.Field{
					Type:    graphql.NewList(graphql.Int),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []int{1, 2, 3, 4, 5}, nil },
				},
				"strs": &graphql.Field{
					Type:    graphql.NewList(graphql.String),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []string{"a", "b", "c"}, nil },
				},
				"empty": &graphql.Field{
					Type:    graphql.NewList(graphql.Int),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []int{}, nil },
				},
				"nullableItems": &graphql.Field{
					Type:    graphql.NewList(graphql.Int),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []interface{}{1, nil, 3}, nil },
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ nums strs empty nullableItems }`, "", nil, nil)
}

func TestAppendParity_NonNullBubble(t *testing.T) {
	// Resolver returns nil for a NonNull field — error bubbles to the
	// nullable parent (`me`), which becomes null and an error is
	// recorded.
	person := graphql.NewObject(graphql.ObjectConfig{
		Name: "Person",
		Fields: graphql.Fields{
			"name":     &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"nickname": &graphql.Field{Type: graphql.String},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"me": &graphql.Field{
					Type: person,
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return map[string]interface{}{
							"name":     nil,
							"nickname": "x",
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ me { name nickname } }`, "", nil, nil)
}

func TestAppendParity_NonNullListItem(t *testing.T) {
	// List-of-NonNull item null → list itself nulls, bubbles to the
	// nullable list field.
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"nums": &graphql.Field{
					Type:    graphql.NewList(graphql.NewNonNull(graphql.Int)),
					Resolve: func(graphql.ResolveParams) (interface{}, error) { return []interface{}{1, 2, nil, 4}, nil },
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ nums }`, "", nil, nil)
}

func TestAppendParity_ResolverError(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"ok":  &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "ok", nil }},
				"bad": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return nil, errors.New("boom") }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ ok bad }`, "", nil, nil)
}

func TestAppendParity_Directives(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"a": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "A", nil }},
				"b": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "B", nil }},
				"c": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "C", nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ a b @skip(if: true) c @include(if: false) }`, "", nil, nil)
	runParity(t, schema, `query Q($s: Boolean!) { a b @skip(if: $s) }`, "Q", map[string]interface{}{"s": true}, nil)
	runParity(t, schema, `query Q($s: Boolean!) { a b @skip(if: $s) }`, "Q", map[string]interface{}{"s": false}, nil)
}

func TestAppendParity_Enum(t *testing.T) {
	colorEnum := graphql.NewEnum(graphql.EnumConfig{
		Name: "Color",
		Values: graphql.EnumValueConfigMap{
			"RED":   &graphql.EnumValueConfig{Value: "r"},
			"GREEN": &graphql.EnumValueConfig{Value: "g"},
			"BLUE":  &graphql.EnumValueConfig{Value: "b"},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"c":  &graphql.Field{Type: colorEnum, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "g", nil }},
				"cn": &graphql.Field{Type: colorEnum, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "unknown", nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ c cn }`, "", nil, nil)
}

func TestAppendParity_Interface(t *testing.T) {
	namedIface := graphql.NewInterface(graphql.InterfaceConfig{
		Name: "Named",
		Fields: graphql.Fields{
			"name": &graphql.Field{Type: graphql.String},
		},
		ResolveType: func(p graphql.ResolveTypeParams) *graphql.Object {
			m := p.Value.(map[string]interface{})
			if m["__t"] == "dog" {
				return p.Info.Schema.Type("Dog").(*graphql.Object)
			}
			return p.Info.Schema.Type("Cat").(*graphql.Object)
		},
	})
	dog := graphql.NewObject(graphql.ObjectConfig{
		Name:       "Dog",
		Interfaces: []*graphql.Interface{namedIface},
		Fields: graphql.Fields{
			"name":  &graphql.Field{Type: graphql.String},
			"barks": &graphql.Field{Type: graphql.Boolean},
		},
	})
	cat := graphql.NewObject(graphql.ObjectConfig{
		Name:       "Cat",
		Interfaces: []*graphql.Interface{namedIface},
		Fields: graphql.Fields{
			"name":  &graphql.Field{Type: graphql.String},
			"meows": &graphql.Field{Type: graphql.Boolean},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"pets": &graphql.Field{
					Type: graphql.NewList(namedIface),
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return []interface{}{
							map[string]interface{}{"__t": "dog", "name": "Rex", "barks": true},
							map[string]interface{}{"__t": "cat", "name": "Mia", "meows": true},
						}, nil
					},
				},
			},
		}),
		Types: []graphql.Type{dog, cat},
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ pets { name ... on Dog { barks } ... on Cat { meows } } }`, "", nil, nil)
}

func TestAppendParity_Variables(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"echo": &graphql.Field{
					Type: graphql.String,
					Args: graphql.FieldConfigArgument{
						"v": &graphql.ArgumentConfig{Type: graphql.String},
					},
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return p.Args["v"], nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `query Q($v: String) { echo(v: $v) }`, "Q", map[string]interface{}{"v": "hello"}, nil)
}

// TestAppendByteShape spot-checks the literal output bytes for a
// trivial query — guards against regressions in the JSON envelope
// layout that JSON-decode parity wouldn't catch (e.g. trailing
// comma, double-quoted keys).
func TestAppendByteShape(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"a": &graphql.Field{Type: graphql.String, Resolve: func(graphql.ResolveParams) (interface{}, error) { return "1", nil }},
				"b": &graphql.Field{Type: graphql.Int, Resolve: func(graphql.ResolveParams) (interface{}, error) { return 2, nil }},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ a b }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")
	got, specErrs := graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if len(specErrs) > 0 {
		t.Fatalf("spec errors: %v", specErrs)
	}
	want := `{"data":{"a":"1","b":2}}`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if !strings.HasPrefix(string(got), `{"data":`) || !strings.HasSuffix(string(got), `}`) {
		t.Fatalf("envelope shape wrong: %s", got)
	}
}

// TestAppendLazyPath_InfoPathIsNil confirms the contract advertised
// by ExecuteParams.LazyPath: every resolver sees info.Path == nil.
// Default mode (LazyPath=false) must still populate info.Path.
func TestAppendLazyPath_InfoPathIsNil(t *testing.T) {
	var sawPath, sawNil bool
	probe := func(p graphql.ResolveParams) (interface{}, error) {
		if p.Info.Path == nil {
			sawNil = true
		} else {
			sawPath = true
		}
		return "ok", nil
	}
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"v": &graphql.Field{Type: graphql.String, Resolve: probe},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := source.NewSource(&source.Source{Body: []byte(`{ v }`), Name: "t"})
	doc, _ := parser.Parse(parser.ParseParams{Source: src})
	plan, _ := graphql.PlanQuery(&schema, doc, "")

	sawPath, sawNil = false, false
	_, _ = graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc}, nil)
	if !sawPath || sawNil {
		t.Fatalf("default mode: want non-nil info.Path; sawPath=%v sawNil=%v", sawPath, sawNil)
	}

	sawPath, sawNil = false, false
	_, _ = graphql.ExecutePlanAppend(plan, graphql.ExecuteParams{Schema: schema, AST: doc, LazyPath: true}, nil)
	if sawPath || !sawNil {
		t.Fatalf("lazy mode: want nil info.Path; sawPath=%v sawNil=%v", sawPath, sawNil)
	}
}

// TestAppendLazyPath_ErrorLocation confirms that error envelopes
// under LazyPath carry the same `path` array as the default-mode
// envelope — the depth-stack snapshot must reconstruct the same
// locator that AsArray() builds from a linked list.
func TestAppendLazyPath_ErrorLocation(t *testing.T) {
	thing := graphql.NewObject(graphql.ObjectConfig{
		Name: "Thing",
		Fields: graphql.Fields{
			"name": &graphql.Field{
				Type: graphql.NewNonNull(graphql.String),
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if s, ok := p.Source.(map[string]interface{}); ok {
						return s["name"], nil
					}
					return nil, errors.New("bad source")
				},
			},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Q",
			Fields: graphql.Fields{
				"things": &graphql.Field{
					Type: graphql.NewList(thing),
					Resolve: func(graphql.ResolveParams) (interface{}, error) {
						return []map[string]interface{}{
							{"name": "ok"},
							{"name": nil},
						}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runParity(t, schema, `{ things { name } }`, "", nil, nil)
}
