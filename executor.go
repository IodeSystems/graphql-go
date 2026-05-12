package graphql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/IodeSystems/graphql-go/gqlerrors"
	"github.com/IodeSystems/graphql-go/language/ast"
)

type ExecuteParams struct {
	Schema        Schema
	Root          interface{}
	AST           *ast.Document
	OperationName string
	Args          map[string]interface{}

	// Context may be provided to pass application-specific per-request
	// information to resolve functions.
	Context context.Context

	// PreserveInfoPath disables the append-mode executor's depth-stack
	// path representation and restores per-field *ResponsePath
	// allocation so ResolveInfo.Path is populated for every resolver
	// call (today's spec contract). The default — false — leaves
	// info.Path nil under ExecutePlanAppend and reclaims one alloc
	// per resolved field and per list item. Set true if any resolver
	// in the schema reads info.Path (DataLoader keyed on path,
	// tracing spans, federation refs). Has no effect on ExecutePlan,
	// which always populates info.Path.
	PreserveInfoPath bool

	// ConcurrentThunks restores the thunk-concurrency contract under
	// the append-mode executor by delegating to ExecutePlan +
	// json.Marshal: thunked resolvers (`func() (interface{}, error)`)
	// run with breadth-first dethunking so their internal goroutines
	// overlap. The default — false — dethunks synchronously as
	// resolvers return, which is correct for thunks that only defer
	// work (no concurrency) but loses the documented parallelism
	// pattern (see examples/concurrent-resolvers). Set true when the
	// schema's thunks kick off goroutines and rely on the dethunk
	// pass for parallel execution. Has no effect on ExecutePlan.
	ConcurrentThunks bool

	// RetainArgs disables the args-map pool that the executor uses
	// to recycle ResolveParams.Args across resolver calls. The
	// default — false — acquires the args map from a sync.Pool and
	// returns it after the resolver finishes; resolvers must treat
	// p.Args as borrowed (read freely, do not retain references
	// past the call, do not mutate-then-return). Set true if a
	// resolver stashes p.Args in a struct field, channel, or
	// goroutine that outlives the resolver call. Applies to both
	// ExecutePlan and ExecutePlanAppend.
	RetainArgs bool
}

// Execute runs an operation against a schema. Behavior is unchanged
// from prior releases: it now plans + executes via PlanQuery /
// ExecutePlan internally, but builds a fresh plan per call. Callers
// that issue the same query repeatedly should hold onto the *Plan
// returned by PlanQuery and pass it to ExecutePlan to skip the
// per-call planning work.
func Execute(p ExecuteParams) (result *Result) {
	plan, err := PlanQuery(&p.Schema, p.AST, p.OperationName)
	if err != nil {
		return &Result{Errors: gqlerrors.FormatErrors(err)}
	}
	return ExecutePlan(plan, p)
}

type buildExecutionCtxParams struct {
	Schema        Schema
	Root          interface{}
	AST           *ast.Document
	OperationName string
	Args          map[string]interface{}
	Result        *Result
	Context       context.Context
}

type executionContext struct {
	Schema         Schema
	Fragments      map[string]ast.Definition
	Root           interface{}
	Operation      ast.Definition
	VariableValues map[string]interface{}
	Errors         []gqlerrors.FormattedError
	Context        context.Context

	// lazyPath mirrors ExecuteParams.LazyPath. When true, the
	// append-mode walker uses pathBuf as the authoritative depth-stack
	// for the response path and skips per-field *ResponsePath
	// allocation. Always false for the map-tree executor.
	lazyPath bool
	pathBuf  []interface{}

	// retainArgs mirrors !ExecuteParams.RetainArgs. When true (the
	// default), the executor recycles per-resolver args maps through
	// argsMapPool. When false, every resolver call allocates a fresh
	// args map — required when resolvers retain p.Args references
	// past the resolve call (struct fields, channels, goroutines).
	poolArgs bool

	// ResponsePath slab. The map-tree executor allocates a fresh
	// *ResponsePath per resolved field and per list element; the slab
	// amortizes those into geometric-growth pages so wide/list-heavy
	// queries pay one heap alloc per ~16-256 paths instead of one per
	// path. Pages are never grown in place, so pointers handed out
	// earlier remain valid for the lifetime of the response. Resolvers
	// see normal *ResponsePath values via ResolveInfo.Path.
	pathPage []ResponsePath
	pathIdx  int

	// thunkCount is incremented every time a resolver-returned func
	// (a thunk) is wrapped by completePlannedValue. The dethunk pass
	// only needs to run when this is > 0; for thunk-free schemas
	// (the dominant shape) we skip the entire tree walk.
	thunkCount int
}

