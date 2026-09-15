package gitlab

import (
	"math"
	"strconv"
	"strings"
)

// payload is the one place a decoded GitLab response stops being any.
//
// The body comes from a server we do not control, so every field read out of
// it is untrusted input. Rather than scatter type assertions through the
// models, each field is narrowed exactly once — here, at the boundary — into
// the type the model declares. Everything inward of a from* call therefore
// works with real types.
//
// Narrowing is total and lossy by design: a field of the wrong JSON type is
// not an error, it is the caller-supplied default. A wrong-typed field on a
// dashboard row must degrade that row, not abort the run. Structural faults
// that no default can paper over — a body that is not a list of objects when
// the endpoint promises one — are the client's business and surface there as
// a malformed-response failure.
type payload struct {
	data map[string]any
}

// scalarString renders a JSON scalar the way PHP's string cast does, since
// that is the behaviour the corpus was generated against: true is "1", false
// is "", and a whole float loses its ".0".
func scalarString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		if v {
			return "1", true
		}

		return "", true
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			return strconv.FormatInt(int64(v), 10), true
		}

		return strconv.FormatFloat(v, 'g', -1, 64), true
	// A payload decoded from JSON only ever holds float64, but one *built* in
	// this process — a model rendered back through ToAPIMap, which is what a
	// snapshot does before it is serialised — holds real ints.
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	default:
		return "", false
	}
}

// scalarInt narrows a JSON number, or a quoted one — GitLab ids are sometimes
// strings — to an int.
func scalarInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}

		return int(math.Trunc(v)), true
	case string:
		trimmed := strings.TrimSpace(v)
		if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return int(n), true
		}
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int(math.Trunc(f)), true
		}

		return 0, false
	case int:
		return v, true
	case int64:
		return int(v), true
	default:
		return 0, false
	}
}

// str is a scalar field as a string; absent, null or structured yields def.
func (p payload) str(key, def string) string {
	if s, ok := scalarString(p.data[key]); ok {
		return s
	}

	return def
}

// intOr is a numeric field as an int; anything else yields def.
func (p payload) intOr(key string, def int) int {
	if n, ok := scalarInt(p.data[key]); ok {
		return n
	}

	return def
}

// boolOr is a scalar field as a bool. The default is required because callers
// derive it: a merge request infers draft state from its title when the field
// is absent.
func (p payload) boolOr(key string, def bool) bool {
	switch v := p.data[key].(type) {
	case bool:
		return v
	case string:
		// PHP's bool cast: every string but "" and "0" is true.
		return v != "" && v != "0"
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	default:
		return def
	}
}

// child is a nested JSON object as its own payload. found is false when the
// field is absent, null, or not an object at all.
func (p payload) child(key string) (payload, bool) {
	if nested, ok := p.data[key].(map[string]any); ok {
		return payload{data: nested}, true
	}

	return payload{}, false
}
