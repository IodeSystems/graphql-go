package graphql

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Result coercion (Serialize) of the built-in scalars, per the spec
// (Type System › Scalars › Result Coercion) and graphql-js: a value is
// converted only when the conversion loses nothing (2.0 → 2 and "2" → 2
// for Int, true → "true" for String, 7 → "7" for ID). A value that
// cannot be represented is an error, never a silent truncation or null.
//
// Each serializeX returns (nil, nil) for a nil pointer (the value is
// null), the coerced value, or an error with the graphql-js message.
// The executors call serializeX through Scalar.serializeStrict and turn
// the error into a field error. The public Serialize (coerceX) has no
// error channel and returns nil for an invalid value.

// newBuiltinScalar is NewScalar for a built-in scalar whose result
// coercion can fail with an error.
func newBuiltinScalar(serialize func(interface{}) (interface{}, error), config ScalarConfig) *Scalar {
	st := NewScalar(config)
	st.serializeStrict = serialize
	return st
}

func coerceInt(value interface{}) interface{} {
	v, err := serializeInt(value)
	if err != nil {
		return nil
	}
	return v
}

func coerceFloat(value interface{}) interface{} {
	v, err := serializeFloat(value)
	if err != nil {
		return nil
	}
	return v
}

func coerceString(value interface{}) interface{} {
	v, err := serializeString(value)
	if err != nil {
		return nil
	}
	return v
}

func coerceBool(value interface{}) interface{} {
	v, err := serializeBool(value)
	if err != nil {
		return nil
	}
	return v
}

func coerceID(value interface{}) interface{} {
	v, err := serializeID(value)
	if err != nil {
		return nil
	}
	return v
}

func intNonIntegerError(value interface{}) error {
	return fmt.Errorf("Int cannot represent non-integer value: %s", inspectValue(value))
}

func intRangeError(value interface{}) error {
	return fmt.Errorf("Int cannot represent non 32-bit signed integer value: %s", inspectValue(value))
}

// serializeInt is Int result coercion: an integer in 32-bit signed
// range, an integral float (2.0), a numeric string or json.Number that
// denotes such an integer, or a boolean (1 / 0, as in graphql-js).
func serializeInt(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case int:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return nil, intRangeError(value)
		}
		return value, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return nil, intRangeError(value)
		}
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint:
		if v > math.MaxInt32 {
			return nil, intRangeError(value)
		}
		return int(v), nil
	case uint32:
		if v > math.MaxInt32 {
			return nil, intRangeError(value)
		}
		return int(v), nil
	case uint64:
		if v > math.MaxInt32 {
			return nil, intRangeError(value)
		}
		return int(v), nil
	case float64:
		return serializeIntFromFloat(value, v)
	case float32:
		return serializeIntFromFloat(value, float64(v))
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		if !isJSONNumberSyntax(v) {
			return nil, intNonIntegerError(value)
		}
		return serializeIntFromNumber(value, v)
	case json.Number:
		return serializeIntFromNumber(value, string(v))
	case nil:
		return nil, nil
	}
	return serializeReflect(value, serializeInt, intNonIntegerError)
}

func serializeIntFromFloat(orig interface{}, f float64) (interface{}, error) {
	if math.IsInf(f, 0) || f != math.Trunc(f) {
		return nil, intNonIntegerError(orig) // also NaN
	}
	if f < math.MinInt32 || f > math.MaxInt32 {
		return nil, intRangeError(orig)
	}
	return int(f), nil
}

func serializeIntFromNumber(orig interface{}, s string) (interface{}, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < math.MinInt32 || n > math.MaxInt32 {
			return nil, intRangeError(orig)
		}
		return int(n), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Junk, or out of float64 range ("1e400"): neither is an integer.
		return nil, intNonIntegerError(orig)
	}
	return serializeIntFromFloat(orig, f)
}

func floatError(value interface{}) error {
	return fmt.Errorf("Float cannot represent non numeric value: %s", inspectValue(value))
}

// serializeFloat is Float result coercion: a finite number, a numeric
// string or json.Number that parses to a finite float, or a boolean
// (1 / 0, as in graphql-js). NaN and ±Inf are errors. A float32 stays
// float32.
func serializeFloat(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, floatError(value)
		}
		return value, nil
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, floatError(value)
		}
		return value, nil
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case bool:
		if v {
			return 1.0, nil
		}
		return 0.0, nil
	case string:
		if !isJSONNumberSyntax(v) {
			return nil, floatError(value)
		}
		return serializeFloatFromNumber(value, v)
	case json.Number:
		return serializeFloatFromNumber(value, string(v))
	case nil:
		return nil, nil
	}
	return serializeReflect(value, serializeFloat, floatError)
}

func serializeFloatFromNumber(orig interface{}, s string) (interface{}, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, floatError(orig)
	}
	return f, nil
}

func stringError(value interface{}) error {
	return fmt.Errorf("String cannot represent value: %s", inspectValue(value))
}

