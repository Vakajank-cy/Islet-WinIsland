package main

import (
	"fmt"
	"strings"
	"time"
)

// UI language: "lt" (source texts), "en" or "ru".
var curLang = "lt"

func setLang(l string) {
	switch l {
	case "en", "ru":
		curLang = l
	default:
		curLang = "lt"
	}
}

// L translates a Lithuanian UI text into the current language.
// Texts that start with a known phrase ("Prijungta: …") are translated
// by that phrase.
func L(s string) string {
	if curLang == "lt" || s == "" {
		return s
	}
	idx := 0
	if curLang == "ru" {
		idx = 1
	}
	if t, ok := dict[s]; ok && t[idx] != "" {
		return t[idx]
	}
	// "Naudoja Foo", "Prijungta: Bar" – translate the known beginning
	for _, k := range prefixKeys {
		if strings.HasPrefix(s, k) {
			if t := dict[k][idx]; t != "" {
				return t + s[len(k):]
			}
		}
	}
	return s
}

var prefixKeys = []string{"Naudoja ", "Prijungta: ", "Atjungta: ", "Priminimas: ", "Atsisiųsta: ", "Garsas: "}

func dayNames() []string {
	switch curLang {
	case "en":
		return []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	case "ru":
		return []string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}
	}
	return []string{"Sekmadienis", "Pirmadienis", "Antradienis", "Trečiadienis", "Ketvirtadienis", "Penktadienis", "Šeštadienis"}
}

func shortDay(t time.Time) string {
	switch curLang {
	case "en":
		return []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}[t.Weekday()]
	case "ru":
		return []string{"Вс", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб"}[t.Weekday()]
	}
	return []string{"Sk", "Pr", "An", "Tr", "Kt", "Pn", "Št"}[t.Weekday()]
}

func monthGen(t time.Time) string {
	m := int(t.Month()) - 1
	switch curLang {
	case "en":
		return []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}[m]
	case "ru":
		return []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}[m]
	}
	return []string{"sausio", "vasario", "kovo", "balandžio", "gegužės", "birželio", "liepos", "rugpjūčio", "rugsėjo", "spalio", "lapkričio", "gruodžio"}[m]
}

// shortDate: "Kt, lapkričio 19" / "Thu, November 19" / "Чт, 19 ноября"
func shortDate(t time.Time) string {
	if curLang == "ru" {
		return fmt.Sprintf("%s, %d %s", shortDay(t), t.Day(), monthGen(t))
	}
	return fmt.Sprintf("%s, %s %d", shortDay(t), monthGen(t), t.Day())
}

// longDate: "Ketvirtadienis, lapkričio 19 d."
func longDate(t time.Time) string {
	d := dayNames()[t.Weekday()]
	switch curLang {
	case "en":
		return fmt.Sprintf("%s, %s %d", d, monthGen(t), t.Day())
	case "ru":
		return fmt.Sprintf("%s, %d %s", strings.Title(d), t.Day(), monthGen(t))
	}
	return fmt.Sprintf("%s, %s %d d.", d, monthGen(t), t.Day())
}

func ltDate(t time.Time) string { return longDate(t) }

// filesWord: 1 failas / 2 failai / 10 failų (and EN/RU forms)
func filesWord(n int) string {
	switch curLang {
	case "en":
		if n == 1 {
			return "file"
		}
		return "files"
	case "ru":
		return pluralForm(n, "файл", "файла", "файлов")
	}
	return pluralForm(n, "failas", "failai", "failų")
}

// pluralForm works for Lithuanian and Russian (one / few / many).
func pluralForm(n int, one, few, many string) string {
	if curLang == "ru" {
		switch {
		case n%10 == 1 && n%100 != 11:
			return one
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return few
		}
		return many
	}
	switch {
	case n%10 == 1 && n%100 != 11:
		return one
	case n%10 >= 2 && n%10 <= 9 && (n%100 < 11 || n%100 > 19):
		return few
	}
	return many
}

// dict: Lithuanian text -> {English, Russian}. Filled in i18n_dict.go.
var dict = map[string][2]string{}
