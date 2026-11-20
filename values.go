package graphql

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/IodeSystems/graphql-go/v2/gqlerrors"
	"github.com/IodeSystems/graphql-go/v2/language/ast"
	"github.com/IodeSystems/graphql-go/v2/language/kinds"
	"github.com/IodeSystems/graphql-go/v2/language/printer"
)

// Prepares an object map of variableValues of the correct type based on the
// provided variable definitions and arbitrary input. If the input cannot be
// parsed to match the variable definitions, a GraphQLError will be returned.
func getVariableValues(
	schema Schema,
	definitionASTs []*ast.VariableDefinition,
	inputs map[string]interface{}) (_ map[string]interface{}, err error) {
	// Custom scalar ParseValue may panic; report it as an error so
	// callers outside a recovering goroutine (ExecutePlanAppend) survive.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	// Fast path: no variable definitions on the operation. Skip the
	// map alloc; downstream readers (populateArgumentValues, skip
	// predicates, ResolveInfo.VariableValues consumers) all tolerate
	// nil — Go map reads on nil return the zero value.
	if len(definitionASTs) == 0 {
		return nil, nil
	}
	values := map[string]interface{}{}
	for _, defAST := range definitionASTs {
		if defAST == nil || defAST.Variable == nil || defAST.Variable.Name == nil {
			continue
		}
		varName := defAST.Variable.Name.Value
		input, hasValue := inputs[varName]
		varValue, present, err := getVariableValue(schema, defAST, input, hasValue)
		if err != nil {
			return values, err
		}
		// A variable that was not provided and has no default is left
		// out, so an argument it feeds is absent rather than null.
		if present {
			values[varName] = varValue
		}
	}
	return values, nil
}

// Prepares an object map of argument values given a list of argument
// definitions and list of argument AST nodes.
func getArgumentValues(
	argDefs []*Argument, argASTs []*ast.Argument,
	variableValues map[string]interface{}) map[string]interface{} {
	results := map[string]interface{}{}
	populateArgumentValues(results, argDefs, argASTs, variableValues)
	return results
}

// populateArgumentValues writes the resolved argument values into dst.
// dst is left empty when argDefs is empty or every arg's value is
// nullish. Used by the executor to write directly into a pooled args
// map (avoiding the per-field map alloc that getArgumentValues would
// otherwise return). The argASTs lookup is open-coded as a linear
// scan; typical fields have 0-3 args so the O(N×M) is comfortably
// under any hashed-lookup overhead.
func populateArgumentValues(
	dst map[string]interface{},
	argDefs []*Argument, argASTs []*ast.Argument,
	variableValues map[string]interface{},
) {
	for _, argDef := range argDefs {
		var value ast.Value
		for _, argAST := range argASTs {
			if argAST.Name != nil && argAST.Name.Value == argDef.PrivateName {
				value = argAST.Value
				break
			}
		}
		if isNullLiteral(value) {
			dst[argDef.PrivateName] = nil
			continue
		}
		if v, ok := value.(*ast.Variable); ok && v.Name != nil {
			// A provided variable passes its value through, null
			// included. Null into a non-null argument (validation lets
			// it through when the variable or the argument has a
			// default) falls back to the argument default, or is left
			// out, as before.
			if val, present := variableValues[v.Name.Value]; present {
				if _, nonNull := argDef.Type.(*NonNull); val != nil || !nonNull {
					dst[argDef.PrivateName] = val
					continue
				}
			}
			if def, ok := defaultValue(argDef.DefaultValue); ok {
				dst[argDef.PrivateName] = def
			}
			continue
		}
		tmp := valueFromAST(value, argDef.Type, variableValues)
		if isNullish(tmp) {
			if def, ok := defaultValue(argDef.DefaultValue); ok {
				dst[argDef.PrivateName] = def
			}
			continue
		}
		dst[argDef.PrivateName] = tmp
	}
}

// ExplicitNull, set as an Argument's or InputObjectField's
// DefaultValue, declares a default of null (`arg: Int = null`). A nil
// DefaultValue means no default. When the default applies, the
// resolver sees the key in ResolveParams.Args with a nil value, the
// same as for an explicit `null` literal; introspection reports the
// defaultValue as "null".
var ExplicitNull interface{} = explicitNull{}

type explicitNull struct{}

