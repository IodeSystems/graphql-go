package graphql

import (
	"fmt"
	"strconv"

	"github.com/IodeSystems/graphql-go/v3/language/ast"
)

// normalizeDocument walks the given operation in `doc`, replacing
// fully-literal field-argument values with synthetic variables. Two
// queries that differ only in literal values now collapse to one
// normalized form — so a PlanCache keyed on the normalized text can
// reuse a single *Plan across arbitrary literal variations.
//
// Returns:
//   - newDoc: the normalized document (a deep-clone of the operation
//     with rewritten arguments + appended VariableDefinitions). The
//     fragments in `doc` are preserved by reference; this first cut
//     does NOT recurse into fragment definitions, so literals there
//     remain in-text (still cached, just without de-duplication
//     across literal variants).
//   - synthArgs: the extracted literal values, keyed by synth
//     variable name. Callers merge this into the request's Args
//     before ExecutePlan.
//   - cacheKey: the printed normalized document (the canonical
//     cache identifier). Empty when normalization isn't applicable
//     (e.g. operationName not found).
//   - err: only set for document-level malformations; missing
//     literals to extract is a no-op return, not an error.
//
// Scope (first cut, intentional):
//   - Only field arguments are normalized. Directive arguments
//     (@skip(if: true), @deprecated(reason: "..."), etc.) stay as
//     literals — they're rare and rule-bound.
//   - Only fully-literal argument values are extracted as a single
//     synth variable. A value containing a variable somewhere
//     (e.g. {a: 1, b: $foo}) is left untouched.
//   - Abstract-typed sub-selections (Interface/Union returns) are
//     not recursed into — type inference for arg positions across
//     concrete-type branches is more bookkeeping than first-cut
//     warrants. Wide queries that shape parametric calls at the
//     top level get the win regardless.
//   - Fragment definitions stay as-is. Real-world parametric
//     queries usually carry their literals at the call site, not
//     in fragment definitions.
//
// Synth variable naming uses the prefix `__pcv` (plan-cache-var) to
// minimize collision with hand-authored variable names.
func normalizeDocument(schema *Schema, doc *ast.Document, operationName string) (_ *ast.Document, _ map[string]interface{}, _ string, err error) {
	// Literal coercion calls custom scalar ParseLiteral, which may panic.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	if schema == nil || doc == nil {
		return doc, nil, "", nil
	}

	var op *ast.OperationDefinition
	var foundOps int
	for _, def := range doc.Definitions {
		if d, ok := def.(*ast.OperationDefinition); ok {
			foundOps++
			if operationName == "" || (d.GetName() != nil && d.GetName().Value == operationName) {
				op = d
			}
		}
	}
	if op == nil {
		return doc, nil, "", nil
	}
	if foundOps > 1 && operationName == "" {
		return doc, nil, "", nil
	}

	rootType, err := getOperationRootType(*schema, op)
	if err != nil {
		return doc, nil, "", err
	}

	ctx := &normCtx{
		schema:     schema,
		synthArgs:  map[string]interface{}{},
		newVarDefs: nil,
	}

	newOp := cloneOperation(op)
	ctx.normalizeSelectionSet(newOp.SelectionSet, rootType)

	if len(ctx.synthArgs) == 0 {
		// No literals to extract — return early. Fingerprint the
		// original doc (over the operation + reachable fragments)
		// for the cache key.
		return doc, nil, fingerprintDocument(doc, op, operationName), nil
	}

	// Append the new synth-variable definitions to the operation.
	newOp.VariableDefinitions = append(newOp.VariableDefinitions, ctx.newVarDefs...)

	// Build a new doc containing the normalized op + the rest of the
	// definitions (fragments) unchanged. Order preserved: op stays in
	// its original slot.
	newDefs := make([]ast.Node, 0, len(doc.Definitions))
	for _, def := range doc.Definitions {
		if def == op {
			newDefs = append(newDefs, newOp)
		} else {
			newDefs = append(newDefs, def)
		}
	}
	newDoc := &ast.Document{Kind: doc.Kind, Loc: doc.Loc, Definitions: newDefs}
	return newDoc, ctx.synthArgs, fingerprintDocument(newDoc, newOp, operationName), nil
}

