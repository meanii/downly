package i18n

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// verbRE matches fmt verbs, with an optional explicit argument index.
var verbRE = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?([a-zA-Z%])`)

// verbs maps argument index (1-based) to the verb used for it.
func verbs(t *testing.T, msg string) map[int]string {
	t.Helper()
	out := map[int]string{}
	next := 1
	for _, m := range verbRE.FindAllStringSubmatch(msg, -1) {
		if m[3] == "%" {
			continue
		}
		idx := next
		if m[1] != "" {
			idx, _ = strconv.Atoi(strings.Trim(m[1], "[]"))
		}
		next = idx + 1
		if prev, ok := out[idx]; ok && prev != m[3] {
			t.Errorf("argument %d used as both %%%s and %%%s in %q", idx, prev, m[3], msg)
		}
		out[idx] = m[3]
	}
	return out
}

func TestCatalogsAreComplete(t *testing.T) {
	ref := catalog[Default]
	for _, info := range Languages {
		c, ok := catalog[info.Code]
		if !ok {
			t.Fatalf("no catalog for %s", info.Code)
		}
		for key, enMsg := range ref {
			msg, ok := c[key]
			if !ok {
				t.Errorf("%s: missing key %q", info.Code, key)
				continue
			}
			if strings.TrimSpace(msg) == "" {
				t.Errorf("%s: empty message for %q", info.Code, key)
			}
			if !utf8.ValidString(msg) {
				t.Errorf("%s: invalid UTF-8 in %q", info.Code, key)
			}
			want, got := verbs(t, enMsg), verbs(t, msg)
			if len(want) != len(got) {
				t.Errorf("%s/%s: %d format args, English has %d", info.Code, key, len(got), len(want))
				continue
			}
			for i, v := range want {
				if got[i] != v {
					t.Errorf("%s/%s: argument %d is %%%s, English uses %%%s", info.Code, key, i, got[i], v)
				}
			}
		}
		for key := range c {
			if _, ok := ref[key]; !ok {
				t.Errorf("%s: key %q does not exist in English", info.Code, key)
			}
		}
	}
}

func TestTranslationsDiffer(t *testing.T) {
	// Guards against copy-pasting English into another catalog.
	for _, info := range Languages {
		if info.Code == Default {
			continue
		}
		if T(info.Code, "start", "bot") == T(Default, "start", "bot") {
			t.Errorf("%s start message is identical to English", info.Code)
		}
	}
}

func TestTFormatsAndFallsBack(t *testing.T) {
	if got := T(RU, "queued", 5, 1, 2, 3); !strings.Contains(got, "#5") || !strings.Contains(got, "в очереди") {
		t.Fatalf("got %q", got)
	}
	if got := T(HI, "playlist_done", 3, 10); !strings.Contains(got, "10 में से 3") {
		t.Fatalf("explicit argument order broken: %q", got)
	}
	if got := T(Lang("xx"), "invalid_url"); got != "Invalid link." {
		t.Fatalf("unknown language should fall back to English, got %q", got)
	}
	if got := T(EN, "no_such_key"); got != "no_such_key" {
		t.Fatalf("missing key should return the key, got %q", got)
	}
}

func TestGuessAndParse(t *testing.T) {
	cases := map[string]Lang{"ru": RU, "ru-RU": RU, "fa-IR": FA, "HI": HI, "en-US": EN, "de": EN, "": EN, "pt_BR": EN}
	for in, want := range cases {
		if got := Guess(in); got != want {
			t.Errorf("Guess(%q) = %s, want %s", in, got, want)
		}
	}
	if _, ok := Parse("xx"); ok {
		t.Error("Parse should reject unknown codes")
	}
	if l, ok := Parse("fa"); !ok || l != FA {
		t.Error("Parse(fa)")
	}
}

func TestLabelsAndPrompt(t *testing.T) {
	if Label(RU) != "🇷🇺 Русский" || Label(FA) != "🇮🇷 فارسی" || Label(HI) != "🇮🇳 हिन्दी" || Label(EN) != "🇬🇧 English" {
		t.Fatal("unexpected labels")
	}
	p := PickerPrompt()
	for _, info := range Languages {
		if !strings.Contains(p, catalog[info.Code]["lang_prompt"]) {
			t.Errorf("picker prompt missing %s", info.Code)
		}
	}
}