const (
	pathPageInitSize = 16
	pathPageMaxSize  = 256
)

// allocResponsePath returns a *ResponsePath populated with (prev, key),
// drawn from the executionContext's slab. Functionally equivalent to
// `prev.WithKey(key)` but amortizes the heap allocation.
func (eCtx *executionContext) allocResponsePath(prev *ResponsePath, key interface{}) *ResponsePath {
	if eCtx.pathIdx == len(eCtx.pathPage) {
		next := len(eCtx.pathPage) * 2
		if next < pathPageInitSize {
			next = pathPageInitSize
		} else if next > pathPageMaxSize {
			next = pathPageMaxSize
		}
		eCtx.pathPage = make([]ResponsePath, next)
		eCtx.pathIdx = 0
	}
	p := &eCtx.pathPage[eCtx.pathIdx]
	eCtx.pathIdx++
	p.Prev = prev
	p.Key = key
	return p
}

// argsMapPool recycles per-resolver argument maps. Acquired before
// each resolver call (via acquireArgsMap), released after (via
// releaseArgsMap). The pool persists across requests; resolvers must
// treat p.Args as borrowed (read freely, do not retain references
// past the resolver return). Adopters that retain set
// ExecuteParams.RetainArgs to opt out.
var argsMapPool = sync.Pool{
	New: func() interface{} {
		return make(map[string]interface{}, 4)
	},
}

// emptyArgsMap is a shared, never-mutated zero-length map used as
// p.Args for resolvers on arg-less fields. Resolvers must treat
// p.Args as read-only (documented contract), so a singleton is safe
// even on the no-pool path: there are no entries to read or mutate.
// Avoids one map allocation per arg-less resolver call.
var emptyArgsMap = map[string]interface{}{}

// acquireArgsMap returns a cleared args map suitable for one
// resolver call. Always returns a non-nil map; ResolveParams.Args is
// non-nil by historical contract.
func acquireArgsMap() map[string]interface{} {
	return argsMapPool.Get().(map[string]interface{})
}

// releaseArgsMap returns a no-longer-needed args map to the pool,
// clearing its keys first. Safe on a nil map (no-op). Must not be
// called when ExecuteParams.RetainArgs is set: the caller may have
// stashed the map past the resolver return.
func releaseArgsMap(m map[string]interface{}) {
	if m == nil {
		return
	}
	for k := range m {
		delete(m, k)
	}
	argsMapPool.Put(m)
}

// popPath truncates pathBuf back to the depth captured at the
// caller's push site. The walker open-codes the push (a branch on
// eCtx.lazyPath either WithKey-allocates or appends to pathBuf) but
// routes the pop through here so the deferred recover handlers
// converge on a single helper for both normal-return and
// panic-propagation paths.
func (eCtx *executionContext) popPath(entryDepth int) {
	if entryDepth < 0 || !eCtx.lazyPath {
		return
	}
	eCtx.pathBuf = eCtx.pathBuf[:entryDepth]
}

// errorPathArray returns the response-path locator for an error
// envelope. Under default mode the linked-list *ResponsePath argument
// is walked; under lazyPath the pathBuf depth-stack is snapshotted.
// Caller is responsible for not retaining the returned slice past the
// next pushPath/popPath cycle.
func (eCtx *executionContext) errorPathArray(path *ResponsePath) []interface{} {
	if path != nil {
		return path.AsArray()
	}
	if !eCtx.lazyPath || len(eCtx.pathBuf) == 0 {
		return nil
	}
	out := make([]interface{}, len(eCtx.pathBuf))
	copy(out, eCtx.pathBuf)
	return out
}