// fingerprintDocument produces a canonical string identifying the
// normalized structural shape of `op` (and any fragments it spreads,
// recursively). Two queries with the same shape — but possibly
// different extracted literals — produce the same fingerprint, so
// they collapse to one PlanCache entry.
//
// We use a 64-bit FNV-1a hash so the cache key stays tiny regardless
// of query size; collisions over a 1024-entry cache are
// vanishingly improbable, and the schema-pointer guard inside the
// cache catches any cross-schema accident.
//
// The fingerprint captures everything the plan depends on: operation
// type, operation name, variable definitions (with defaults),
// directives (with arguments), and the selection tree (aliases, field
// names, arguments, sub-selections, fragment spreads and the bodies
// they reach). Extracted literals appear as their synth variable
// names, so two queries that differ only in those literals hash the
// same; literals that stayed in place are hashed by kind and content.
func fingerprintDocument(doc *ast.Document, op *ast.OperationDefinition, operationName string) string {
	w := fingerprintWriter{h: fnvOffset64, fragments: collectFragmentDefs(doc)}
	w.writeString("OP:")
	w.writeString(string(op.Operation))
	w.writeByte(0)
	w.writeString(operationName)
	w.writeByte(0)
	w.writeVariableDefs(op.VariableDefinitions)
	w.writeDirectives(op.Directives)
	w.writeSelectionSet(op.SelectionSet)
	return strconv.FormatUint(w.h, 16)
}

func collectFragmentDefs(doc *ast.Document) map[string]*ast.FragmentDefinition {
	out := map[string]*ast.FragmentDefinition{}
	for _, def := range doc.Definitions {
		if fd, ok := def.(*ast.FragmentDefinition); ok && fd.Name != nil {
			out[fd.Name.Value] = fd
		}
	}
	return out
}

// fingerprintWriter walks the AST and feeds canonical bytes into the
// hash. Separate from the normalizer's mutating walker because we
// need a different traversal: we follow fragment spreads here (so
// spread-reachable structure participates in the cache key), but we
// don't rewrite anything.
type fingerprintWriter struct {
	h uint64 // FNV-1a state

	fragments map[string]*ast.FragmentDefinition
	visited   map[string]bool
}

// FNV-1a, 64-bit, the same function as hash/fnv's New64a. Inlined so a
// write does not box a []byte through the hash.Hash interface: that
// cost one allocation per byte and per string written, on every
// normalized PlanCache.Get.
const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func (w *fingerprintWriter) writeString(s string) {
	for i := 0; i < len(s); i++ {
		w.h ^= uint64(s[i])
		w.h *= fnvPrime64
	}
}

func (w *fingerprintWriter) writeByte(b byte) {
	w.h ^= uint64(b)
	w.h *= fnvPrime64
}

func (w *fingerprintWriter) writeVariableDefs(defs []*ast.VariableDefinition) {
	w.writeString("VD(")
	for _, d := range defs {
		if d == nil || d.Variable == nil || d.Variable.Name == nil {
			continue
		}
		w.writeString(d.Variable.Name.Value)
		w.writeByte(':')
		w.writeType(d.Type)
		// The default is part of the key: the plan reads defaults from
		// the operation it was built from, and validation of the
		// default (`$v: Int = null` into Int!) runs only on a miss.
		w.writeByte('=')
		w.writeValue(d.DefaultValue)
		w.writeByte(',')
	}
	w.writeByte(')')
}

func (w *fingerprintWriter) writeType(t ast.Type) {
	switch tt := t.(type) {
	case *ast.NonNull:
		w.writeType(tt.Type)
		w.writeByte('!')
	case *ast.List:
		w.writeByte('[')
		w.writeType(tt.Type)
		w.writeByte(']')
	case *ast.Named:
		if tt != nil && tt.Name != nil {
			w.writeString(tt.Name.Value)
		}
	}
}

func (w *fingerprintWriter) writeSelectionSet(sel *ast.SelectionSet) {
	if sel == nil {
		return
	}
	w.writeByte('{')
	for _, isel := range sel.Selections {
		switch s := isel.(type) {
		case *ast.Field:
			if s.Alias != nil {
				w.writeString(s.Alias.Value)
				w.writeByte(':')
			}
			if s.Name != nil {
				w.writeString(s.Name.Value)
			}
			w.writeArguments(s.Arguments)
			w.writeDirectives(s.Directives)
			w.writeSelectionSet(s.SelectionSet)
			w.writeByte(';')
		case *ast.InlineFragment:
			w.writeString("...")
			if s.TypeCondition != nil && s.TypeCondition.Name != nil {
				w.writeString(s.TypeCondition.Name.Value)
			}
			w.writeDirectives(s.Directives)
			w.writeSelectionSet(s.SelectionSet)
			w.writeByte(';')
		case *ast.FragmentSpread:
			w.writeString("...")
			if s.Name != nil {
				w.writeString(s.Name.Value)
				w.writeDirectives(s.Directives)
				w.writeByte(';')
				w.writeFragmentBody(s.Name.Value)
			}
		}
	}
	w.writeByte('}')
}

