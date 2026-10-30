package mcp

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aac/ask/internal/core"
)

// Tests that the server enforces every constraint its tool schemas
// advertise beyond maxLength (mcpdrift_test.go covers that one): array
// and number bounds, minLength, required, enum, and
// additionalProperties:false.

// mcpconstraintErr decodes a single-part error envelope.
func mcpconstraintErr(t *testing.T, tr toolResult) errEnvelope {
	t.Helper()
	if !tr.IsError || len(tr.Content) != 1 {
		body := ""
		if len(tr.Content) > 0 {
			body = tr.Content[0].Text
		}
		t.Fatalf("want a single-part error, got isError=%v with %d part(s): %.200s", tr.IsError, len(tr.Content), body)
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %.200s", err, tr.Content[0].Text)
	}
	return env
}

// mcpconstraintRefused asserts a code-2 refusal whose message contains
// every want fragment, and that the store is byte-identical to before.
func mcpconstraintRefused(t *testing.T, root string, before map[string]string, tr toolResult, want ...string) {
	t.Helper()
	env := mcpconstraintErr(t, tr)
	if env.Code != 2 {
		t.Errorf("code: got %d want 2 (message %q)", env.Code, env.Message)
	}
	for _, w := range want {
		if !strings.Contains(env.Message, w) {
			t.Errorf("message %q must contain %q", env.Message, w)
		}
	}
	mcpdriftSameStore(t, root, before)
}

// mcpconstraintLinks returns n minimal valid links.
func mcpconstraintLinks(n int) []map[string]any {
	links := make([]map[string]any, n)
	for i := range links {
		links[i] = map[string]any{"label": "l", "url": "u"}
	}
	return links
}

// TestAdvertisedBoundsAreEnforced: for each advertised bound, the value
// at the bound is accepted and the value just past it is refused with
// code 2, a message naming the field and the bound, and no write.
func TestAdvertisedBoundsAreEnforced(t *testing.T) {
	verifier := func(timeout any) map[string]any {
		return map[string]any{"title": "t", "verifier": map[string]any{"command": "true", "timeout_seconds": timeout}}
	}
	link := func(l map[string]any) map[string]any {
		return map[string]any{"title": "t", "links": []map[string]any{l}}
	}
	cases := []struct {
		name, tool string
		ok, bad    map[string]any
		want       []string
	}{
		{"links maxItems", "ask_new",
			map[string]any{"title": "t", "links": mcpconstraintLinks(32)},
			map[string]any{"title": "t", "links": mcpconstraintLinks(33)},
			[]string{"links", "32", "33"}},
		{"link label minLength", "ask_new",
			link(map[string]any{"label": "x", "url": "u"}),
			link(map[string]any{"label": "", "url": "u"}),
			[]string{"links[0].label", "at least 1 character"}},
		{"link url minLength", "ask_new",
			link(map[string]any{"label": "l", "url": "x"}),
			link(map[string]any{"label": "l", "url": ""}),
			[]string{"links[0].url", "at least 1 character"}},
		{"link label required", "ask_new",
			link(map[string]any{"label": "l", "url": "u"}),
			link(map[string]any{"url": "u"}),
			[]string{"links[0].label is required"}},
		{"link url required", "ask_new",
			link(map[string]any{"label": "l", "url": "u"}),
			link(map[string]any{"label": "l"}),
			[]string{"links[0].url is required"}},
		{"timeout minimum", "ask_new", verifier(1), verifier(0),
			[]string{"verifier.timeout_seconds", "between 1 and 3600", "got 0"}},
		{"timeout maximum", "ask_new", verifier(3600), verifier(3601),
			[]string{"verifier.timeout_seconds", "between 1 and 3600", "got 3601"}},
		{"timeout negative", "ask_new", verifier(1), verifier(-5),
			[]string{"verifier.timeout_seconds", "between 1 and 3600"}},
		{"recipient minLength", "ask_new",
			map[string]any{"title": "t", "recipient": "r"},
			map[string]any{"title": "t", "recipient": ""},
			[]string{"recipient", "at least 1 character"}},
		{"urgency enum, explicit empty", "ask_new",
			map[string]any{"title": "t", "urgency": "fyi"},
			map[string]any{"title": "t", "urgency": ""},
			[]string{"urgency", "blocker, normal, fyi"}},
		{"verifier.type enum, explicit empty", "ask_new",
			map[string]any{"title": "t", "verifier": map[string]any{"command": "true", "type": "shell"}},
			map[string]any{"title": "t", "verifier": map[string]any{"command": "true", "type": ""}},
			[]string{"verifier.type", "shell"}},
		{"list blocks minLength", "ask_list",
			map[string]any{"blocks": "b"}, map[string]any{"blocks": ""},
			[]string{"blocks", "at least 1 character"}},
		{"list recipient minLength", "ask_list",
			map[string]any{"recipient": "r"}, map[string]any{"recipient": ""},
			[]string{"recipient", "at least 1 character"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			initRepo(t, root)
			mcpupdNew(t, root, "seed", "", 1)
			if tr := mcpdriftCall(t, root, tc.tool, tc.ok); tr.IsError {
				t.Fatalf("the value at the bound was refused: %.200s", tr.Content[0].Text)
			}
			before := mcpdriftSnapshot(t, root)
			mcpconstraintRefused(t, root, before, mcpdriftCall(t, root, tc.tool, tc.bad), tc.want...)
		})
	}

	// An omitted timeout_seconds is not a violation: the schema does not
	// require it. Only an explicit out-of-range value is refused.
	root := t.TempDir()
	initRepo(t, root)
	if tr := mcpdriftCall(t, root, "ask_new", map[string]any{
		"title": "t", "verifier": map[string]any{"command": "true"},
	}); tr.IsError {
		t.Fatalf("a verifier with no timeout_seconds was refused: %.200s", tr.Content[0].Text)
	}
}

