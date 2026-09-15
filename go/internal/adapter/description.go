package adapter

import (
	"encoding/json"
	"strconv"
)

// EngineDescription is the engine's machine-readable project description
// (`ddev describe -j`), narrowed once here at the boundary.
//
// Child output is untrusted: the engine may be a different version, may report
// an error object, or may print nothing parseable at all. Rather than every
// caller re-deciding what a missing or wrongly typed field means, the
// description is parsed in one place and exposes typed readers — absent,
// null-shaped and wrongly typed all collapse to the same "not known" answer,
// which is what every caller wants anyway.
type EngineDescription struct {
	fields map[string]any
}

// DescriptionFromJSON parses the engine's describe output. It reports false
// when there is nothing usable in it — no output, invalid JSON, or no
// description object.
func DescriptionFromJSON(output string) (EngineDescription, bool) {
	if output == "" {
		return EngineDescription{}, false
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		return EngineDescription{}, false
	}

	fields, isObject := decoded["raw"].(map[string]any)
	if !isObject {
		return EngineDescription{}, false
	}

	return EngineDescription{fields: fields}, true
}

// StringOrNull is a scalar field as a string, addressed by a path of keys.
//
// Empty when any step of the path is missing or the value is structured — the
// same narrowing rule as the API-payload readers at the other two boundaries:
// a wrongly shaped field is "not known", never the word "Array".
func (d EngineDescription) StringOrNull(path ...string) string {
	var value any = d.fields

	for _, key := range path {
		object, isObject := value.(map[string]any)
		if !isObject {
			return ""
		}
		next, present := object[key]
		if !present {
			return ""
		}
		value = next
	}

	switch scalar := value.(type) {
	case string:
		return scalar
	case bool:
		// PHP's string cast, which is what the other boundaries do.
		if scalar {
			return "1"
		}

		return ""
	case float64:
		if scalar == float64(int64(scalar)) {
			return strconv.FormatInt(int64(scalar), 10)
		}

		return strconv.FormatFloat(scalar, 'g', -1, 64)
	default:
		return ""
	}
}
