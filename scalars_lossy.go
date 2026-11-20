package graphql

import (
	"math"
	"strconv"
)

// Lossy variable coercion, opt-in per request with
// ExecuteParams.AllowLossyInputCoercion / Params.AllowLossyInputCoercion.
// These are the v2.1.1 ParseValue functions of Int and Float, kept
// verbatim (renamed): Int truncates 2.5 to 2 and maps true/false to
// 1/0; Float accepts booleans and any string strconv.ParseFloat
// accepts. ID's v2.1.1 ParseValue is parseStringValue (%v of any
// value). They run only after the strict ParseValue rejected a value,
// so a value the strict path accepts coerces the same with or without
// the flag. Literals in the query text never reach them.

// lossyParseInt is the v2.1.1 Int ParseValue.
func lossyParseInt(value interface{}) interface{} {
	switch value := value.(type) {
	case bool:
		if value == true {
			return 1
		}
		return 0
	case *bool:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case int:
		if value < int(math.MinInt32) || value > int(math.MaxInt32) {
			return nil
		}
		return value
	case *int:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case int8:
		return int(value)
	case *int8:
		if value == nil {
			return nil
		}
		return int(*value)
	case int16:
		return int(value)
	case *int16:
		if value == nil {
			return nil
		}
		return int(*value)
	case int32:
		return int(value)
	case *int32:
		if value == nil {
			return nil
		}
		return int(*value)
	case int64:
		if value < int64(math.MinInt32) || value > int64(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *int64:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case uint:
		if value > math.MaxInt32 {
			return nil
		}
		return int(value)
	case *uint:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case uint8:
		return int(value)
	case *uint8:
		if value == nil {
			return nil
		}
		return int(*value)
	case uint16:
		return int(value)
	case *uint16:
		if value == nil {
			return nil
		}
		return int(*value)
	case uint32:
		if value > uint32(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *uint32:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case uint64:
		if value > uint64(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *uint64:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case float32:
		if value < float32(math.MinInt32) || value > float32(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *float32:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case float64:
		if value < float64(math.MinInt32) || value > float64(math.MaxInt32) {
			return nil
		}
		return int(value)
	case *float64:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	case string:
		val, err := strconv.ParseFloat(value, 0)
		if err != nil {
			return nil
		}
		return lossyParseInt(val)
	case *string:
		if value == nil {
			return nil
		}
		return lossyParseInt(*value)
	}

	// If the value cannot be transformed into an int, return nil instead of '0'
	// to denote 'no integer found'
	return nil
}

// lossyParseFloat is the v2.1.1 Float ParseValue.
func lossyParseFloat(value interface{}) interface{} {
	switch value := value.(type) {
	case bool:
		if value == true {
			return 1.0
		}
		return 0.0
	case *bool:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case int:
		return float64(value)
	case *int:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case int8:
		return float64(value)
	case *int8:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case int16:
		return float64(value)
	case *int16:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case int32:
		return float64(value)
	case *int32:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case int64:
		return float64(value)
	case *int64:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case uint:
		return float64(value)
	case *uint:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case uint8:
		return float64(value)
	case *uint8:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case uint16:
		return float64(value)
	case *uint16:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case uint32:
		return float64(value)
	case *uint32:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case uint64:
		return float64(value)
	case *uint64:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case float32:
		return value
	case *float32:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case float64:
		return value
	case *float64:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	case string:
		val, err := strconv.ParseFloat(value, 0)
		if err != nil {
			return nil
		}
		return val
	case *string:
		if value == nil {
			return nil
		}
		return lossyParseFloat(*value)
	}

	// If the value cannot be transformed into an float, return nil instead of '0.0'
	// to denote 'no float found'
	return nil
}

func init() {
	Int.parseValueLossy = lossyParseInt
	Float.parseValueLossy = lossyParseFloat
	ID.parseValueLossy = parseStringValue
}