func (w *fingerprintWriter) writeArguments(args []*ast.Argument) {
	if len(args) == 0 {
		return
	}
	w.writeByte('(')
	for _, a := range args {
		if a == nil || a.Name == nil {
			continue
		}
		w.writeString(a.Name.Value)
		w.writeByte('=')
		w.writeValue(a.Value)
		w.writeByte(',')
	}
	w.writeByte(')')
}

// writeDirectives hashes directives with their arguments. The planner
// evaluates literal @skip/@include at plan time, so `@skip(if: true)`
// and `@skip(if: false)` must not share a plan.
func (w *fingerprintWriter) writeDirectives(dirs []*ast.Directive) {
	for _, d := range dirs {
		if d == nil || d.Name == nil {
			continue
		}
		w.writeByte('@')
		w.writeString(d.Name.Value)
		w.writeArguments(d.Arguments)
	}
}

func (w *fingerprintWriter) writeFragmentBody(name string) {
	if w.visited == nil {
		w.visited = map[string]bool{}
	}
	if w.visited[name] {
		return
	}
	w.visited[name] = true
	frag, ok := w.fragments[name]
	if !ok {
		return
	}
	w.writeByte('F')
	if frag.TypeCondition != nil && frag.TypeCondition.Name != nil {
		w.writeString(frag.TypeCondition.Name.Value)
	}
	w.writeDirectives(frag.Directives)
	w.writeSelectionSet(frag.SelectionSet)
}

// writeValue writes canonical bytes for an ast.Value. Variables are
// hashed by name (so synth var names from normalization participate
// in the key). Literals that survived normalization are hashed as
// their kind+content — two identical un-extractable literals map to
// the same fingerprint, two different ones don't.
func (w *fingerprintWriter) writeValue(v ast.Value) {
	switch n := v.(type) {
	case nil:
		w.writeByte('n')
	case *ast.Variable:
		w.writeByte('V')
		if n.Name != nil {
			w.writeString(n.Name.Value)
		}
	case *ast.IntValue:
		w.writeByte('i')
		w.writeString(n.Value)
	case *ast.FloatValue:
		w.writeByte('f')
		w.writeString(n.Value)
	case *ast.StringValue:
		w.writeByte('s')
		w.writeString(n.Value)
	case *ast.BooleanValue:
		w.writeByte('b')
		if n.Value {
			w.writeByte('1')
		} else {
			w.writeByte('0')
		}
	case *ast.EnumValue:
		w.writeByte('e')
		w.writeString(n.Value)
	case *ast.NullValue:
		w.writeByte('N')
	case *ast.ListValue:
		w.writeByte('[')
		for _, item := range n.Values {
			w.writeValue(item)
			w.writeByte(',')
		}
		w.writeByte(']')
	case *ast.ObjectValue:
		w.writeByte('{')
		for _, f := range n.Fields {
			if f == nil || f.Name == nil {
				continue
			}
			w.writeString(f.Name.Value)
			w.writeByte('=')
			w.writeValue(f.Value)
			w.writeByte(',')
		}
		w.writeByte('}')
	}
}

// normCtx threads state across the recursive walk: schema for type
// lookups, synth counter, accumulated args + var defs.
type normCtx struct {
	schema     *Schema
	counter    int
	synthArgs  map[string]interface{}
	newVarDefs []*ast.VariableDefinition
}

func (c *normCtx) nextName() string {
	n := fmt.Sprintf("__pcv%d", c.counter)
	c.counter++
	return n
}

// normalizeSelectionSet walks selections under the given parent type.
// Inline fragments are followed (with type-condition awareness);
// fragment spreads are skipped (literals inside named fragments stay).
func (c *normCtx) normalizeSelectionSet(sel *ast.SelectionSet, parentType *Object) {
	if sel == nil {
		return
	}
	for _, isel := range sel.Selections {
		switch s := isel.(type) {
		case *ast.Field:
			c.normalizeField(s, parentType)
		case *ast.InlineFragment:
			frag := s
			condType := parentType
			if frag.TypeCondition != nil {
				if t, err := typeFromAST(*c.schema, frag.TypeCondition); err == nil {
					if obj, ok := t.(*Object); ok {
						condType = obj
					}
				}
			}
			c.normalizeSelectionSet(frag.SelectionSet, condType)
		case *ast.FragmentSpread:
			// Skipped: see scope note in normalizeDocument.
		}
	}
}

