// Package i18n holds the bot's user-facing text in every supported language.
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a supported language code.
type Lang string

const (
	EN Lang = "en"
	RU Lang = "ru"
	HI Lang = "hi"
	FA Lang = "fa"
)

// Default is used when nothing better is known.
const Default = EN

// Info describes a language for pickers.
type Info struct {
	Code Lang
	Name string // native name
	Flag string
}

// Languages lists the supported languages in picker order.
var Languages = []Info{
	{EN, "English", "🇬🇧"},
	{RU, "Русский", "🇷🇺"},
	{HI, "हिन्दी", "🇮🇳"},
	{FA, "فارسی", "🇮🇷"},
}

// Parse returns the language for code, if supported.
func Parse(code string) (Lang, bool) {
	for _, l := range Languages {
		if string(l.Code) == code {
			return l.Code, true
		}
	}
	return "", false
}

// Guess maps a Telegram language_code (e.g. "ru", "fa-IR", "en-US") to a
// supported language, falling back to Default.
func Guess(code string) Lang {
	code = strings.ToLower(code)
	if i := strings.IndexAny(code, "-_"); i >= 0 {
		code = code[:i]
	}
	if l, ok := Parse(code); ok {
		return l
	}
	return Default
}

// Label returns "🇬🇧 English" for l.
func Label(l Lang) string {
	for _, info := range Languages {
		if info.Code == l {
			return info.Flag + " " + info.Name
		}
	}
	return string(l)
}

// T returns the message key in language l, formatted with args. Missing
// translations fall back to English, then to the key itself.
func T(l Lang, key string, args ...any) string {
	msg, ok := catalog[l][key]
	if !ok {
		msg, ok = catalog[Default][key]
	}
	if !ok {
		msg = key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// PickerPrompt is shown before a language is chosen, so it is written in
// every language at once.
func PickerPrompt() string {
	parts := make([]string, 0, len(Languages))
	for _, l := range Languages {
		parts = append(parts, catalog[l.Code]["lang_prompt"])
	}
	return strings.Join(parts, "\n")
}
