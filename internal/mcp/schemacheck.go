// Package mcp — enforcement of the advertised tool schemas.
//
// A tool's inputSchema is only a hint to the client: nothing on the wire
// stops a caller from sending arguments that break it. schemaViolation
// checks the arguments of one call against the very schema tools/list
// advertises for that tool, so a constraint written into a schema is
// enforced by being written there — there is no second list to keep in
// step.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// toolSchema returns the inputSchema tools/list advertises for a tool.
func toolSchema(tool string) (map[string]any, bool) {
	for _, td := range tools() {
		if td.Name == tool {
			return td.InputSchema, true
		}
	}
	return nil, false
}

// schemaViolation checks raw (the `arguments` object of one tools/call)
// against the tool's advertised schema and reports the first violation
// as a ready-to-send code-2 message (`ask <verb>: ...`) naming the field
// and the bound.
//
// Keywords enforced: additionalProperties:false, required, minLength,
// maxLength, minItems, maxItems, minimum, maximum and enum, at every
// depth (nested objects and array items included). `type` is left to the
// handler's own json.Unmarshal, which already refuses a wrong type; an
// explicit JSON null is treated as "argument absent", as that decode
// treats it.
//
// skip names top-level arguments the caller has already validated under
// a rule that is deliberately not the schema's literal reading — `title`
// is trimmed before it is measured, so " x " padded past 200 characters
// is accepted.
func schemaViolation(tool string, raw json.RawMessage, skip ...string) (string, bool) {
	schema, ok := toolSchema(tool)
	if !ok {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// Malformed arguments are the handler's decode error to report.
		return "", false
	}
	skipped := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipped[s] = true
	}
	if msg := checkSchema("", schema, v, skipped); msg != "" {
		return "ask " + strings.TrimPrefix(tool, "ask_") + ": " + msg, true
	}
	return "", false
}

// checkSchema returns the first violation of schema by v, or "". path is
// the argument's dotted name ("" for the arguments object itself).
func checkSchema(path string, schema map[string]any, v any, skip map[string]bool) string {
	if v == nil || skip[path] {
		return ""
	}
	switch val := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if closed, isBool := schema["additionalProperties"].(bool); isBool && !closed {
			for _, k := range keys {
				if _, declared := props[k]; !declared {
					accepted := make([]string, 0, len(props))
					for p := range props {
						accepted = append(accepted, p)
					}
					sort.Strings(accepted)
					return fmt.Sprintf("unknown argument %q (accepted: %s)", joinPath(path, k), strings.Join(accepted, ", "))
				}
			}
		}
		if required, _ := schema["required"].([]string); len(required) > 0 {
			for _, r := range required {
				if val[r] == nil {
					return joinPath(path, r) + " is required"
				}
			}
		}
		for _, k := range keys {
			sub, _ := props[k].(map[string]any)
			if sub == nil {
				continue
			}
			if msg := checkSchema(joinPath(path, k), sub, val[k], skip); msg != "" {
				return msg
			}
		}
	case []any:
		if max, ok := schema["maxItems"].(int); ok && len(val) > max {
			return fmt.Sprintf("%s exceeds %d items (got %d)", path, max, len(val))
		}
		if min, ok := schema["minItems"].(int); ok && len(val) < min {
			return fmt.Sprintf("%s needs at least %d item(s) (got %d)", path, min, len(val))
		}
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			return ""
		}
		for i, el := range val {
			if msg := checkSchema(fmt.Sprintf("%s[%d]", path, i), items, el, skip); msg != "" {
				return msg
			}
		}
	case string:
		n := utf8.RuneCountInString(val)
		if min, ok := schema["minLength"].(int); ok && n < min {
			if min == 1 {
				return path + " must not be empty (at least 1 character)"
			}
			return fmt.Sprintf("%s must be at least %d characters (got %d)", path, min, n)
		}
		if max, ok := schema["maxLength"].(int); ok && n > max {
			return fmt.Sprintf("%s exceeds %d characters (got %d)", path, max, n)
		}
		if enum, _ := schema["enum"].([]string); len(enum) > 0 {
			for _, e := range enum {
				if e == val {
					return ""
				}
			}
			return fmt.Sprintf("%s: invalid value %q (want one of %s)", path, val, strings.Join(enum, ", "))
		}
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return ""
		}
		min, hasMin := schema["minimum"].(int)
		max, hasMax := schema["maximum"].(int)
		below := hasMin && f < float64(min)
		above := hasMax && f > float64(max)
		switch {
		case (below || above) && hasMin && hasMax:
			return fmt.Sprintf("%s must be between %d and %d (got %s)", path, min, max, val)
		case below:
			return fmt.Sprintf("%s must be at least %d (got %s)", path, min, val)
		case above:
			return fmt.Sprintf("%s must be at most %d (got %s)", path, max, val)
		}
	}
	return ""
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