// normalizeField rewrites a field's arguments in place (the field
// AST is already a clone of the original) and recurses into its
// sub-selection if the return type is a concrete Object.
func (c *normCtx) normalizeField(f *ast.Field, parentType *Object) {
	fieldName := ""
	if f.Name != nil {
		fieldName = f.Name.Value
	}
	fieldDef := getFieldDef(*c.schema, parentType, fieldName)
	if fieldDef == nil {
		return
	}
	if len(f.Arguments) > 0 {
		// Build an arg-name → argDef map for O(1) lookup.
		argDefByName := make(map[string]*Argument, len(fieldDef.Args))
		for _, ad := range fieldDef.Args {
			argDefByName[ad.PrivateName] = ad
		}
		for _, arg := range f.Arguments {
			if arg == nil || arg.Name == nil {
				continue
			}
			ad := argDefByName[arg.Name.Value]
			if ad == nil {
				continue
			}
			if newVal, ok := c.tryExtract(arg.Value, ad.Type); ok {
				arg.Value = newVal
			}
		}
	}
	// Recurse into sub-selection on concrete object returns. List and
	// NonNull wrappers are unwrapped here.
	if f.SelectionSet != nil {
		t := unwrapToNamed(fieldDef.Type)
		if obj, ok := t.(*Object); ok {
			c.normalizeSelectionSet(f.SelectionSet, obj)
		}
	}
}

// tryExtract attempts to replace the entire `value` AST with a synth
// variable. Returns the replacement *ast.Variable + true on success;
// returns the original value + false when extraction isn't safe
// (variable already present anywhere in the value).
func (c *normCtx) tryExtract(value ast.Value, expected Input) (ast.Value, bool) {
	if value == nil {
		return value, false
	}
	if _, isVar := value.(*ast.Variable); isVar {
		return value, false
	}
	if valueHasVariables(value) {
		return value, false
	}
	if expected == nil {
		return value, false
	}
	// Coerce literal once at extract time; the tree has no variables.
	// An invalid literal stays in place so validation reports it
	// against the literal. Extracted, it would pass validation as a
	// variable: `[Int!]` given `[null]` would coerce to [nil], and
	// `[Int]` given `[1, "x"]` to [1, nil].
	coerced, valid := coerceLiteral(value, expected)
	if !valid {
		return value, false
	}
	if coerced == nil {
		// valueFromAST returns nil for literals it can't coerce
		// (typically a type mismatch the validator should have caught
		// earlier). Don't extract; let the executor surface the
		// downstream error against the original literal.
		return value, false
	}
	name := c.nextName()
	c.synthArgs[name] = coerced
	c.newVarDefs = append(c.newVarDefs, ast.NewVariableDefinition(&ast.VariableDefinition{
		Variable: ast.NewVariable(&ast.Variable{Name: ast.NewName(&ast.Name{Value: name})}),
		Type:     typeASTFromGoType(expected),
	}))
	return ast.NewVariable(&ast.Variable{Name: ast.NewName(&ast.Name{Value: name})}), true
}

// coerceLiteral returns valueFromAST(v, t, nil) for a variable-free
// literal, and whether isValidLiteralValue(t, v) accepts it. It runs on
// every normalized PlanCache.Get, hits included, so it does both in one
// pass: each scalar is parsed once and nothing is allocated for the
// check. TestCoerceLiteralMatches pins it to the two functions.
func coerceLiteral(v ast.Value, t Input) (interface{}, bool) {
	if nn, ok := t.(*NonNull); ok {
		if nn.Error() != nil || v == nil || isNullLiteral(v) {
			return nil, false
		}
		inner, _ := nn.OfType.(Input)
		return coerceLiteral(v, inner)
	}
	if v == nil || isNullLiteral(v) {
		return nil, true
	}
	switch t := t.(type) {
	case *List:
		item, _ := t.OfType.(Input)
		lv, ok := v.(*ast.ListValue)
		if !ok {
			// A non-list value is a list of one.
			val, valid := coerceLiteral(v, item)
			return []interface{}{val}, valid
		}
		values := make([]interface{}, 0, len(lv.Values))
		valid := true
		for _, itemAST := range lv.Values {
			val, ok := coerceLiteral(itemAST, item)
			valid = valid && ok
			values = append(values, val)
		}
		return values, valid
	case *InputObject:
		ov, ok := v.(*ast.ObjectValue)
		if !ok {
			return nil, false
		}
		fields := t.Fields()
		valid := true
		for _, of := range ov.Fields {
			if of == nil || of.Name == nil || fields[of.Name.Value] == nil {
				valid = false
			}
		}
		obj := map[string]interface{}{}
		for name, field := range fields {
			var fieldAST *ast.ObjectField
			for _, of := range ov.Fields {
				if of != nil && of.Name != nil && of.Name.Value == name {
					fieldAST = of // last one wins, as in valueFromAST
				}
			}
			if fieldAST == nil {
				if _, nonNull := field.Type.(*NonNull); nonNull && !hasNonNullDefault(field.DefaultValue) {
					valid = false
				}
				if def, hasDefault := defaultValue(field.DefaultValue); hasDefault {
					obj[name] = def
				}
				continue
			}
			val, ok := coerceLiteral(fieldAST.Value, field.Type)
			valid = valid && ok
			if isNullLiteral(fieldAST.Value) {
				obj[name] = nil
			} else if !isNullish(val) {
				obj[name] = val
			}
		}
		return obj, valid
	case *Scalar:
		parsed := t.ParseLiteral(v)
		return parsed, !isNullish(parsed)
	case *Enum:
		parsed := t.ParseLiteral(v)
		return parsed, !isNullish(parsed)
	}
	return nil, true
}