// TestUnknownArgumentIsRefused: every tool advertises
// additionalProperties:false, so an argument its schema does not declare
// is a code-2 error and nothing is written — at the top level of every
// tool and inside each nested object that declares it.
func TestUnknownArgumentIsRefused(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "", 1)
	before := mcpdriftSnapshot(t, root)

	for _, td := range tools() {
		if td.InputSchema["additionalProperties"] != false {
			t.Fatalf("%s no longer advertises additionalProperties:false; this test assumes it does", td.Name)
		}
		t.Run(td.Name, func(t *testing.T) {
			args := mcpdriftBaseArgs(td.Name, it.ID)
			if td.Name == "ask_update" {
				args["title"] = "renamed"
			}
			args["bogus"] = 1
			mcpconstraintRefused(t, root, before, mcpdriftCall(t, root, td.Name, args), `unknown argument "bogus"`)
		})
	}

	// idArgs decodes note and reason for all four id tools; each tool
	// still refuses the one its own schema does not declare.
	crossed := []struct{ tool, arg string }{
		{"ask_show", "note"}, {"ask_show", "reason"},
		{"ask_resolve", "reason"}, {"ask_reopen", "note"}, {"ask_close", "note"},
	}
	for _, tc := range crossed {
		t.Run(tc.tool+"/"+tc.arg, func(t *testing.T) {
			tr := mcpdriftCall(t, root, tc.tool, map[string]any{"id": it.ID, tc.arg: "x"})
			mcpconstraintRefused(t, root, before, tr, `unknown argument "`+tc.arg+`"`)
		})
	}

	nested := []struct {
		name string
		args map[string]any
		want string
	}{
		{"verifier", map[string]any{"title": "t",
			"verifier": map[string]any{"command": "true", "bogus": 1}}, `unknown argument "verifier.bogus"`},
		{"links item", map[string]any{"title": "t",
			"links": []map[string]any{{"label": "l", "url": "u"}, {"label": "l", "url": "u", "bogus": 1}}},
			`unknown argument "links[1].bogus"`},
	}
	for _, tc := range nested {
		t.Run("nested/"+tc.name, func(t *testing.T) {
			mcpconstraintRefused(t, root, before, mcpdriftCall(t, root, "ask_new", tc.args), tc.want)
		})
	}
}

