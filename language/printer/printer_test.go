package printer_test

import (
	"io/ioutil"
	"reflect"
	"testing"

	"github.com/IodeSystems/graphql-go/v3/language/ast"
	"github.com/IodeSystems/graphql-go/v3/language/parser"
	"github.com/IodeSystems/graphql-go/v3/language/printer"
	"github.com/IodeSystems/graphql-go/v3/testutil"
)

func parse(t *testing.T, query string) *ast.Document {
	astDoc, err := parser.Parse(parser.ParseParams{
		Source: query,
		Options: parser.ParseOptions{
			NoLocation: true,
		},
	})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return astDoc
}

func TestPrinter_DoesNotAlterAST(t *testing.T) {
	b, err := ioutil.ReadFile("../../kitchen-sink.graphql")
	if err != nil {
		t.Fatalf("unable to load kitchen-sink.graphql")
	}

	query := string(b)
	astDoc := parse(t, query)

	astDocBefore := testutil.ASTToJSON(t, astDoc)

	_ = printer.Print(astDoc)

	astDocAfter := testutil.ASTToJSON(t, astDoc)

	_ = testutil.ASTToJSON(t, astDoc)

	if !reflect.DeepEqual(astDocAfter, astDocBefore) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(astDocBefore, astDocAfter))
	}
}

func TestPrinter_PrintsMinimalAST(t *testing.T) {
	astDoc := ast.NewField(&ast.Field{
		Name: ast.NewName(&ast.Name{
			Value: "foo",
		}),
	})
	results := printer.Print(astDoc)
	expected := "foo"
	if !reflect.DeepEqual(results, expected) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(expected, results))
	}
}

// TestPrinter_ProducesHelpfulErrorMessages
// Skipped, can't figure out how to pass in an invalid astDoc, which is already strongly-typed

func TestPrinter_CorrectlyPrintsNonQueryOperationsWithoutName(t *testing.T) {

	// Test #1
	queryAstShorthanded := `query { id, name }`
	expected := `{
  id
  name
}
`
	astDoc := parse(t, queryAstShorthanded)
	results := printer.Print(astDoc)

	if !reflect.DeepEqual(expected, results) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(expected, results))
	}

	// Test #2
	mutationAst := `mutation { id, name }`
	expected = `mutation {
  id
  name
}
`
	astDoc = parse(t, mutationAst)
	results = printer.Print(astDoc)

	if !reflect.DeepEqual(expected, results) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(expected, results))
	}

	// Test #3
	queryAstWithArtifacts := `query ($foo: TestType) @testDirective { id, name }`
	expected = `query ($foo: TestType) @testDirective {
  id
  name
}
`
	astDoc = parse(t, queryAstWithArtifacts)
	results = printer.Print(astDoc)

	if !reflect.DeepEqual(expected, results) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(expected, results))
	}

	// Test #4
	mutationAstWithArtifacts := `mutation ($foo: TestType) @testDirective { id, name }`
	expected = `mutation ($foo: TestType) @testDirective {
  id
  name
}
`
	astDoc = parse(t, mutationAstWithArtifacts)
	results = printer.Print(astDoc)

	if !reflect.DeepEqual(expected, results) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(expected, results))
	}
}

func TestPrinter_PrintsKitchenSink(t *testing.T) {
	b, err := ioutil.ReadFile("../../kitchen-sink.graphql")
	if err != nil {
		t.Fatalf("unable to load kitchen-sink.graphql")
	}

	query := string(b)
	astDoc := parse(t, query)
	expected := `query namedQuery($foo: ComplexFooType, $bar: Bar = DefaultBarValue) {
  customUser: user(id: [987, 654]) {
    id
    ... on User @defer {
      field2 {
        id
        alias: field1(first: 10, after: $foo) @include(if: $foo) {
          id
          ...frag
        }
      }
    }
    ... @skip(unless: $foo) {
      id
    }
    ... {
      id
    }
  }
}

mutation favPost {
  fav(post: 123) @defer {
    post {
      id
    }
  }
}

subscription PostFavSubscription($input: StoryLikeSubscribeInput) {
  postFavSubscribe(input: $input) {
    post {
      favers {
        count
      }
      favSentence {
        text
      }
    }
  }
}

fragment frag on Follower {
  foo(size: $size, bar: $b, obj: {key: "value"})
}

{
  unnamed(truthyVal: true, falseyVal: false)
  query
}
`
	results := printer.Print(astDoc)

	if !reflect.DeepEqual(expected, results) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(expected, results))
	}
}