// typeASTFromGoType maps a runtime Type to its AST form so we can
// build VariableDefinition.Type. NonNull and List wrap; named types
// terminate with an *ast.Named referencing the type's name.
func typeASTFromGoType(t Input) ast.Type {
	switch tt := t.(type) {
	case *NonNull:
		inner := typeASTFromGoType(tt.OfType.(Input))
		return ast.NewNonNull(&ast.NonNull{Type: inner})
	case *List:
		inner := typeASTFromGoType(tt.OfType.(Input))
		return ast.NewList(&ast.List{Type: inner})
	default:
		name := ""
		if named, ok := t.(interface{ Name() string }); ok {
			name = named.Name()
		}
		return ast.NewNamed(&ast.Named{Name: ast.NewName(&ast.Name{Value: name})})
	}
}

// unwrapToNamed strips NonNull and List wrappers to expose the inner
// named type. Mirrors plan.go's unwrapNamedType for Output positions
// but takes any Type for use against fieldDef.Type which is Output.
func unwrapToNamed(t Type) Type {
	for {
		switch tt := t.(type) {
		case *NonNull:
			t = tt.OfType
		case *List:
			t = tt.OfType
		default:
			return tt
		}
	}
}

// cloneOperation deep-copies the parts of an OperationDefinition we
// mutate: SelectionSet (recursively, only Fields' Arguments) and the
// VariableDefinitions slice (we append; original stays read-only).
// Other fields share with the original — we never write through them.
func cloneOperation(op *ast.OperationDefinition) *ast.OperationDefinition {
	out := &ast.OperationDefinition{
		Kind:                op.Kind,
		Loc:                 op.Loc,
		Operation:           op.Operation,
		Name:                op.Name,
		Directives:          op.Directives,
		VariableDefinitions: append([]*ast.VariableDefinition(nil), op.VariableDefinitions...),
		SelectionSet:        cloneSelectionSet(op.SelectionSet),
	}
	return out
}

func cloneSelectionSet(sel *ast.SelectionSet) *ast.SelectionSet {
	if sel == nil {
		return nil
	}
	out := &ast.SelectionSet{Kind: sel.Kind, Loc: sel.Loc, Selections: make([]ast.Selection, len(sel.Selections))}
	for i, s := range sel.Selections {
		switch n := s.(type) {
		case *ast.Field:
			out.Selections[i] = cloneField(n)
		case *ast.InlineFragment:
			out.Selections[i] = &ast.InlineFragment{
				Kind:          n.Kind,
				Loc:           n.Loc,
				TypeCondition: n.TypeCondition,
				Directives:    n.Directives,
				SelectionSet:  cloneSelectionSet(n.SelectionSet),
			}
		default:
			// FragmentSpread or anything else: share by reference.
			out.Selections[i] = s
		}
	}
	return out
}

func cloneField(f *ast.Field) *ast.Field {
	args := make([]*ast.Argument, len(f.Arguments))
	for i, a := range f.Arguments {
		// Shallow clone of *ast.Argument so we can swap a.Value
		// without mutating the original.
		ac := *a
		args[i] = &ac
	}
	return &ast.Field{
		Kind:         f.Kind,
		Loc:          f.Loc,
		Alias:        f.Alias,
		Name:         f.Name,
		Arguments:    args,
		Directives:   f.Directives,
		SelectionSet: cloneSelectionSet(f.SelectionSet),
	}
}