func buildExecutionContext(p buildExecutionCtxParams) (*executionContext, error) {
	eCtx := &executionContext{}
	var operation *ast.OperationDefinition
	fragments := map[string]ast.Definition{}

	for _, definition := range p.AST.Definitions {
		switch definition := definition.(type) {
		case *ast.OperationDefinition:
			if (p.OperationName == "") && operation != nil {
				return nil, errors.New("Must provide operation name if query contains multiple operations.")
			}
			if p.OperationName == "" || definition.GetName() != nil && definition.GetName().Value == p.OperationName {
				operation = definition
			}
		case *ast.FragmentDefinition:
			key := ""
			if definition.GetName() != nil && definition.GetName().Value != "" {
				key = definition.GetName().Value
			}
			fragments[key] = definition
		default:
			return nil, fmt.Errorf("GraphQL cannot execute a request containing a %v", definition.GetKind())
		}
	}

	if operation == nil {
		if p.OperationName != "" {
			return nil, fmt.Errorf(`Unknown operation named "%v".`, p.OperationName)
		}
		return nil, fmt.Errorf(`Must provide an operation.`)
	}

	variableValues, err := getVariableValues(p.Schema, operation.GetVariableDefinitions(), p.Args)
	if err != nil {
		return nil, err
	}

	eCtx.Schema = p.Schema
	eCtx.Fragments = fragments
	eCtx.Root = p.Root
	eCtx.Operation = operation
	eCtx.VariableValues = variableValues
	eCtx.Context = p.Context
	return eCtx, nil
}

// Extracts the root type of the operation from the schema.
func getOperationRootType(schema Schema, operation ast.Definition) (*Object, error) {
	if operation == nil {
		return nil, errors.New("Can only execute queries, mutations and subscription")
	}

	switch operation.GetOperation() {
	case ast.OperationTypeQuery:
		return schema.QueryType(), nil
	case ast.OperationTypeMutation:
		mutationType := schema.MutationType()
		if mutationType == nil || mutationType.PrivateName == "" {
			return nil, gqlerrors.NewError(
				"Schema is not configured for mutations",
				[]ast.Node{operation},
				"",
				nil,
				[]int{},
				nil,
			)
		}
		return mutationType, nil
	case ast.OperationTypeSubscription:
		subscriptionType := schema.SubscriptionType()
		if subscriptionType == nil || subscriptionType.PrivateName == "" {
			return nil, gqlerrors.NewError(
				"Schema is not configured for subscriptions",
				[]ast.Node{operation},
				"",
				nil,
				[]int{},
				nil,
			)
		}
		return subscriptionType, nil
	default:
		return nil, gqlerrors.NewError(
			"Can only execute queries, mutations and subscription",
			[]ast.Node{operation},
			"",
			nil,
			[]int{},
			nil,
		)
	}
}

// dethunkQueue is a structure that allows us to execute a classic breadth-first traversal.
type dethunkQueue struct {
	DethunkFuncs []func()
}

func (d *dethunkQueue) push(f func()) {
	d.DethunkFuncs = append(d.DethunkFuncs, f)
}

func (d *dethunkQueue) shift() func() {
	f := d.DethunkFuncs[0]
	d.DethunkFuncs = d.DethunkFuncs[1:]
	return f
}

// dethunkWithBreadthFirstTraversal performs a breadth-first descent of the map, calling any thunks
// in the map values and replacing each thunk with that thunk's return value. This parallels
// the reference graphql-js implementation, which calls Promise.all on thunks at each depth (which
// is an implicit parallel descent).
func dethunkMapWithBreadthFirstTraversal(finalResults map[string]interface{}) {
	dethunkQueue := &dethunkQueue{DethunkFuncs: []func(){}}
	dethunkMapBreadthFirst(finalResults, dethunkQueue)
	for len(dethunkQueue.DethunkFuncs) > 0 {
		f := dethunkQueue.shift()
		f()
	}
}

func dethunkMapBreadthFirst(m map[string]interface{}, dethunkQueue *dethunkQueue) {
	for k, v := range m {
		if f, ok := v.(func() interface{}); ok {
			m[k] = f()
		}
		switch val := m[k].(type) {
		case map[string]interface{}:
			dethunkQueue.push(func() { dethunkMapBreadthFirst(val, dethunkQueue) })
		case []interface{}:
			dethunkQueue.push(func() { dethunkListBreadthFirst(val, dethunkQueue) })
		}
	}
}

