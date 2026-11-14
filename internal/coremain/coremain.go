// Package coremain is test scaffolding: it derives the variant of this module's
// package that the core's current main can install. The real manifest declares
// fields that wait on module-core items (client_home and client tabs, tab params,
// sidebar placement and badges, notification kinds), and the core's manifest parser
// refuses unknown fields, so the real package can't be installed until those items
// land. The variant drops exactly those fields and nothing else, so the rest of the
// package (migrations, permissions, widgets, data, settings, help) goes through the
// core's real install. It never ships.
package coremain

import "strings"

// Pending names the module-core items whose manifest fields Manifest drops.
const Pending = "CORE-03, CORE-08, CORE-10"

// Fields maps each manifest field the core main refuses, by the name its parser's
// refusal gives, to the CORE item that adds it.
var Fields = map[string]string{
	"client_home":   "CORE-10",
	"params":        "CORE-10", // tabs[].params
	"placement":     "CORE-10", // placement: client
	"section":       "CORE-08", // sidebar[].section
	"icon":          "CORE-08",
	"order":         "CORE-08",
	"badge":         "CORE-08",
	"notifications": "CORE-03",
}

// Item returns the CORE item a refusal of the real manifest waits on, or "" when the
// refusal names none of Fields.
func Item(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for name, item := range Fields {
		if strings.Contains(msg, `"`+name+`"`) || (name == "placement" && strings.Contains(msg, "placement")) {
			return item
		}
	}
	return ""
}

// Edit replaces the manifest in files with its core-main variant. It has the shape
// clientportal.Build and Archive take.
func Edit(files map[string][]byte) {
	files["togavsu-module.yaml"] = Manifest(files["togavsu-module.yaml"])
}

// Manifest returns the manifest without the fields that wait on CORE items.
func Manifest(full []byte) []byte {
	lines := strings.Split(string(full), "\n")
	var out []string
	section := ""     // the current top-level key
	var item []string // the tab item being collected
	skipDeeper := -1  // drop lines indented deeper than this (a dropped block's children)
	flush := func() {
		keep := true
		for _, l := range item {
			if strings.TrimSpace(l) == "placement: client" {
				keep = false
			}
		}
		if keep {
			out = append(out, item...)
		}
		item = nil
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		ind := len(l) - len(strings.TrimLeft(l, " "))
		if skipDeeper >= 0 {
			if t == "" || ind > skipDeeper {
				continue
			}
			skipDeeper = -1
		}
		if ind == 0 && t != "" && !strings.HasPrefix(t, "#") {
			if section == "tabs" {
				flush()
			}
			section = strings.SplitN(t, ":", 2)[0]
			switch section {
			case "client_home":
				continue
			case "notifications":
				skipDeeper = 0
				continue
			}
		}
		switch section {
		case "tabs":
			if strings.HasPrefix(t, "- id:") {
				flush()
			}
			if ind == 4 && strings.HasPrefix(t, "params:") {
				continue
			}
			if strings.HasPrefix(t, "#") {
				continue
			}
			if ind == 0 {
				out = append(out, l)
				continue
			}
			item = append(item, l)
			continue
		case "sidebar":
			if ind == 4 {
				key := strings.SplitN(t, ":", 2)[0]
				switch key {
				case "section", "icon", "order", "subgroup":
					continue
				case "badge":
					skipDeeper = 4
					continue
				}
			}
		}
		out = append(out, l)
	}
	if section == "tabs" {
		flush()
	}
	return []byte(strings.Join(out, "\n"))
}
