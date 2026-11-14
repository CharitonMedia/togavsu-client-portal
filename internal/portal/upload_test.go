package portal

import (
	"testing"
	"time"
)

func TestSniffType(t *testing.T) {
	ole := "\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1rest"
	cases := []struct {
		name, head, kind, err string
	}{
		{"brief.pdf", "%PDF-1.7\n", "pdf", ""},
		{"photo.jpg", "\xff\xd8\xff\xe0\x00\x10JFIF", "jpeg", ""},
		{"scan.png", "\x89PNG\r\n\x1a\n\x00\x00", "png", ""},
		{"IMG_0001.HEIC", "\x00\x00\x00\x18ftypheic\x00\x00", "heic", ""},
		{"letter.docx", "PK\x03\x04\x14\x00[Content_Types].xml word/document.xml", "docx", ""},
		{"budget.xlsx", "PK\x03\x04\x14\x00[Content_Types].xml xl/workbook.xml", "xlsx", ""},
		{"old.doc", ole, "doc", ""},
		{"old.xls", ole, "xls", ""},
		{"notes.txt", "Plain words.\n", "txt", ""},
		{"list.csv", "a,b\n1,2\n", "csv", ""},
		{"setup.pdf", "MZ\x90\x00\x03\x00", "", msgWrongType},
		{"setup.txt", "MZ\x90\x00\x03\x00", "", msgWrongType},
		{"archive.zip", "PK\x03\x04", "", msgWrongType},
		{"renamed.docx", "PK\x03\x04 xl/workbook.xml", "", msgWrongType},
		{"ole.pdf", ole, "", msgWrongType},
		{"binary.txt", "abc\x00def", "", msgWrongType},
		{"empty.pdf", "", "", msgEmpty},
	}
	for _, c := range cases {
		ft, err := SniffType(c.name, []byte(c.head))
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || ft.Kind != c.kind {
			t.Errorf("%s: %v, %v, want %s", c.name, ft, err, c.kind)
		}
	}
}

func TestWindowStep(t *testing.T) {
	start, count := int64(1000), int64(0)
	var ok bool
	var retry int64
	for i := 0; i < 90; i++ {
		start, count, ok, _ = WindowStep(start, count, 1000+int64(i), 90, 300)
		if !ok {
			t.Fatalf("upload %d refused", i+1)
		}
	}
	if _, _, ok, retry = WindowStep(start, count, 1100, 90, 300); ok || retry != 200 {
		t.Fatalf("91st upload: ok %v retry %d, want refused with 200", ok, retry)
	}
	if s, c, ok, _ := WindowStep(start, count, 1300, 90, 300); !ok || s != 1300 || c != 1 {
		t.Fatalf("after the window: %d %d %v, want a new window", s, c, ok)
	}
	if s, c, ok, _ := WindowStep(start, count, 900, 90, 300); !ok || s != 900 || c != 1 {
		t.Fatalf("clock behind the window: %d %d %v, want a new window", s, c, ok)
	}
}

func TestTemplatesParse(t *testing.T) {
	for _, name := range []string{"home", "access-ended", "pick", "documents", "invoices", "client-messages", "account",
		"inbox", "staff-thread", "matter-messages", "staff-new", "widget", "search", "access", "expired", "message"} {
		if pages.Lookup(name) == nil {
			t.Errorf("template %q missing", name)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"/client/messages": "/client/messages", "https://evil.example/client/x": "/client/home",
		"/staff/inbox": "/client/home", "/client/../staff": "/client/home", "//evil.example/client/": "/client/home"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewIDKeepsOrderWithinAMillisecond(t *testing.T) {
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	prev := newID(now)
	for i := 0; i < 5000; i++ {
		id := newID(now)
		if !validID(id) || id <= prev || id[14] != '7' {
			t.Fatalf("id %d: %s after %s", i, id, prev)
		}
		prev = id
	}
}