// serializeString is String result coercion: a string, a boolean
// ("true" / "false"), an integer, a finite float (formatted as
// graphql-js formats numbers), a json.Number (verbatim), or a
// fmt.Stringer. Anything else (maps, slices, structs, NaN) is an error.
func serializeString(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return value, nil
	case *string:
		if v == nil {
			return nil, nil
		}
		return *v, nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, stringError(value)
		}
		return formatJSNumber(v, 64), nil
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, stringError(value)
		}
		return formatJSNumber(float64(v), 32), nil
	case json.Number:
		return string(v), nil
	case fmt.Stringer:
		return serializeStringer(v)
	case nil:
		return nil, nil
	}
	return serializeReflect(value, serializeString, stringError)
}

func boolError(value interface{}) error {
	return fmt.Errorf("Boolean cannot represent a non boolean value: %s", inspectValue(value))
}

// serializeBool is Boolean result coercion: a boolean, a finite number
// (non-zero is true, as in graphql-js), or the string "true" / "false".
// Other strings are errors.
func serializeBool(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case bool:
		return value, nil
	case *bool:
		if v == nil {
			return nil, nil
		}
		return *v, nil
	case int:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case uint64:
		return v != 0, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, boolError(value)
		}
		return v != 0, nil
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, boolError(value)
		}
		return v != 0, nil
	case string:
		// "true" and "false" convert without loss. graphql-js rejects
		// every string; this fork keeps the lossless pair.
		switch v {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, boolError(value)
	case json.Number:
		f, err := v.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, boolError(value)
		}
		return f != 0, nil
	case nil:
		return nil, nil
	}
	return serializeReflect(value, serializeBool, boolError)
}

func idError(value interface{}) error {
	return fmt.Errorf("ID cannot represent value: %s", inspectValue(value))
}

// serializeID is ID result coercion: a string, an integer or an
// integral float (as its decimal string), a json.Number denoting an
// integer, or a fmt.Stringer. Booleans and fractional numbers are
// errors.
func serializeID(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return value, nil
	case *string:
		if v == nil {
			return nil, nil
		}
		return *v, nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		if math.IsInf(v, 0) || v != math.Trunc(v) {
			return nil, idError(value) // also NaN
		}
		return formatJSNumber(v, 64), nil
	case float32:
		if math.IsInf(float64(v), 0) || v != float32(math.Trunc(float64(v))) {
			return nil, idError(value)
		}
		return formatJSNumber(float64(v), 32), nil
	case json.Number:
		if isJSONIntegerSyntax(string(v)) {
			return string(v), nil
		}
		f, err := v.Float64()
		if err != nil || math.IsInf(f, 0) || f != math.Trunc(f) {
			return nil, idError(value)
		}
		return formatJSNumber(f, 64), nil
	case bool:
		return nil, idError(value)
	case fmt.Stringer:
		return serializeStringer(v)
	case nil:
		return nil, nil
	}
	return serializeReflect(value, serializeID, idError)
}

// serializeStringer returns s.String(), or null for a nil pointer
// (whose String method may dereference it).
func serializeStringer(s fmt.Stringer) (interface{}, error) {
	if rv := reflect.ValueOf(s); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return nil, nil
	}
	return s.String(), nil
}

// serializeReflect handles the values the type switches above do not
// name: pointers (dereferenced; nil is null) and named types whose
// kind is a bool, number or string (type Count int), converted to
// bool, int64, uint64, float32, float64 or string, which every
// serializeX names in its switch (so this never recurses on itself).
// Anything else is rejected with invalid(value).
func serializeReflect(value interface{}, serialize func(interface{}) (interface{}, error), invalid func(interface{}) error) (interface{}, error) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Ptr:
		if rv.IsNil() {
			return nil, nil
		}
		return serialize(rv.Elem().Interface())
	case reflect.Bool:
		return serialize(rv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return serialize(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return serialize(rv.Uint())
	case reflect.Float32:
		return serialize(float32(rv.Float()))
	case reflect.Float64:
		return serialize(rv.Float())
	case reflect.String:
		return serialize(rv.String())
	}
	return nil, invalid(value)
}

// formatJSNumber formats f as JavaScript's Number.prototype.toString
// does: shortest round-trip digits, plain notation for 1e-6 <= |f| <
// 1e21, exponent notation ("1e+21", "1e-7") outside it. bitSize 32
// keeps a float32's shortest form.
func formatJSNumber(f float64, bitSize int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	abs := math.Abs(f)
	if abs == 0 || (abs >= 1e-6 && abs < 1e21) {
		return strconv.FormatFloat(f, 'f', -1, bitSize)
	}
	s := strconv.FormatFloat(f, 'e', -1, bitSize)
	// Go writes a two-digit exponent ("1e-07"); JavaScript does not.
	if i := strings.IndexByte(s, 'e'); i >= 0 && i+3 < len(s) && s[i+2] == '0' {
		s = s[:i+2] + s[i+3:]
	}
	return s
}

// inspectValue renders a rejected value for an error message, close to
// graphql-js inspect(): strings quoted, numbers in JavaScript form,
// pointers dereferenced.
func inspectValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(v)
	case json.Number:
		return string(v)
	case float64:
		return formatJSNumber(v, 64)
	case float32:
		return formatJSNumber(float64(v), 32)
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Ptr:
		if rv.IsNil() {
			return "null"
		}
		return inspectValue(rv.Elem().Interface())
	case reflect.String:
		return strconv.Quote(rv.String())
	case reflect.Float32, reflect.Float64:
		return formatJSNumber(rv.Float(), rv.Type().Bits())
	}
	return fmt.Sprintf("%v", value)
}