func TestPrinter_CorrectlyPrintsStringArgumentsWithProperQuoting(t *testing.T) {
	queryAst := `query { foo(jsonStr: "{\"foo\": \"bar\"}") }`
	expected := `{
  foo(jsonStr: "{\"foo\": \"bar\"}")
}
`
	astDoc := parse(t, queryAst)
	results := printer.Print(astDoc)

	if !reflect.DeepEqual(expected, results) {
		t.Fatalf("Unexpected result, Diff: %v", testutil.Diff(expected, results))
	}
}

func BenchmarkPrint(b *testing.B) {
	q, err := ioutil.ReadFile("../../kitchen-sink.graphql")
	if err != nil {
		b.Fatalf("unable to load kitchen-sink.graphql")
	}

	query := string(q)

	astDoc, err := parser.Parse(parser.ParseParams{
		Source: query,
		Options: parser.ParseOptions{
			NoLocation: true,
		},
	})
	if err != nil {
		b.Fatalf("Parse failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = printer.Print(astDoc)
	}
}

func TestPrintBlockStringDescriptionRoundTrip(t *testing.T) {
	descs := []string{
		`Representation of a "Foo"`,
		`ends with quote"`,
		`ends with two quotes""`,
		`contains """ triple`,
		`ends with triple"""`,
		`""" starts with triple`,
		`ends with backslash\`,
		`backslash before triple \"""`,
		`  leading whitespace`,
		`  leading whitespace and quote"`,
		"multi\nline \"quoted\"",
		"multi\nline ends with quote\"",
		"multi\n  indented \"\"\" triple\nbackslash\\",
		"  deeper first line\nshallow second",
		`x`,
	}
	for _, desc := range descs {
		doc := parse(t, `"x" type Foo { "x" a: Int }`)
		obj := doc.Definitions[0].(*ast.ObjectDefinition)
		obj.Description.Value = desc
		obj.Fields[0].Description.Value = desc
		out := printer.Print(doc).(string)
		reparsed, err := parser.Parse(parser.ParseParams{Source: out, Options: parser.ParseOptions{NoLocation: true}})
		if err != nil {
			t.Errorf("desc %q: printed SDL does not reparse: %v\n%s", desc, err, out)
			continue
		}
		robj := reparsed.Definitions[0].(*ast.ObjectDefinition)
		if got := robj.Description.Value; got != desc {
			t.Errorf("object desc: want %q, got %q\n%s", desc, got, out)
		}
		if got := robj.Fields[0].Description.Value; got != desc {
			t.Errorf("field desc: want %q, got %q\n%s", desc, got, out)
		}
	}
}

// Upstream issues 677, 641.
func TestPrintReparsesDescriptionWithQuotes(t *testing.T) {
	for _, src := range []string{
		"\"\"\"\nRepresentation of a \"Foo\"\n\"\"\"\ntype Foo {\n  name: String\n}\n",
		`"test \"quoted\"" type myObject { a: Int }`,
	} {
		doc := parse(t, src)
		out := printer.Print(doc).(string)
		if _, err := parser.Parse(parser.ParseParams{Source: out}); err != nil {
			t.Errorf("printed SDL does not reparse: %v\n%s", err, out)
		}
	}
}