// TestEveryDeclaredArgumentIsAccepted is the other half of refusing
// unknown arguments: a call that uses every property a schema declares
// must go through. The key sets are compared with the schema so a
// property added later fails here until it is exercised.
func TestEveryDeclaredArgumentIsAccepted(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	it := mcpupdNew(t, root, "seed", "", 1)

	full := map[string][]map[string]any{
		"ask_new": {{
			"title": "everything", "body": "b", "urgency": "blocker", "filed_by": "agent",
			"recipient": "someone", "tracker_ref": "act-1234",
			"verifier": map[string]any{"type": "shell", "command": "true", "timeout_seconds": 60},
			"links":    []map[string]any{{"label": "l", "url": "u"}},
			"blocks":   []string{"act-1234"},
		}},
		"ask_list": {{"status": []string{"open"}, "urgency": []string{"normal"}, "blocks": "b", "recipient": "r"},
			{"all": true}},
		"ask_show": {{"id": it.ID}},
		// body/body_append and verifier/verifier_clear are mutually
		// exclusive pairs, so two calls.
		"ask_update": {{"id": it.ID, "title": "renamed", "body": "new body", "verifier": "true"},
			{"id": it.ID, "body_append": "more", "verifier_clear": true}},
		"ask_resolve": {{"id": it.ID, "note": "done"}},
		"ask_reopen":  {{"id": it.ID, "reason": "not done"}},
		"ask_close":   {{"id": it.ID, "reason": "dismissed"}},
	}
	for _, td := range tools() {
		t.Run(td.Name, func(t *testing.T) {
			used := map[string]bool{}
			for _, args := range full[td.Name] {
				for k := range args {
					used[k] = true
				}
				if tr := mcpdriftCall(t, root, td.Name, args); tr.IsError {
					t.Fatalf("a call using only declared arguments was refused: %.200s", tr.Content[0].Text)
				}
			}
			props, _ := td.InputSchema["properties"].(map[string]any)
			for p := range props {
				if !used[p] {
					t.Errorf("declared argument %q is not exercised by this test", p)
				}
			}
		})
	}
}