func dethunkListBreadthFirst(list []interface{}, dethunkQueue *dethunkQueue) {
	for i, v := range list {
		if f, ok := v.(func() interface{}); ok {
			list[i] = f()
		}
		switch val := list[i].(type) {
		case map[string]interface{}:
			dethunkQueue.push(func() { dethunkMapBreadthFirst(val, dethunkQueue) })
		case []interface{}:
			dethunkQueue.push(func() { dethunkListBreadthFirst(val, dethunkQueue) })
		}
	}
}

// dethunkMapDepthFirst performs a serial descent of the map, calling any thunks
// in the map values and replacing each thunk with that thunk's return value. This is needed
// to conform to the graphql-js reference implementation, which requires serial (depth-first)
// implementations for mutation selects.
func dethunkMapDepthFirst(m map[string]interface{}) {
	for k, v := range m {
		if f, ok := v.(func() interface{}); ok {
			m[k] = f()
		}
		switch val := m[k].(type) {
		case map[string]interface{}:
			dethunkMapDepthFirst(val)
		case []interface{}:
			dethunkListDepthFirst(val)
		}
	}
}

func dethunkListDepthFirst(list []interface{}) {
	for i, v := range list {
		if f, ok := v.(func() interface{}); ok {
			list[i] = f()
		}
		switch val := list[i].(type) {
		case map[string]interface{}:
			dethunkMapDepthFirst(val)
		case []interface{}:
			dethunkListDepthFirst(val)
		}
	}
}

type collectFieldsParams struct {
	ExeContext           *executionContext
	RuntimeType          *Object // previously known as OperationType
	SelectionSet         *ast.SelectionSet
	Fields               map[string][]*ast.Field
	VisitedFragmentNames map[string]bool
}

// Given a selectionSet, adds all of the fields in that selection to
// the passed in map of fields, and returns it at the end.
// CollectFields requires the "runtime type" of an object. For a field which
// returns and Interface or Union type, the "runtime type" will be the actual
// Object type returned by that field.
func collectFields(p collectFieldsParams) (fields map[string][]*ast.Field) {
	// overlying SelectionSet & Fields to fields
	if p.SelectionSet == nil {
		return p.Fields
	}
	fields = p.Fields
	if fields == nil {
		fields = map[string][]*ast.Field{}
	}
	if p.VisitedFragmentNames == nil {
		p.VisitedFragmentNames = map[string]bool{}
	}
	for _, iSelection := range p.SelectionSet.Selections {
		switch selection := iSelection.(type) {
		case *ast.Field:
			if !shouldIncludeNode(p.ExeContext, selection.Directives) {
				continue
			}
			name := getFieldEntryKey(selection)
			if _, ok := fields[name]; !ok {
				fields[name] = []*ast.Field{}
			}
			fields[name] = append(fields[name], selection)
		case *ast.InlineFragment:

			if !shouldIncludeNode(p.ExeContext, selection.Directives) ||
				!doesFragmentConditionMatch(p.ExeContext, selection, p.RuntimeType) {
				continue
			}
			innerParams := collectFieldsParams{
				ExeContext:           p.ExeContext,
				RuntimeType:          p.RuntimeType,
				SelectionSet:         selection.SelectionSet,
				Fields:               fields,
				VisitedFragmentNames: p.VisitedFragmentNames,
			}
			collectFields(innerParams)
		case *ast.FragmentSpread:
			fragName := ""
			if selection.Name != nil {
				fragName = selection.Name.Value
			}
			if visited, ok := p.VisitedFragmentNames[fragName]; (ok && visited) ||
				!shouldIncludeNode(p.ExeContext, selection.Directives) {
				continue
			}
			p.VisitedFragmentNames[fragName] = true
			fragment, hasFragment := p.ExeContext.Fragments[fragName]
			if !hasFragment {
				continue
			}

			if fragment, ok := fragment.(*ast.FragmentDefinition); ok {
				if !doesFragmentConditionMatch(p.ExeContext, fragment, p.RuntimeType) {
					continue
				}
				innerParams := collectFieldsParams{
					ExeContext:           p.ExeContext,
					RuntimeType:          p.RuntimeType,
					SelectionSet:         fragment.GetSelectionSet(),
					Fields:               fields,
					VisitedFragmentNames: p.VisitedFragmentNames,
				}
				collectFields(innerParams)
			}
		}
	}
	return fields
}

