package graphql

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/IodeSystems/graphql-go/v3/language/ast"
)

// parseIntValue is Int input coercion (variables; spec: Scalars › Int ›
// Input Coercion). It accepts what converts to a 32-bit signed integer
// without loss: an integral number in range, including an integral
// float such as JSON 2.0 and a json.Number, or a string in JSON number
// syntax that denotes such an integer ("2", "-3", "2.0", "1e3"). A
// fractional number, an out-of-range number, a boolean or any other
// string is rejected (nil), never truncated or converted. Literals in
// the query text stay spec-strict (ParseLiteral).
func parseIntValue(value interface{}) interface{} {
	switch value := value.(type) {
	case bool, *bool:
		return nil
	case string:
		if !isJSONNumberSyntax(value) {
			return nil
		}
		return parseIntValue(json.Number(value))
	case *string:
		if value == nil {
			return nil
		}
		return parseIntValue(*value)
	case float32:
		return parseIntValue(float64(value))
	case *float32:
		if value == nil {
			return nil
		}
		return parseIntValue(float64(*value))
	case float64:
		if value != math.Trunc(value) {
			return nil // fractional, NaN or ±Inf
		}
		return coerceInt(value)
	case *float64:
		if value == nil {
			return nil
		}
		return parseIntValue(*value)
	case json.Number:
		// Integral numbers only, as for float64; "1e3" and "2.0" are
		// integral, "2.5" is not.
		if n, err := value.Int64(); err == nil {
			return coerceInt(n)
		}
		f, err := value.Float64()
		if err != nil {
			return nil
		}
		return parseIntValue(f)
	}
	return coerceInt(value)
}

// isJSONNumberSyntax reports whether s is a number in JSON syntax
// (RFC 8259 §6): an optional minus, an integer part without leading
// zeros, an optional fraction and an optional exponent. Whitespace, a
// leading plus, hex, underscores, "NaN" and "Inf" are not numbers.
func isJSONNumberSyntax(s string) bool {
	i, n := 0, len(s)
	if i < n && s[i] == '-' {
		i++
	}
	switch {
	case i < n && s[i] == '0':
		i++
	case i < n && s[i] >= '1' && s[i] <= '9':
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < n && s[i] == '.' {
		i++
		start := i
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < n && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == n
}

// isJSONIntegerSyntax reports whether s is an optional minus followed
// by digits only, with no leading zero.
func isJSONIntegerSyntax(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i == len(s) || (s[i] == '0' && i+1 != len(s)) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Int is the GraphQL Integer type definition.
var Int = newBuiltinScalar(serializeInt, ScalarConfig{
	Name: "Int",
	Description: "The `Int` scalar type represents non-fractional signed whole numeric " +
		"values. Int can represent values between -(2^31) and 2^31 - 1. ",
	Serialize:  coerceInt,
	ParseValue: parseIntValue,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.IntValue:
			if intValue, err := strconv.ParseInt(valueAST.Value, 10, 32); err == nil {
				return int(intValue)
			}
		}
		return nil
	},
	AppendJSON: appendIntJSON,
})

// parseFloatValue is Float input coercion (variables): a finite number,
// including a json.Number, or a string in JSON number syntax that
// parses to a finite float ("2.5", "1e3"). A boolean, "NaN", "Inf",
// an overflowing value and any other string are rejected (nil).
// Literals in the query text stay spec-strict (ParseLiteral).
func parseFloatValue(value interface{}) interface{} {
	switch v := value.(type) {
	case bool, *bool:
		return nil
	case string:
		if !isJSONNumberSyntax(v) {
			return nil
		}
		value = json.Number(v)
	case *string:
		if v == nil {
			return nil
		}
		return parseFloatValue(*v)
	}
	f := coerceFloat(value)
	switch v := f.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil
		}
	}
	return f
}

// Float is the GraphQL float type definition.
var Float = newBuiltinScalar(serializeFloat, ScalarConfig{
	Name: "Float",
	Description: "The `Float` scalar type represents signed double-precision fractional " +
		"values as specified by " +
		"[IEEE 754](http://en.wikipedia.org/wiki/IEEE_floating_point). ",
	Serialize:  coerceFloat,
	ParseValue: parseFloatValue,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.FloatValue:
			if floatValue, err := strconv.ParseFloat(valueAST.Value, 64); err == nil {
				return floatValue
			}
		case *ast.IntValue:
			if floatValue, err := strconv.ParseFloat(valueAST.Value, 64); err == nil {
				return floatValue
			}
		}
		return nil
	},
	AppendJSON: appendFloatJSON,
})

