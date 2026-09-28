package wf

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
)

// Schema describes a Go type as the subset of JSON Schema that start forms, the plan builder and a
// model need: objects with properties and required fields, arrays, maps, strings, numbers,
// integers, booleans and date-times. A field's `description` tag becomes its description; a field
// tagged `wf:"sensitive"` is marked so the admin panel masks it.
func Schema(t reflect.Type) map[string]any { return schemaOf(t, 0) }

func schemaOf(t reflect.Type, depth int) map[string]any {
	if depth > 8 {
		return map[string]any{} // deep or recursive types: anything
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == rawType:
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), depth+1)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), depth+1)}
	case reflect.Struct:
		properties, required := map[string]any{}, []string{}
		addFields(t, properties, &required, depth)
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	return map[string]any{}
}

// missingRequired names the first field a JSON value lacks that schema requires, searching nested
// objects and lists, or returns "".
func missingRequired(schema map[string]any, v any, path string) string {
	switch value := v.(type) {
	case nil:
		// null decodes into an empty struct; an object with required fields must be given.
		if required, ok := schema["required"].([]string); ok && len(required) > 0 {
			return strings.TrimSuffix(path, ".") + " (null)"
		}
	case map[string]any:
		if required, ok := schema["required"].([]string); ok {
			for _, name := range required {
				if _, present := value[name]; !present {
					return path + name
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, item := range value {
			if property, ok := properties[name].(map[string]any); ok {
				if missing := missingRequired(property, item, path+name+"."); missing != "" {
					return missing
				}
			}
		}
		if extra, ok := schema["additionalProperties"].(map[string]any); ok {
			for name, item := range value {
				if missing := missingRequired(extra, item, path+name+"."); missing != "" {
					return missing
				}
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range value {
				if missing := missingRequired(items, item, fmt.Sprintf("%s%d.", path, i)); missing != "" {
					return missing
				}
			}
		}
	}
	return ""
}

// checkRequired reports a required field missing from raw.
func checkRequired(schema map[string]any, raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	if missing := missingRequired(schema, v, ""); missing != "" {
		if strings.HasPrefix(missing, " (null)") {
			return errors.New("the input is null; it must be an object with its required fields")
		}
		return fmt.Errorf("missing required field %s", missing)
	}
	return nil
}

func addFields(t reflect.Type, properties map[string]any, required *[]string, depth int) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			embedded := f.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				addFields(embedded, properties, required, depth)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		property := schemaOf(f.Type, depth+1)
		if description := f.Tag.Get("description"); description != "" {
			property["description"] = description
		}
		if f.Tag.Get("wf") == "sensitive" {
			property["sensitive"] = true
		}
		properties[name] = property
		if !strings.Contains(options, "omitempty") && !strings.Contains(options, "omitzero") && f.Type.Kind() != reflect.Pointer {
			*required = append(*required, name)
		}
	}
}