func (explicitNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// defaultValue maps a configured DefaultValue to the Go value it
// supplies. ok is false when there is no default.
func defaultValue(def interface{}) (value interface{}, ok bool) {
	if _, isNull := def.(explicitNull); isNull {
		return nil, true
	}
	if isNullish(def) {
		return nil, false
	}
	return def, true
}

// hasNonNullDefault reports whether def supplies a non-null value, so
// that a non-null position may be omitted. A null default
// (ExplicitNull) does not.
func hasNonNullDefault(def interface{}) bool {
	_, isNull := def.(explicitNull)
	return !isNull && !isNullish(def)
}

// isNullLiteral reports whether v is the `null` literal. A nil v is an
// absent value, not a null one.
func isNullLiteral(v ast.Value) bool {
	_, ok := v.(*ast.NullValue)
	return ok
}

// Given a variable definition, and any value of input, return a value which
// adheres to the variable definition, or throw an error. hasValue says
// whether the request supplied the variable at all; present says
// whether the variable has a value (possibly nil, for null) after
// defaults (spec CoerceVariableValues).
func getVariableValue(schema Schema, definitionAST *ast.VariableDefinition, input interface{}, hasValue bool) (value interface{}, present bool, err error) {
	ttype, err := typeFromAST(schema, definitionAST.Type)
	if err != nil {
		return nil, false, err
	}
	variable := definitionAST.Variable

	if ttype == nil || !IsInputType(ttype) {
		return "", false, gqlerrors.NewError(
			fmt.Sprintf(`Variable "$%v" expected value of type `+
				`"%v" which cannot be used as an input type.`, variable.Name.Value, printer.Print(definitionAST.Type)),
			[]ast.Node{definitionAST},
			"",
			nil,
			[]int{},
			nil,
		)
	}

	if !hasValue && definitionAST.DefaultValue != nil {
		return valueFromAST(definitionAST.DefaultValue, ttype, nil), true, nil
	}
	isValid, messages := isValidInputValue(input, ttype)
	if isValid {
		if !hasValue {
			return nil, false, nil
		}
		return coerceValue(ttype, input), true, nil
	}
	if isNullish(input) {
		return "", false, gqlerrors.NewError(
			fmt.Sprintf(`Variable "$%v" of required type `+
				`"%v" was not provided.`, variable.Name.Value, printer.Print(definitionAST.Type)),
			[]ast.Node{definitionAST},
			"",
			nil,
			[]int{},
			nil,
		)
	}
	// convert input interface into string for error message
	bts, _ := json.Marshal(input)
	var (
		inputStr = string(bts)
		msg      string
	)
	if len(messages) > 0 {
		msg = "\n" + strings.Join(messages, "\n")
	}

	return "", false, gqlerrors.NewError(
		fmt.Sprintf(`Variable "$%v" got invalid value `+
			`%v.%v`, variable.Name.Value, inputStr, msg),
		[]ast.Node{definitionAST},
		"",
		nil,
		[]int{},
		nil,
	)
}

// Given a type and any value, return a runtime value coerced to match the type.
func coerceValue(ttype Input, value interface{}) interface{} {
	if isNullish(value) {
		return nil
	}
	switch ttype := ttype.(type) {
	case *NonNull:
		return coerceValue(ttype.OfType, value)
	case *List:
		var values = []interface{}{}
		valType := reflect.ValueOf(value)
		if valType.Kind() == reflect.Slice {
			for i := 0; i < valType.Len(); i++ {
				val := valType.Index(i).Interface()
				values = append(values, coerceValue(ttype.OfType, val))
			}
			return values
		}
		return append(values, coerceValue(ttype.OfType, value))
	case *InputObject:
		var obj = map[string]interface{}{}
		valueMap, _ := value.(map[string]interface{})
		if valueMap == nil {
			valueMap = map[string]interface{}{}
		}

		for name, field := range ttype.Fields() {
			value, presentInValueMap := valueMap[name]
			if presentInValueMap && value == nil {
				// Explicit null: kept, and not replaced by the
				// field's default.
				obj[name] = nil
				continue
			}
			fieldValue := coerceValue(field.Type, value)
			if isNullish(fieldValue) {
				def, hasDefault := defaultValue(field.DefaultValue)
				if hasDefault || presentInValueMap {
					obj[name] = def
				}
				continue
			}
			obj[name] = fieldValue
		}
		return obj
	case *Scalar:
		if parsed := ttype.ParseValue(value); !isNullish(parsed) {
			return parsed
		}
	case *Enum:
		if parsed := ttype.ParseValue(value); !isNullish(parsed) {
			return parsed
		}
	}

	return nil
}

// graphql-js/src/utilities.js`
// TODO: figure out where to organize utils
// TODO: change to *Schema
func typeFromAST(schema Schema, inputTypeAST ast.Type) (Type, error) {
	switch inputTypeAST := inputTypeAST.(type) {
	case *ast.List:
		innerType, err := typeFromAST(schema, inputTypeAST.Type)
		if err != nil {
			return nil, err
		}
		return NewList(innerType), nil
	case *ast.NonNull:
		innerType, err := typeFromAST(schema, inputTypeAST.Type)
		if err != nil {
			return nil, err
		}
		return NewNonNull(innerType), nil
	case *ast.Named:
		nameValue := ""
		if inputTypeAST.Name != nil {
			nameValue = inputTypeAST.Name.Value
		}
		ttype := schema.Type(nameValue)
		return ttype, nil
	default:
		return nil, invariant(inputTypeAST.GetKind() == kinds.Named, "Must be a named type.")
	}
}

// isValidInputValue alias isValidJSValue
// Given a value and a GraphQL type, determine if the value will be
// accepted for that type. This is primarily useful for validating the
// runtime values of query variables.
func isValidInputValue(value interface{}, ttype Input) (bool, []string) {
	if isNullish(value) {
		if ttype, ok := ttype.(*NonNull); ok {
			if ttype.OfType.Name() != "" {
				return false, []string{fmt.Sprintf(`Expected "%v!", found null.`, ttype.OfType.Name())}
			}
			return false, []string{"Expected non-null value, found null."}
		}
		return true, nil
	}
	switch ttype := ttype.(type) {
	case *NonNull:
		return isValidInputValue(value, ttype.OfType)
	case *List:
		valType := reflect.ValueOf(value)
		if valType.Kind() == reflect.Ptr {
			valType = valType.Elem()
		}
		if valType.Kind() == reflect.Slice {
			messagesReduce := []string{}
			for i := 0; i < valType.Len(); i++ {
				val := valType.Index(i).Interface()
				_, messages := isValidInputValue(val, ttype.OfType)
				for _, message := range messages {
					messagesReduce = append(messagesReduce, fmt.Sprintf(`In element #%v: %v`, i, message))
				}
			}
			return (len(messagesReduce) == 0), messagesReduce
		}
		return isValidInputValue(value, ttype.OfType)

	case *InputObject:
		messagesReduce := []string{}

		valueMap, ok := value.(map[string]interface{})
		if !ok {
			return false, []string{fmt.Sprintf(`Expected "%v", found not an object.`, ttype.Name())}
		}
		fields := ttype.Fields()

		// to ensure stable order of field evaluation
		fieldNames := []string{}
		valueMapFieldNames := []string{}

		for fieldName := range fields {
			fieldNames = append(fieldNames, fieldName)
		}
		sort.Strings(fieldNames)

		for fieldName := range valueMap {
			valueMapFieldNames = append(valueMapFieldNames, fieldName)
		}
		sort.Strings(valueMapFieldNames)

		// Ensure every provided field is defined.
		for _, fieldName := range valueMapFieldNames {
			if _, ok := fields[fieldName]; !ok {
				messagesReduce = append(messagesReduce, fmt.Sprintf(`In field "%v": Unknown field.`, fieldName))
			}
		}

		// Ensure every defined field is valid.
		for _, fieldName := range fieldNames {
			fieldValue, present := valueMap[fieldName]
			if !present && hasNonNullDefault(fields[fieldName].DefaultValue) {
				// Omitted with a default: the default applies.
				continue
			}
			_, messages := isValidInputValue(fieldValue, fields[fieldName].Type)
			if messages != nil {
				for _, message := range messages {
					messagesReduce = append(messagesReduce, fmt.Sprintf(`In field "%v": %v`, fieldName, message))
				}
			}
		}
		return (len(messagesReduce) == 0), messagesReduce
	case *Scalar:
		if parsedVal := ttype.ParseValue(value); isNullish(parsedVal) {
			return false, []string{fmt.Sprintf(`Expected type "%v", found "%v".`, ttype.Name(), value)}
		}
	case *Enum:
		if parsedVal := ttype.ParseValue(value); isNullish(parsedVal) {
			return false, []string{fmt.Sprintf(`Expected type "%v", found "%v".`, ttype.Name(), value)}
		}
	}

	return true, nil
}

// Returns true if a value is null, undefined, or NaN.
func isNullish(src interface{}) bool {
	if src == nil {
		return true
	}
	value := reflect.ValueOf(src)
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return true
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		// if src is ptr type and len(string)=0, it returns false
		if !value.IsValid() {
			return true
		}
	case reflect.Int:
		return math.IsNaN(float64(value.Int()))
	case reflect.Float32, reflect.Float64:
		return math.IsNaN(float64(value.Float()))
	}
	return false
}