// Determines if a field should be included based on the @include and @skip
// directives, where @skip has higher precedence than @include.
func shouldIncludeNode(eCtx *executionContext, directives []*ast.Directive) bool {
	var (
		skipAST, includeAST *ast.Directive
		argValues           map[string]interface{}
	)
	for _, directive := range directives {
		if directive == nil || directive.Name == nil {
			continue
		}
		switch directive.Name.Value {
		case SkipDirective.Name:
			skipAST = directive
		case IncludeDirective.Name:
			includeAST = directive
		}
	}
	// precedence: skipAST > includeAST
	if skipAST != nil {
		argValues = getArgumentValues(SkipDirective.Args, skipAST.Arguments, eCtx.VariableValues)
		if skipIf, ok := argValues["if"].(bool); ok && skipIf {
			return false // excluded selectionSet's fields
		}
	}
	if includeAST != nil {
		argValues = getArgumentValues(IncludeDirective.Args, includeAST.Arguments, eCtx.VariableValues)
		if includeIf, ok := argValues["if"].(bool); ok && !includeIf {
			return false // excluded selectionSet's fields
		}
	}
	return true
}

// Determines if a fragment is applicable to the given type.
func doesFragmentConditionMatch(eCtx *executionContext, fragment ast.Node, ttype *Object) bool {

	switch fragment := fragment.(type) {
	case *ast.FragmentDefinition:
		typeConditionAST := fragment.TypeCondition
		if typeConditionAST == nil {
			return true
		}
		conditionalType, err := typeFromAST(eCtx.Schema, typeConditionAST)
		if err != nil {
			return false
		}
		if conditionalType == ttype {
			return true
		}
		if conditionalType.Name() == ttype.Name() {
			return true
		}
		if conditionalType, ok := conditionalType.(*Interface); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
		if conditionalType, ok := conditionalType.(*Union); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
	case *ast.InlineFragment:
		typeConditionAST := fragment.TypeCondition
		if typeConditionAST == nil {
			return true
		}
		conditionalType, err := typeFromAST(eCtx.Schema, typeConditionAST)
		if err != nil {
			return false
		}
		if conditionalType == ttype {
			return true
		}
		if conditionalType.Name() == ttype.Name() {
			return true
		}
		if conditionalType, ok := conditionalType.(*Interface); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
		if conditionalType, ok := conditionalType.(*Union); ok {
			return eCtx.Schema.IsPossibleType(conditionalType, ttype)
		}
	}

	return false
}

// Implements the logic to compute the key of a given field’s entry
func getFieldEntryKey(node *ast.Field) string {

	if node.Alias != nil && node.Alias.Value != "" {
		return node.Alias.Value
	}
	if node.Name != nil && node.Name.Value != "" {
		return node.Name.Value
	}
	return ""
}

func handleFieldError(r interface{}, fieldNodes []ast.Node, path *ResponsePath, returnType Output, eCtx *executionContext) {
	err := NewLocatedErrorWithPath(r, fieldNodes, eCtx.errorPathArray(path))
	// send panic upstream
	if _, ok := returnType.(*NonNull); ok {
		panic(err)
	}
	eCtx.Errors = append(eCtx.Errors, gqlerrors.FormatError(err))
}

// completeLeafValue complete a leaf value (Scalar / Enum) by serializing to a valid value, returning nil if serialization is not possible.
func completeLeafValue(returnType Leaf, result interface{}) interface{} {
	serializedResult := returnType.Serialize(result)
	if isNullish(serializedResult) {
		return nil
	}
	return serializedResult
}

// defaultResolveTypeFn If a resolveType function is not given, then a default resolve behavior is
// used which tests each possible type for the abstract type by calling
// isTypeOf for the object being coerced, returning the first type that matches.
func defaultResolveTypeFn(p ResolveTypeParams, abstractType Abstract) *Object {
	possibleTypes := p.Info.Schema.PossibleTypes(abstractType)
	for _, possibleType := range possibleTypes {
		if possibleType.IsTypeOf == nil {
			continue
		}
		isTypeOfParams := IsTypeOfParams{
			Value:   p.Value,
			Info:    p.Info,
			Context: p.Context,
		}
		if res := possibleType.IsTypeOf(isTypeOfParams); res {
			return possibleType
		}
	}
	return nil
}

