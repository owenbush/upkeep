package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// orderedJSON is a JSON object that keeps its keys in the order they arrived.
//
// composer.json is a file people read and edit, and Go's map drops the order
// PHP's associative array keeps for free. Rewriting one key must not shuffle
// the rest.
type orderedJSON struct {
	keys     []string
	values   map[string]json.RawMessage
	isObject bool
}

func (o *orderedJSON) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))

	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim || delim != '{' {
		// Valid JSON, and not an object — the caller decides what that means.
		o.isObject = false

		return nil
	}

	o.isObject = true
	o.values = map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, isString := keyToken.(string)
		if !isString {
			return fmt.Errorf("object key is not a string")
		}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if _, seen := o.values[key]; !seen {
			o.keys = append(o.keys, key)
		}
		o.values[key] = value
	}

	_, err = decoder.Token()

	return err
}

func (o *orderedJSON) get(key string) (any, bool) {
	raw, present := o.values[key]
	if !present {
		return nil, false
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}

	return value, true
}

func (o *orderedJSON) set(key string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	if _, present := o.values[key]; !present {
		o.keys = append(o.keys, key)
	}
	o.values[key] = encoded
}

// marshalIndent renders the object with four-space indentation and unescaped
// slashes, which is what PHP's JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES
// produces — so a composer.json written by either implementation reads the
// same.
func (o *orderedJSON) marshalIndent() ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("{\n")

	for i, key := range o.keys {
		encodedKey, err := marshalNoEscape(key)
		if err != nil {
			return nil, err
		}

		var value any
		if err := json.Unmarshal(o.values[key], &value); err != nil {
			return nil, err
		}
		encodedValue, err := indentValue(value, "    ")
		if err != nil {
			return nil, err
		}

		out.WriteString("    ")
		out.Write(encodedKey)
		out.WriteString(": ")
		out.Write(encodedValue)
		if i < len(o.keys)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}

	out.WriteString("}")

	return out.Bytes(), nil
}

func indentValue(value any, prefix string) ([]byte, error) {
	encoded, err := marshalNoEscape(value)
	if err != nil {
		return nil, err
	}

	var indented bytes.Buffer
	if err := json.Indent(&indented, encoded, prefix, "    "); err != nil {
		return nil, err
	}

	return indented.Bytes(), nil
}

// marshalNoEscape renders without Go's HTML escaping, which would turn a
// composer.json's "&" and "<" into & and <.
func marshalNoEscape(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return bytes.TrimRight(out.Bytes(), "\n"), nil
}