// isNullishFor is isNullish for a resolved output value of type t. For
// object, interface and union types a nil map is also null, like a nil
// pointer. Scalars keep their value, since a custom scalar may accept a
// nil map. Nil slices are not null: a nil slice for a list type is an
// empty list (TestLists_ValueMayBeNilPointer).
func isNullishFor(t Type, src interface{}) bool {
	if isNullish(src) {
		return true
	}
	switch t.(type) {
	case *Object, *Interface, *Union:
	default:
		return false
	}
	switch v := src.(type) {
	case map[string]interface{}:
		return v == nil
	case string, int, int64, float64, bool:
		return false
	}
	rv := reflect.ValueOf(src)
	return rv.Kind() == reflect.Map && rv.IsNil()
}

// Returns true if src is a slice or an array
func isIterable(src interface{}) bool {
	if src == nil {
		return false
	}
	t := reflect.TypeOf(src)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Kind() == reflect.Slice || t.Kind() == reflect.Array
}

/**
 * Produces a value given a GraphQL Value AST.
 *
 * A GraphQL type must be provided, which will be used to interpret different
 * GraphQL Value literals.
 *
 * | GraphQL Value        | JSON Value    |
 * | -------------------- | ------------- |
 * | Input Object         | Object        |
 * | List                 | Array         |
 * | Boolean              | Boolean       |
 * | String / Enum Value  | String        |
 * | Int / Float          | Number        |
 * | Null                 | nil           |
 *
 * nil is also returned for an absent (nil) valueAST and for a literal
 * that does not coerce; callers that must tell `null` from absent
 * check isNullLiteral first.
 */
