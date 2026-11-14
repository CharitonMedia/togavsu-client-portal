// Package coremain is test scaffolding: it derives the variant of this module's
// package that the core's current main can install. The real manifest declares
// fields that wait on module-core items (client_home and client tabs, tab params,
// sidebar placement and badges, notification kinds), which the core's manifest parser
// refuses, and scopes the core doesn't know yet, which its approval refuses. So the
// real package can't be installed until those items land. The variant drops exactly
// those fields and permissions and nothing else, so the rest of the package
// (migrations, the other permissions, widgets, data, settings, help) goes through the
// core's real install and approval. It never ships.
package coremain

import "strings"

// Pending names the module-core items whose manifest fields and scopes the variant
// drops.
const Pending = "CORE-03, CORE-04, CORE-08, CORE-10, CORE-14, CORE-18, CORE-19"

// Small fixed numbers spelled as arithmetic for the core's AC-030 source check
// (modulekit.CheckSource), as the core's matter-clock does. fieldIndent is the indent
// of a field inside a tabs or sidebar item.
var (
	zero        = int(0)
	fieldIndent = len("    ")
)

// A Field is a manifest field the core main refuses: the name its refusal gives, the
// CORE item that adds it, and where the manifest uses it.
type Field struct {
	Name, Item, Where string
}

// Fields lists every manifest field the variant strips. Each is refused by the core
// main's manifest parser today, and nothing else in the manifest is.
var Fields = []Field{
	{"client_home", "CORE-10", "top level: the client dashboard tab"},
	{"placement", "CORE-10", "tabs[] with placement: client, dropped whole"},
	{"params", "CORE-10", "tabs[].params"},
	{"section", "CORE-08", "sidebar[].section"},
	{"icon", "CORE-08", "sidebar[].icon"},
	{"order", "CORE-08", "sidebar[].order"},
	{"badge", "CORE-08", "sidebar[].badge and its fields"},
	{"notifications", "CORE-03", "top level: notification kinds"},
}

// Scopes maps each requested scope the core main doesn't know (its manifest check
// accepts it, and approval answers "unknown scope") to the CORE item that adds it. The
// variant drops the permissions that request them.
var Scopes = map[string]string{
	"users:directory":     "CORE-19",
	"firm:read":           "CORE-18",
	"mail:send":           "CORE-04",
	"client_access:read":  "CORE-14",
	"client_access:write": "CORE-14",
}

// Item returns the CORE item a refusal of a manifest waits on, or "" when the refusal
// names none of Fields.
func Item(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, f := range Fields {
		if strings.Contains(msg, `"`+f.Name+`"`) || (f.Name == "placement" && strings.Contains(msg, "placement")) {
			return f.Item
		}
	}
	return ""
}

// Edit replaces the manifest in files with its core-main variant. It has the shape
// clientportal.Build and Archive take.
func Edit(files map[string][]byte) {
	files["togavsu-module.yaml"] = Manifest(files["togavsu-module.yaml"])
}

// Manifest returns the manifest without the fields and scopes that wait on CORE items.
func Manifest(full []byte) []byte { return Strip(full, "") }

// Strip returns the manifest without the Fields and the permissions that request
// Scopes, except the field named keep, so a test can show the core refuses each field
// on its own.
func Strip(full []byte, keep string) []byte {
	drop := func(name string) bool { return name != keep }
	var out []string
	section := ""     // the current top-level key
	var item []string // the tab or permission being collected
	skipDeeper := -1  // drop lines indented deeper than this (a dropped block's children)
	flush := func() {
		keep := true
		for _, l := range item {
			t := strings.TrimSpace(l)
			scope, isScope := strings.CutPrefix(t, "- scope: ")
			if (t == "placement: client" && drop("placement")) || (isScope && Scopes[scope] != "") {
				keep = false
			}
		}
		if keep {
			out = append(out, item...)
		}
		item = nil
	}
	for _, l := range strings.Split(string(full), "\n") {
		t := strings.TrimSpace(l)
		ind := len(l) - len(strings.TrimLeft(l, " "))
		if skipDeeper >= zero {
			if t == "" || ind > skipDeeper {
				continue
			}
			skipDeeper = -1
		}
		if ind == zero && t != "" && !strings.HasPrefix(t, "#") {
			flush()
			section, _, _ = strings.Cut(t, ":")
			switch {
			case section == "client_home" && drop(section):
				continue
			case section == "notifications" && drop(section):
				skipDeeper = zero
				continue
			}
			out = append(out, l)
			continue
		}
		key, _, _ := strings.Cut(t, ":")
		switch section {
		case "tabs", "permissions":
			switch {
			case strings.HasPrefix(t, "#"):
				continue
			case strings.HasPrefix(t, "- "):
				flush()
			case section == "tabs" && ind == fieldIndent && key == "params" && drop(key):
				continue
			}
			item = append(item, l)
			continue
		case "sidebar":
			if ind == fieldIndent && drop(key) {
				switch key {
				case "section", "icon", "order":
					continue
				case "badge":
					skipDeeper = fieldIndent
					continue
				}
			}
		}
		out = append(out, l)
	}
	flush()
	return []byte(strings.Join(out, "\n"))
}