// parseStringValue is String input coercion (variables): unchanged
// from the historical lenient behavior, any value is formatted with
// %v. Result coercion is coerceString / serializeString.
func parseStringValue(value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return v
	case *string:
		if v == nil {
			return nil
		}
		return *v
	}
	return fmt.Sprintf("%v", value)
}

// String is the GraphQL string type definition
var String = newBuiltinScalar(serializeString, ScalarConfig{
	Name: "String",
	Description: "The `String` scalar type represents textual data, represented as UTF-8 " +
		"character sequences. The String type is most often used by GraphQL to " +
		"represent free-form human-readable text.",
	Serialize:  coerceString,
	ParseValue: parseStringValue,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			return valueAST.Value
		}
		return nil
	},
	AppendJSON: appendStringJSON,
})

// parseBoolValue is Boolean input coercion (variables): unchanged from
// the historical lenient behavior. Result coercion is coerceBool /
// serializeBool.
func parseBoolValue(value interface{}) interface{} {
	switch value := value.(type) {
	case bool:
		return value
	case *bool:
		if value == nil {
			return nil
		}
		return *value
	case string:
		switch value {
		case "", "false":
			return false
		}
		return true
	case *string:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case float64:
		if value != 0 {
			return true
		}
		return false
	case *float64:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case float32:
		if value != 0 {
			return true
		}
		return false
	case *float32:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case int:
		if value != 0 {
			return true
		}
		return false
	case *int:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case int8:
		if value != 0 {
			return true
		}
		return false
	case *int8:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case int16:
		if value != 0 {
			return true
		}
		return false
	case *int16:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case int32:
		if value != 0 {
			return true
		}
		return false
	case *int32:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case int64:
		if value != 0 {
			return true
		}
		return false
	case *int64:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case uint:
		if value != 0 {
			return true
		}
		return false
	case *uint:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case uint8:
		if value != 0 {
			return true
		}
		return false
	case *uint8:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case uint16:
		if value != 0 {
			return true
		}
		return false
	case *uint16:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case uint32:
		if value != 0 {
			return true
		}
		return false
	case *uint32:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	case uint64:
		if value != 0 {
			return true
		}
		return false
	case *uint64:
		if value == nil {
			return nil
		}
		return parseBoolValue(*value)
	}
	return false
}

// Boolean is the GraphQL boolean type definition
var Boolean = newBuiltinScalar(serializeBool, ScalarConfig{
	Name:        "Boolean",
	Description: "The `Boolean` scalar type represents `true` or `false`.",
	Serialize:   coerceBool,
	ParseValue:  parseBoolValue,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.BooleanValue:
			return valueAST.Value
		}
		return nil
	},
	AppendJSON: appendBoolJSON,
})

// parseIDValue is ID input coercion (spec: Scalars › ID › Input
// Coercion; graphql-js parseValue): a string, or an integral number,
// which becomes its decimal string. A json.Number in integer syntax is
// kept digit for digit; one with a fraction or exponent is accepted
// when it is integral and exact as a float64 (|n| <= 2^53). A
// fractional number, a boolean or any other value is rejected (nil).
func parseIDValue(value interface{}) interface{} {
	switch value := value.(type) {
	case string:
		return value
	case *string:
		if value == nil {
			return nil
		}
		return *value
	case bool, *bool:
		return nil
	case float32:
		return parseIDValue(float64(value))
	case *float32:
		if value == nil {
			return nil
		}
		return parseIDValue(float64(*value))
	case float64:
		if value != math.Trunc(value) {
			return nil // fractional, NaN or ±Inf
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case *float64:
		if value == nil {
			return nil
		}
		return parseIDValue(*value)
	case json.Number:
		if isJSONIntegerSyntax(string(value)) {
			return string(value)
		}
		f, err := value.Float64()
		if err != nil || math.Abs(f) > 1<<53 {
			return nil
		}
		return parseIDValue(f)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", value)
	case *int, *int8, *int16, *int32, *int64, *uint, *uint8, *uint16, *uint32, *uint64:
		if isNullish(value) {
			return nil
		}
		return fmt.Sprintf("%d", reflect.ValueOf(value).Elem().Interface())
	}
	return nil
}

// ID is the GraphQL id type definition
var ID = newBuiltinScalar(serializeID, ScalarConfig{
	Name: "ID",
	Description: "The `ID` scalar type represents a unique identifier, often used to " +
		"refetch an object or as key for a cache. The ID type appears in a JSON " +
		"response as a String; however, it is not intended to be human-readable. " +
		"When expected as an input type, any string (such as `\"4\"`) or integer " +
		"(such as `4`) input value will be accepted as an ID.",
	Serialize:  coerceID,
	ParseValue: parseIDValue,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.IntValue:
			return valueAST.Value
		case *ast.StringValue:
			return valueAST.Value
		}
		return nil
	},
	AppendJSON: appendStringJSON,
})