func valueFromAST(valueAST ast.Value, ttype Input, variables map[string]interface{}) interface{} {
	if valueAST == nil || isNullLiteral(valueAST) {
		return nil
	}
	// precedence: value > type
	if valueAST, ok := valueAST.(*ast.Variable); ok {
		if valueAST.Name == nil || variables == nil {
			return nil
		}
		// Note: we're not doing any checking that this variable is correct. We're
		// assuming that this query has been validated and the variable usage here
		// is of the correct type.
		return variables[valueAST.Name.Value]
	}
	switch ttype := ttype.(type) {
	case *NonNull:
		return valueFromAST(valueAST, ttype.OfType, variables)
	case *List:
		values := []interface{}{}
		if valueAST, ok := valueAST.(*ast.ListValue); ok {
			for _, itemAST := range valueAST.Values {
				values = append(values, valueFromAST(itemAST, ttype.OfType, variables))
			}
			return values
		}
		return append(values, valueFromAST(valueAST, ttype.OfType, variables))
	case *InputObject:
		var (
			ok bool
			ov *ast.ObjectValue
			of *ast.ObjectField
		)
		if ov, ok = valueAST.(*ast.ObjectValue); !ok {
			return nil
		}
		fieldASTs := map[string]*ast.ObjectField{}
		for _, of = range ov.Fields {
			if of == nil || of.Name == nil {
				continue
			}
			fieldASTs[of.Name.Value] = of
		}
		obj := map[string]interface{}{}
		for name, field := range ttype.Fields() {
			if of, ok = fieldASTs[name]; ok {
				if isNullLiteral(of.Value) {
					obj[name] = nil
					continue
				}
				if v, isVar := of.Value.(*ast.Variable); isVar && v.Name != nil {
					// Provided (null included) passes through; not
					// provided falls back to the field default.
					if val, present := variables[v.Name.Value]; present {
						obj[name] = val
						continue
					}
					if def, hasDefault := defaultValue(field.DefaultValue); hasDefault {
						obj[name] = def
					}
					continue
				}
				if value := valueFromAST(of.Value, field.Type, variables); !isNullish(value) {
					obj[name] = value
				}
				continue
			}
			if def, hasDefault := defaultValue(field.DefaultValue); hasDefault {
				obj[name] = def
			}
		}
		return obj
	case *Scalar:
		return ttype.ParseLiteral(valueAST)
	case *Enum:
		return ttype.ParseLiteral(valueAST)
	}

	return nil
}

func invariant(condition bool, message string) error {
	if !condition {
		return gqlerrors.NewFormattedError(message)
	}
	return nil
}

func invariantf(condition bool, format string, a ...interface{}) error {
	if !condition {
		return gqlerrors.NewFormattedError(fmt.Sprintf(format, a...))
	}
	return nil
}