// mcpconstraintJSONFields returns the json field names a handler's
// argument struct decodes.
func mcpconstraintJSONFields(v any) []string {
	rt := reflect.TypeOf(v)
	var names []string
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mcpconstraintProps returns the sorted property names of the schema
// found by following path ("properties" keys, or "items") from a tool's
// inputSchema.
func mcpconstraintProps(t *testing.T, tool string, path ...string) []string {
	t.Helper()
	schema, ok := toolSchema(tool)
	if !ok {
		t.Fatalf("no tool %q", tool)
	}
	for _, step := range path {
		if step == "items" {
			schema, _ = schema["items"].(map[string]any)
		} else {
			props, _ := schema["properties"].(map[string]any)
			schema, _ = props[step].(map[string]any)
		}
		if schema == nil {
			t.Fatalf("%s: no schema at %v", tool, path)
		}
	}
	props, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for p := range props {
		names = append(names, p)
	}
	sort.Strings(names)
	return names
}

// TestHandlersDecodeExactlyTheDeclaredArguments compares each handler's
// argument struct with its schema, so a handler cannot start reading an
// argument the schema does not declare (which the unknown-argument check
// would then refuse) without this failing.
func TestHandlersDecodeExactlyTheDeclaredArguments(t *testing.T) {
	union := func(lists ...[]string) []string {
		set := map[string]bool{}
		for _, l := range lists {
			for _, s := range l {
				set[s] = true
			}
		}
		out := make([]string, 0, len(set))
		for s := range set {
			out = append(out, s)
		}
		sort.Strings(out)
		return out
	}
	cases := []struct {
		name     string
		decoded  []string
		declared []string
	}{
		{"ask_new", mcpconstraintJSONFields(newArgs{}), mcpconstraintProps(t, "ask_new")},
		{"ask_new verifier", mcpconstraintJSONFields(verifierArgs{}), mcpconstraintProps(t, "ask_new", "verifier")},
		{"ask_new links[]", mcpconstraintJSONFields(core.Link{}), mcpconstraintProps(t, "ask_new", "links", "items")},
		{"ask_list", mcpconstraintJSONFields(listArgs{}), mcpconstraintProps(t, "ask_list")},
		{"ask_update", mcpconstraintJSONFields(updateArgs{}), mcpconstraintProps(t, "ask_update")},
		// idArgs is shared by the four id tools: it must decode exactly
		// what they declare between them.
		{"id tools", mcpconstraintJSONFields(idArgs{}), union(
			mcpconstraintProps(t, "ask_show"), mcpconstraintProps(t, "ask_resolve"),
			mcpconstraintProps(t, "ask_reopen"), mcpconstraintProps(t, "ask_close"))},
	}
	for _, tc := range cases {
		if !reflect.DeepEqual(tc.decoded, tc.declared) {
			t.Errorf("%s: handler decodes %v, schema declares %v", tc.name, tc.decoded, tc.declared)
		}
	}
}

// TestSchemaKeywordsAreAllEnforceable walks every advertised schema, at
// every depth, and fails on a keyword checkSchema does not act on or a
// keyword value of a Go type it would silently skip. Adding, say, a
// `pattern` to a schema fails here until the server enforces it.
func TestSchemaKeywordsAreAllEnforceable(t *testing.T) {
	isInt := func(v any) bool { _, ok := v.(int); return ok }
	isStrings := func(v any) bool { _, ok := v.([]string); return ok }
	wellTyped := map[string]func(any) bool{
		// Enforced by the handlers' own json.Unmarshal.
		"type": func(v any) bool { _, ok := v.(string); return ok },
		// Structural: checkSchema recurses through these.
		"properties": func(v any) bool { _, ok := v.(map[string]any); return ok },
		"items":      func(v any) bool { _, ok := v.(map[string]any); return ok },
		// Enforced by checkSchema.
		"additionalProperties": func(v any) bool { _, ok := v.(bool); return ok },
		"required":             isStrings,
		"enum":                 isStrings,
		"minLength":            isInt,
		"maxLength":            isInt,
		"minItems":             isInt,
		"maxItems":             isInt,
		"minimum":              isInt,
		"maximum":              isInt,
	}
	seen := map[string]int{}
	var walk func(where string, schema map[string]any)
	walk = func(where string, schema map[string]any) {
		for kw, val := range schema {
			check, known := wellTyped[kw]
			if !known {
				t.Errorf("%s: keyword %q is advertised but the server has no enforcement for it", where, kw)
				continue
			}
			if !check(val) {
				t.Errorf("%s: keyword %q has Go type %T, which checkSchema would skip", where, kw, val)
			}
			seen[kw]++
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, sub := range props {
				m, ok := sub.(map[string]any)
				if !ok {
					t.Errorf("%s.%s: property schema has Go type %T", where, name, sub)
					continue
				}
				walk(where+"."+name, m)
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			walk(where+"[]", items)
		}
	}
	for _, td := range tools() {
		walk(td.Name, td.InputSchema)
	}
	// Guard the walk: it must be reaching the nested constraints.
	for _, kw := range []string{"maxItems", "minimum", "maximum", "enum", "minLength", "additionalProperties"} {
		if seen[kw] == 0 {
			t.Errorf("the walk found no %q anywhere; it is no longer reaching the schemas", kw)
		}
	}
	if seen["additionalProperties"] < 9 {
		t.Errorf("found additionalProperties on %d objects, want at least 9 (7 tools + verifier + link)", seen["additionalProperties"])
	}
}