func serializeDateTime(value interface{}) interface{} {
	switch value := value.(type) {
	case time.Time:
		buff, err := value.MarshalText()
		if err != nil {
			return nil
		}

		return string(buff)
	case *time.Time:
		if value == nil {
			return nil
		}
		return serializeDateTime(*value)
	default:
		return nil
	}
}

func unserializeDateTime(value interface{}) interface{} {
	switch value := value.(type) {
	case []byte:
		t := time.Time{}
		err := t.UnmarshalText(value)
		if err != nil {
			return nil
		}

		return t
	case string:
		return unserializeDateTime([]byte(value))
	case *string:
		if value == nil {
			return nil
		}
		return unserializeDateTime([]byte(*value))
	case time.Time:
		return value
	default:
		return nil
	}
}

var DateTime = NewScalar(ScalarConfig{
	Name: "DateTime",
	Description: "The `DateTime` scalar type represents a DateTime." +
		" The DateTime is serialized as an RFC 3339 quoted string",
	Serialize:  serializeDateTime,
	ParseValue: unserializeDateTime,
	ParseLiteral: func(valueAST ast.Value) interface{} {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			return unserializeDateTime(valueAST.Value)
		}
		return nil
	},
	AppendJSON: appendDateTimeJSON,
})

// appendIntJSON writes a coerceInt-style result as a JSON integer.
// Matches the contract that Serialize returns int (or nil); other
// kinds round-trip through coerceInt to preserve the slow-path
// behavior (e.g. float64 → truncated int).
func appendIntJSON(dst []byte, value interface{}) []byte {
	if n, ok := value.(int); ok {
		return strconv.AppendInt(dst, int64(n), 10)
	}
	coerced := coerceInt(value)
	if coerced == nil {
		return append(dst, "null"...)
	}
	return strconv.AppendInt(dst, int64(coerced.(int)), 10)
}

// appendFloatJSON writes a Float-typed value as a JSON number.
// Mirrors encoding/json's float behavior: NaN / +-Inf are not
// representable in JSON and yield null. Format follows json's
// strategy (use 'e' for very small/large magnitudes, 'f' otherwise)
// to keep byte-level parity with json.Marshal for the common cases.
func appendFloatJSON(dst []byte, value interface{}) []byte {
	var f float64
	switch v := value.(type) {
	case float64:
		f = v
	case float32:
		f = float64(v)
	default:
		coerced := coerceFloat(value)
		if coerced == nil {
			return append(dst, "null"...)
		}
		f = coerced.(float64)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(dst, "null"...)
	}
	abs := math.Abs(f)
	fmtByte := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		fmtByte = 'e'
	}
	dst = strconv.AppendFloat(dst, f, fmtByte, -1, 64)
	if fmtByte == 'e' {
		// strconv may emit "1e+09"; encoding/json emits "1e+09" too,
		// so no normalization needed here.
		return dst
	}
	return dst
}

// appendBoolJSON writes a Boolean-typed value as JSON true/false.
func appendBoolJSON(dst []byte, value interface{}) []byte {
	switch v := value.(type) {
	case bool:
		if v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case *bool:
		if v == nil {
			return append(dst, "null"...)
		}
		if *v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	}
	coerced := coerceBool(value)
	b, ok := coerced.(bool)
	if !ok {
		return append(dst, "null"...)
	}
	if b {
		return append(dst, "true"...)
	}
	return append(dst, "false"...)
}

// appendStringJSON writes a String / ID value as a JSON string.
// Falls back to coerceString for non-string types (matching the
// Serialize-then-emit pipeline).
func appendStringJSON(dst []byte, value interface{}) []byte {
	switch v := value.(type) {
	case string:
		return appendJSONString(dst, v)
	case *string:
		if v == nil {
			return append(dst, "null"...)
		}
		return appendJSONString(dst, *v)
	}
	coerced := coerceString(value)
	if coerced == nil {
		return append(dst, "null"...)
	}
	if s, ok := coerced.(string); ok {
		return appendJSONString(dst, s)
	}
	return append(dst, "null"...)
}

