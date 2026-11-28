package domain

import (
	"encoding/json"
	"maps"
	"reflect"
)

// IsSubset reports whether every value the spec declares is already what Qdrant reports.
// Keys the spec leaves out are Qdrant's business, and a false the spec asks for matches an
// unset value, which is how Qdrant reports booleans it never stored.
func IsSubset(desired any, actual any) bool {
	if want, ok := desired.(JSON); ok {
		have, ok := actual.(JSON)
		if !ok || have == nil {
			return false
		}
		for key, value := range want {
			if value == nil {
				continue
			}
			if !IsSubset(value, have[key]) {
				return false
			}
		}
		return true
	}
	if flag, ok := desired.(bool); ok && !flag && actual == nil {
		return true
	}
	return jsonEqual(desired, actual)
}

// LiveOptimizers is the live optimizer config with Qdrant's null max_optimization_threads read as "auto".
func LiveOptimizers(config JSON) JSON {
	optimizers, ok := config["optimizer_config"].(JSON)
	if !ok || len(optimizers) == 0 || optimizers["max_optimization_threads"] != nil {
		return optimizers
	}
	live := maps.Clone(optimizers)
	live["max_optimization_threads"] = "auto"
	return live
}

// AtDefault reports whether a live value is Qdrant's default; a default of false also matches any falsy value.
func AtDefault(live any, fallback any) bool {
	if flag, ok := fallback.(bool); ok && !flag {
		return !jsonTruthy(live)
	}
	return jsonEqual(live, fallback)
}

func jsonNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	}
	return 0, false
}

// jsonEqual compares decoded JSON values, treating 1 and 1.0 as the same number.
func jsonEqual(left any, right any) bool {
	leftNumber, leftIsNumber := jsonNumber(left)
	rightNumber, rightIsNumber := jsonNumber(right)
	if leftIsNumber || rightIsNumber {
		return leftIsNumber && rightIsNumber && leftNumber == rightNumber
	}
	switch want := left.(type) {
	case JSON:
		have, ok := right.(JSON)
		if !ok || len(want) != len(have) {
			return false
		}
		for key, value := range want {
			other, present := have[key]
			if !present || !jsonEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		have, ok := right.([]any)
		if !ok || len(want) != len(have) {
			return false
		}
		for index := range want {
			if !jsonEqual(want[index], have[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}

func jsonTruthy(value any) bool {
	if value == nil {
		return false
	}
	if number, ok := jsonNumber(value); ok {
		return number != 0
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed != ""
	case JSON:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	}
	return true
}

func jsonObject(value any) JSON {
	object, ok := value.(JSON)
	if !ok || object == nil {
		return JSON{}
	}
	return object
}

func putJSON(target JSON, key string, value JSON) {
	if len(value) == 0 {
		return
	}
	target[key] = value
}

func putString(target JSON, key string, value string) {
	if value == "" {
		return
	}
	target[key] = value
}

func putInt(target JSON, key string, value *int) {
	if value == nil {
		return
	}
	target[key] = *value
}

func putBool(target JSON, key string, value *bool) {
	if value == nil {
		return
	}
	target[key] = *value
}

func dropNil(values JSON) JSON {
	kept := JSON{}
	for key, value := range values {
		if value == nil {
			continue
		}
		kept[key] = value
	}
	return kept
}

func jsonInt64(value any) *int64 {
	number, ok := jsonNumber(value)
	if !ok {
		return nil
	}
	whole := int64(number)
	return &whole
}
