package parser

import (
	"strings"
	"testing"
)

// The parser descends one Go stack frame per nesting level. A stack
// overflow in Go is a fatal runtime error that recover cannot catch, so
// an unbounded document kills the process rather than failing the
// request. These pin the cap that prevents that, and pin that it counts
// nesting only — never document size.

func deepSelection(n int) string {
	return "{" + strings.Repeat("a{", n) + "b" + strings.Repeat("}", n+1)
}

func TestMaxDepth_DefaultRejectsDeepNesting(t *testing.T) {
	_, err := Parse(ParseParams{Source: deepSelection(DefaultMaxDepth + 10)})
	if err == nil {
		t.Fatal("expected nesting past the default cap to be rejected")
	}
	if !strings.Contains(err.Error(), "nests deeper than") {
		t.Errorf("expected a depth error, got: %v", err)
	}
}

func TestMaxDepth_DefaultAcceptsOrdinaryNesting(t *testing.T) {
	// Real documents nest tens of levels; the cap must be nowhere near.
	for _, depth := range []int{1, 5, 50, DefaultMaxDepth - 2} {
		if _, err := Parse(ParseParams{Source: deepSelection(depth)}); err != nil {
			t.Errorf("depth %d should parse, got: %v", depth, err)
		}
	}
}

func TestMaxDepth_Configurable(t *testing.T) {
	// A stricter cap than the default.
	if _, err := Parse(ParseParams{
		Source:  deepSelection(20),
		Options: ParseOptions{MaxDepth: 5},
	}); err == nil {
		t.Error("depth 20 should be rejected when MaxDepth is 5")
	}
	if _, err := Parse(ParseParams{
		Source:  deepSelection(4),
		Options: ParseOptions{MaxDepth: 5},
	}); err != nil {
		t.Errorf("depth 4 should parse when MaxDepth is 5, got: %v", err)
	}
	// A looser cap than the default.
	if _, err := Parse(ParseParams{
		Source:  deepSelection(DefaultMaxDepth + 50),
		Options: ParseOptions{MaxDepth: DefaultMaxDepth * 2},
	}); err != nil {
		t.Errorf("raising MaxDepth should admit deeper documents, got: %v", err)
	}
}

func TestMaxDepth_NegativeDisables(t *testing.T) {
	if _, err := Parse(ParseParams{
		Source:  deepSelection(DefaultMaxDepth + 500),
		Options: ParseOptions{MaxDepth: -1},
	}); err != nil {
		t.Errorf("a negative MaxDepth should disable the check, got: %v", err)
	}
}

// Depth is not size. Width costs no stack — sibling fields, list
// elements and object fields are parsed in a loop — so a very wide
// document must parse regardless of the cap.
func TestMaxDepth_WidthIsNotDepth(t *testing.T) {
	wide := "{ " + strings.Repeat("a ", 50000) + "}"
	if _, err := Parse(ParseParams{
		Source:  wide,
		Options: ParseOptions{MaxDepth: 5},
	}); err != nil {
		t.Errorf("50k sibling fields at depth 1 must parse under MaxDepth 5, got: %v", err)
	}

	wideList := "{a(x:[" + strings.Repeat("1,", 20000) + "1])}"
	if _, err := Parse(ParseParams{
		Source:  wideList,
		Options: ParseOptions{MaxDepth: 5},
	}); err != nil {
		t.Errorf("a 20k-element list is depth 1, must parse under MaxDepth 5, got: %v", err)
	}
}

// Selection sets are not the only recursive path: list values,
// input-object values and list types each recurse independently, so each
// needs its own guard.
func TestMaxDepth_AppliesToEveryRecursivePath(t *testing.T) {
	cases := map[string]string{
		"selection set":      deepSelection(50),
		"list value":         "{a(x:" + strings.Repeat("[", 50) + strings.Repeat("]", 50) + ")}",
		"input object value": "{a(x:" + strings.Repeat("{b:", 50) + "1" + strings.Repeat("}", 50) + ")}",
		"list type":          "query Q($v:" + strings.Repeat("[", 50) + "Int" + strings.Repeat("]", 50) + "){a}",
	}
	for name, doc := range cases {
		if _, err := Parse(ParseParams{
			Source:  doc,
			Options: ParseOptions{MaxDepth: 5},
		}); err == nil {
			t.Errorf("%s: nesting 50 deep was accepted under MaxDepth 5", name)
		}
		if _, err := Parse(ParseParams{
			Source:  doc,
			Options: ParseOptions{MaxDepth: 200},
		}); err != nil {
			t.Errorf("%s: nesting 50 deep should parse under MaxDepth 200, got: %v", name, err)
		}
	}
}

// Depth must not accumulate across sibling branches: ten sequential
// 10-deep subtrees are depth 10, not depth 100.
func TestMaxDepth_UnwindsOnSiblings(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 10; i++ {
		b.WriteString(strings.Repeat("a{", 10))
		b.WriteString("b")
		b.WriteString(strings.Repeat("}", 10))
	}
	b.WriteString("}")
	if _, err := Parse(ParseParams{
		Source:  b.String(),
		Options: ParseOptions{MaxDepth: 20},
	}); err != nil {
		t.Errorf("sibling subtrees must not accumulate depth, got: %v", err)
	}
}

// ParseValue shares the value-literal path and must be capped too.
func TestMaxDepth_AppliesToParseValue(t *testing.T) {
	deep := strings.Repeat("[", 50) + strings.Repeat("]", 50)
	if _, err := ParseValue(ParseParams{
		Source:  deep,
		Options: ParseOptions{MaxDepth: 5},
	}); err == nil {
		t.Error("ParseValue should honour MaxDepth")
	}
}