// appendDateTimeJSON renders a time.Time / *time.Time as the JSON
// quoted RFC 3339 form serializeDateTime would produce. Formats
// directly into dst to avoid the MarshalText + string conversion
// allocations.
func appendDateTimeJSON(dst []byte, value interface{}) []byte {
	var t time.Time
	switch v := value.(type) {
	case time.Time:
		t = v
	case *time.Time:
		if v == nil {
			return append(dst, "null"...)
		}
		t = *v
	default:
		return append(dst, "null"...)
	}
	// time.MarshalText produces RFC 3339 with nanosecond precision
	// (time.RFC3339Nano). Format directly to avoid the []byte + string
	// allocations of MarshalText. MarshalText never errors for a
	// time.Time value, so we skip the error check.
	dst = append(dst, '"')
	dst = t.AppendFormat(dst, time.RFC3339Nano)
	return append(dst, '"')
}

const hexChars = "0123456789abcdef"

// appendJSONString writes a JSON-quoted string to dst with HTML-safe
// escaping (matches encoding/json's default for <, >, &). UTF-8 is
// passed through; control bytes (< 0x20) get \u escapes; U+2028 and
// U+2029 get   /   to stay valid as JS embeds. The fast
// path streams contiguous safe runs in a single append.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			if jsonSafeSet[c] {
				i++
				continue
			}
			if start < i {
				dst = append(dst, s[start:i]...)
			}
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexChars[c>>4], hexChars[c&0xF])
			}
			i++
			start = i
			continue
		}
		// Multibyte UTF-8: detect U+2028 / U+2029 (E2 80 A8 / A9).
		if c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			if start < i {
				dst = append(dst, s[start:i]...)
			}
			dst = append(dst, '\\', 'u', '2', '0', '2', hexChars[s[i+2]&0xF])
			i += 3
			start = i
			continue
		}
		i++
	}
	if start < len(s) {
		dst = append(dst, s[start:]...)
	}
	return append(dst, '"')
}

// jsonStringEncodedLen returns exactly len(appendJSONString(nil, s)) —
// the encoded byte count including both quotes — without allocating.
// It mirrors appendJSONString's branches one for one; the two are
// pinned together by TestJSONStringEncodedLenMatchesAppend, because a
// drift here would silently under-size a caller's buffer.
func jsonStringEncodedLen(s string) int {
	n := 2 // the enclosing quotes
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			switch {
			case jsonSafeSet[c]:
				n++
			case c == '"' || c == '\\' || c == '\n' || c == '\r' ||
				c == '\t' || c == '\b' || c == '\f':
				n += 2
			default:
				n += 6 // \u00XX
			}
			i++
			continue
		}
		if c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			n += 6 // \u2028 / \u2029
			i += 3
			continue
		}
		n++
		i++
	}
	return n
}

// jsonSafeSet[c] is true when byte c may appear unescaped inside a
// JSON string under HTML-safe encoding (matches encoding/json's
// htmlSafeSet).
var jsonSafeSet = [128]bool{
	' ': true, '!': true /* '"' escaped */, '#': true, '$': true, '%': true,
	/* '&' escaped */ '\'': true, '(': true, ')': true, '*': true, '+': true,
	',': true, '-': true, '.': true, '/': true,
	'0': true, '1': true, '2': true, '3': true, '4': true,
	'5': true, '6': true, '7': true, '8': true, '9': true,
	':': true, ';': true /* '<' escaped */, '=': true, /* '>' escaped */
	'?': true, '@': true,
	'A': true, 'B': true, 'C': true, 'D': true, 'E': true, 'F': true, 'G': true,
	'H': true, 'I': true, 'J': true, 'K': true, 'L': true, 'M': true, 'N': true,
	'O': true, 'P': true, 'Q': true, 'R': true, 'S': true, 'T': true, 'U': true,
	'V': true, 'W': true, 'X': true, 'Y': true, 'Z': true,
	'[': true /* '\\' escaped */, ']': true, '^': true, '_': true, '`': true,
	'a': true, 'b': true, 'c': true, 'd': true, 'e': true, 'f': true, 'g': true,
	'h': true, 'i': true, 'j': true, 'k': true, 'l': true, 'm': true, 'n': true,
	'o': true, 'p': true, 'q': true, 'r': true, 's': true, 't': true, 'u': true,
	'v': true, 'w': true, 'x': true, 'y': true, 'z': true,
	'{': true, '|': true, '}': true, '~': true,
}