// FieldResolver is used in DefaultResolveFn when the the source value implements this interface.
type FieldResolver interface {
	// Resolve resolves the value for the given ResolveParams. It has the same semantics as FieldResolveFn.
	Resolve(p ResolveParams) (interface{}, error)
}

// DefaultResolveFn If a resolve function is not given, then a default resolve behavior is used
// which takes the property of the source object of the same name as the field
// and returns it as the result, or if it's a function, returns the result
// of calling that function.
func DefaultResolveFn(p ResolveParams) (interface{}, error) {
	sourceVal := reflect.ValueOf(p.Source)
	// Check if value implements 'Resolver' interface
	if resolver, ok := sourceVal.Interface().(FieldResolver); ok {
		return resolver.Resolve(p)
	}

	// try to resolve p.Source as a struct
	if sourceVal.IsValid() && sourceVal.Type().Kind() == reflect.Ptr {
		sourceVal = sourceVal.Elem()
	}
	if !sourceVal.IsValid() {
		return nil, nil
	}

	if sourceVal.Type().Kind() == reflect.Struct {
		for i := 0; i < sourceVal.NumField(); i++ {
			valueField := sourceVal.Field(i)
			typeField := sourceVal.Type().Field(i)
			// try matching the field name first
			if strings.EqualFold(typeField.Name, p.Info.FieldName) {
				return valueField.Interface(), nil
			}
			tag := typeField.Tag
			checkTag := func(tagName string) bool {
				t := tag.Get(tagName)
				tOptions := strings.Split(t, ",")
				if len(tOptions) == 0 {
					return false
				}
				if tOptions[0] != p.Info.FieldName {
					return false
				}
				return true
			}
			if checkTag("json") || checkTag("graphql") {
				return valueField.Interface(), nil
			} else {
				continue
			}
		}
		return nil, nil
	}

	// try p.Source as a map[string]interface
	if sourceMap, ok := p.Source.(map[string]interface{}); ok {
		property := sourceMap[p.Info.FieldName]
		val := reflect.ValueOf(property)
		if val.IsValid() && val.Type().Kind() == reflect.Func {
			// try type casting the func to the most basic func signature
			// for more complex signatures, user have to define ResolveFn
			if propertyFn, ok := property.(func() interface{}); ok {
				return propertyFn(), nil
			}
		}
		return property, nil
	}

	// Try accessing as map via reflection
	if r := reflect.ValueOf(p.Source); r.Kind() == reflect.Map && r.Type().Key().Kind() == reflect.String {
		// Convert the field name to the map's key type to support custom
		// string-based key types (e.g., type MyKey string).
		key := reflect.ValueOf(p.Info.FieldName).Convert(r.Type().Key())
		val := r.MapIndex(key)
		if val.IsValid() {
			property := val.Interface()
			if val.Type().Kind() == reflect.Func {
				// try type casting the func to the most basic func signature
				// for more complex signatures, user have to define ResolveFn
				if propertyFn, ok := property.(func() interface{}); ok {
					return propertyFn(), nil
				}
			}
			return property, nil
		}
	}

	// last resort, return nil
	return nil, nil
}

// This method looks up the field on the given type definition.
// It has special casing for the two introspection fields, __schema
// and __typename. __typename is special because it can always be
// queried as a field, even in situations where no other fields
// are allowed, like on a Union. __schema could get automatically
// added to the query type, but that would require mutating type
// definitions, which would cause issues.
func getFieldDef(schema Schema, parentType *Object, fieldName string) *FieldDefinition {

	if parentType == nil {
		return nil
	}

	if fieldName == SchemaMetaFieldDef.Name &&
		schema.QueryType() == parentType {
		return SchemaMetaFieldDef
	}
	if fieldName == TypeMetaFieldDef.Name &&
		schema.QueryType() == parentType {
		return TypeMetaFieldDef
	}
	if fieldName == TypeNameMetaFieldDef.Name {
		return TypeNameMetaFieldDef
	}
	return parentType.Fields()[fieldName]
}
