package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Command bar (Ctrl+Alt+K by default): type, see results, Enter runs the first one.

type CmdResult struct {
	Icon   string // "calc", "app", "web", "money", "unit", "cmd", "remind", "todo", "link", "trans"
	Title  string
	Sub    string
	Action string // what Enter does, e.g. "copy:42", "open:C:\\x.lnk", "cmd:settings"
}

// ---------- calculator ----------

type calc struct {
	s   []rune
	pos int
}

func (c *calc) peek() rune {
	for c.pos < len(c.s) && c.s[c.pos] == ' ' {
		c.pos++
	}
	if c.pos < len(c.s) {
		return c.s[c.pos]
	}
	return 0
}

func (c *calc) expr() (float64, error) {
	v, err := c.term()
	for err == nil {
		switch c.peek() {
		case '+':
			c.pos++
			var r float64
			r, err = c.term()
			v += r
		case '-', '−':
			c.pos++
			var r float64
			r, err = c.term()
			v -= r
		default:
			return v, nil
		}
	}
	return v, err
}

func (c *calc) term() (float64, error) {
	v, err := c.power()
	for err == nil {
		switch c.peek() {
		case '*', '×', 'x':
			c.pos++
			var r float64
			r, err = c.power()
			v *= r
		case '/', ':', '÷':
			c.pos++
			var r float64
			r, err = c.power()
			if r == 0 {
				return 0, errors.New("dalyba iš nulio")
			}
			v /= r
		case '%':
			c.pos++
			var r float64
			r, err = c.power()
			v = math.Mod(v, r)
		default:
			return v, nil
		}
	}
	return v, err
}

func (c *calc) power() (float64, error) {
	v, err := c.unary()
	if err == nil && c.peek() == '^' {
		c.pos++
		r, e := c.power()
		return math.Pow(v, r), e
	}
	return v, err
}

func (c *calc) unary() (float64, error) {
	switch c.peek() {
	case '-', '−':
		c.pos++
		v, err := c.unary()
		return -v, err
	case '+':
		c.pos++
		return c.unary()
	case '(':
		c.pos++
		v, err := c.expr()
		if c.peek() != ')' {
			return 0, errors.New(")")
		}
		c.pos++
		return v, err
	case '√':
		c.pos++
		v, err := c.unary()
		return math.Sqrt(v), err
	}
	start := c.pos
	for c.pos < len(c.s) && (unicode.IsDigit(c.s[c.pos]) || c.s[c.pos] == '.' || c.s[c.pos] == ',') {
		c.pos++
	}
	if start == c.pos {
		return 0, errors.New("skaičius")
	}
	return strconv.ParseFloat(strings.ReplaceAll(string(c.s[start:c.pos]), ",", "."), 64)
}

var reCalcChars = regexp.MustCompile(`^[0-9\s.,+\-−*/×÷:%^()√]+$`)

// Calc evaluates "2+2*5", "(3,5+1)^2", "√16".
func Calc(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "="))
	if !reCalcChars.MatchString(s) || !strings.ContainsAny(s, "+-−*/×÷:%^√") {
		return 0, false
	}
	c := &calc{s: []rune(s)}
	v, err := c.expr()
	if err != nil || c.peek() != 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func fmtNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if math.Abs(v) >= 1e15 || (math.Abs(v) < 1e-6 && v != 0) {
		s = strconv.FormatFloat(v, 'g', 10, 64)
	} else if strings.Contains(s, ".") && len(s) > 14 {
		s = strconv.FormatFloat(v, 'f', 6, 64)
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if curLang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// ---------- units ----------

var units = map[string]struct {
	to  string
	f   func(float64) float64
	nam string
}{
	"km": {"mi", func(v float64) float64 { return v * 0.621371 }, "mylios"}, "mi": {"km", func(v float64) float64 { return v / 0.621371 }, "km"},
	"kg": {"lb", func(v float64) float64 { return v * 2.20462 }, "svarai"}, "lb": {"kg", func(v float64) float64 { return v / 2.20462 }, "kg"},
	"c": {"f", func(v float64) float64 { return v*9/5 + 32 }, "°F"}, "f": {"c", func(v float64) float64 { return (v - 32) * 5 / 9 }, "°C"},
	"m": {"ft", func(v float64) float64 { return v * 3.28084 }, "pėdos"}, "ft": {"m", func(v float64) float64 { return v / 3.28084 }, "m"},
	"cm": {"in", func(v float64) float64 { return v / 2.54 }, "coliai"}, "in": {"cm", func(v float64) float64 { return v * 2.54 }, "cm"},
	"l": {"gal", func(v float64) float64 { return v / 3.78541 }, "galonai"}, "gal": {"l", func(v float64) float64 { return v * 3.78541 }, "l"},
}

var reAmount = regexp.MustCompile(`^(?i)\s*([0-9]+(?:[.,][0-9]+)?)\s*°?\s*([a-z]{1,3})\s*(?:(?:i|į|to|in|в)\s+)?(?:°?\s*([a-z]{1,3}))?\s*$`)

// ---------- currency (ECB rates via Frankfurter, free, no account) ----------

type rateCache struct {
	mu    sync.Mutex
	rates map[string]map[string]float64
	at    map[string]time.Time
	busy  map[string]bool
}

var rates = &rateCache{rates: map[string]map[string]float64{}, at: map[string]time.Time{}, busy: map[string]bool{}}
var ratesReady func()

var currencies = map[string]bool{"EUR": true, "USD": true, "GBP": true, "PLN": true, "SEK": true, "NOK": true, "DKK": true, "CHF": true,
	"JPY": true, "CNY": true, "CZK": true, "HUF": true, "CAD": true, "AUD": true, "UAH": false, "TRY": true, "INR": true, "BGN": true, "RON": true}

func (r *rateCache) get(base string) (map[string]float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.rates[base]; ok && time.Since(r.at[base]) < time.Hour {
		return m, true
	}
	if !r.busy[base] {
		r.busy[base] = true
		go func() {
			var out struct {
				Rates map[string]float64 `json:"rates"`
			}
			c := &http.Client{Timeout: 8 * time.Second}
			resp, err := c.Get("https://api.frankfurter.app/latest?from=" + base)
			if err == nil {
				json.NewDecoder(resp.Body).Decode(&out)
				resp.Body.Close()
			}
			r.mu.Lock()
			r.busy[base] = false
			if len(out.Rates) > 0 {
				out.Rates[base] = 1
				r.rates[base] = out.Rates
				r.at[base] = time.Now()
			}
			r.mu.Unlock()
			if ratesReady != nil {
				ratesReady()
			}
		}()
	}
	return nil, false
}

// ---------- installed apps (Start menu shortcuts) ----------

type appEntry struct{ Name, Path string }

var (
	appsMu  sync.Mutex
	appList []appEntry
)

func indexApps() {
	var out []appEntry
	seen := map[string]bool{}
	dirs := []string{os.Getenv("ProgramData") + `\Microsoft\Windows\Start Menu\Programs`, os.Getenv("APPDATA") + `\Microsoft\Windows\Start Menu\Programs`}
	for _, d := range dirs {
		if d == `\Microsoft\Windows\Start Menu\Programs` {
			continue
		}
		filepath.WalkDir(d, func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".lnk" && ext != ".url" {
				return nil
			}
			name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			l := strings.ToLower(name)
			if strings.Contains(l, "uninstall") || strings.Contains(l, "pašalinti") || seen[l] {
				return nil
			}
			seen[l] = true
			out = append(out, appEntry{name, p})
			return nil
		})
	}
	appsMu.Lock()
	appList = out
	appsMu.Unlock()
}

func findApps(q string, limit int) []appEntry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	appsMu.Lock()
	defer appsMu.Unlock()
	type scored struct {
		e appEntry
		s int
	}
	var res []scored
	for _, e := range appList {
		n := strings.ToLower(e.Name)
		s := 0
		switch {
		case n == q:
			s = 100
		case strings.HasPrefix(n, q):
			s = 80
		case strings.Contains(" "+n, " "+q):
			s = 60
		case strings.Contains(n, q):
			s = 40
		}
		if s > 0 {
			res = append(res, scored{e, s - len(n)/8})
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].s > res[j].s })
	var out []appEntry
	for i := 0; i < len(res) && i < limit; i++ {
		out = append(out, res[i].e)
	}
	return out
}

// ---------- evaluation ----------

type cmdWord struct {
	words  []string
	action string
	title  string
}

var cmdWords = []cmdWord{
	{[]string{"nustatymai", "settings", "настройки"}, "cmd:settings", "Atidaryti nustatymus"},
	{[]string{"karaoke", "караоке"}, "cmd:karaoke", "Karaoke režimas"},
	{[]string{"orai", "weather", "погода"}, "cmd:weather", "Orai"},
	{[]string{"laikmatis", "timer", "таймер", "pomodoro"}, "cmd:timer", "Paleisti fokusavimo laikmatį"},
	{[]string{"netrukdyti", "dnd", "не беспокоить"}, "cmd:dnd", "Netrukdyti režimas (įjungti / išjungti)"},
	{[]string{"spalva", "color", "цвет", "pipete"}, "cmd:color", "Spalvų rinkiklis"},
	{[]string{"žaidimas", "zaidimas", "game", "игра"}, "cmd:game", "Žaidimas"},
	{[]string{"nuotrauka", "screenshot", "скриншот"}, "cmd:shot", "Ekrano nuotrauka (sritis)"},
	{[]string{"užrašas", "uzrasas", "note", "заметка"}, "cmd:note", "Atidaryti užrašą"},
	{[]string{"lentyna", "shelf", "полка"}, "cmd:shelf", "Failų lentyna"},
	{[]string{"mėgstamos", "megstamos", "favorites", "избранное"}, "cmd:favs", "Mėgstamos dainos"},
}

var (
	reRemind = regexp.MustCompile(`(?i)^(?:priminimas|priminti|primink|remind|reminder|напомни|напоминание)\s+(?:po\s+|in\s+|через\s+)?(\d+)\s*(min|m|val|h|ч|мин)?\.?\s*(.*)$`)
	reTodo   = regexp.MustCompile(`(?i)^(?:užduotis|uzduotis|todo|task|задача)\s+(.+)$`)
	reTrans  = regexp.MustCompile(`(?i)^(?:versti|translate|перевод|перевести)\s+(.+)$`)
	reSearch = regexp.MustCompile(`(?i)^(g|google|yt|youtube|wiki)\s+(.+)$`)
)

// EvalCommand turns the typed text into results (best first).
func EvalCommand(in string, links []Link) []CmdResult {
	q := strings.TrimSpace(in)
	if q == "" {
		return []CmdResult{
			{"cmd", L("Pvz.: 2+2*5"), L("skaičiuotuvas"), ""},
			{"cmd", L("Pvz.: 10 eur usd · 5 km mi"), L("valiuta ir vienetai"), ""},
			{"cmd", L("Pvz.: spotify · orai · priminimas 10 min arbata"), L("programos ir komandos"), ""},
		}
	}
	var out []CmdResult
	lq := strings.ToLower(q)

	if v, ok := Calc(q); ok {
		out = append(out, CmdResult{"calc", "= " + fmtNum(v), L("Enter – nukopijuoti"), "copy:" + fmtNum(v)})
	}
	if m := reAmount.FindStringSubmatch(lq); m != nil {
		v, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		from, to := strings.ToUpper(m[2]), strings.ToUpper(m[3])
		if currencies[from] {
			if to == "" {
				to = "USD"
				if from == "USD" {
					to = "EUR"
				}
			}
			if currencies[to] {
				if rs, ok := rates.get(from); ok && rs[to] > 0 {
					r := v * rs[to]
					out = append(out, CmdResult{"money", fmt.Sprintf("%s %s = %s %s", fmtNum(v), from, fmtNum(math.Round(r*100)/100), to),
						L("Europos centrinio banko kursas · Enter – nukopijuoti"), "copy:" + fmtNum(math.Round(r*100)/100)})
				} else {
					out = append(out, CmdResult{"money", fmt.Sprintf("%s %s → %s", fmtNum(v), from, to), L("Gaunamas valiutos kursas…"), ""})
				}
			}
		} else if u, ok := units[strings.ToLower(m[2])]; ok && (m[3] == "" || strings.EqualFold(m[3], u.to)) {
			r := u.f(v)
			out = append(out, CmdResult{"unit", fmt.Sprintf("%s %s = %s %s", fmtNum(v), m[2], fmtNum(math.Round(r*100)/100), u.to),
				L("Enter – nukopijuoti"), "copy:" + fmtNum(math.Round(r*100)/100)})
		}
	}
	if m := reRemind.FindStringSubmatch(q); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := strings.ToLower(m[2])
		mins := n
		if unit == "val" || unit == "h" || unit == "ч" {
			mins = n * 60
		}
		text := strings.TrimSpace(m[3])
		if text == "" {
			text = L("Priminimas")
		}
		out = append(out, CmdResult{"remind", fmt.Sprintf(L("Priminti po %d min: %s"), mins, text), L("Enter – sukurti priminimą"),
			fmt.Sprintf("remind:%d:%s", mins, text)})
	}
	if m := reTodo.FindStringSubmatch(q); m != nil {
		out = append(out, CmdResult{"todo", L("Nauja užduotis: ") + m[1], L("Enter – pridėti į sąrašą"), "todo:" + m[1]})
	}
	if m := reTrans.FindStringSubmatch(q); m != nil {
		out = append(out, CmdResult{"trans", L("Išversti: ") + m[1], L("Enter – versti"), "trans:" + m[1]})
	}
	for _, c := range cmdWords {
		for _, w := range c.words {
			if strings.HasPrefix(w, lq) && len(lq) >= 2 {
				out = append(out, CmdResult{"cmd", L(c.title), L("komanda"), c.action})
				break
			}
		}
	}
	for _, l := range links {
		if strings.Contains(strings.ToLower(l.Name), lq) {
			out = append(out, CmdResult{"link", l.Name, l.Target, "open:" + l.Target})
		}
	}
	for _, a := range findApps(q, 4) {
		out = append(out, CmdResult{"app", a.Name, L("programa"), "open:" + a.Path})
	}
	if m := reSearch.FindStringSubmatch(q); m != nil {
		site := strings.ToLower(m[1])
		u := "https://www.google.com/search?q="
		name := "Google"
		switch site {
		case "yt", "youtube":
			u, name = "https://www.youtube.com/results?search_query=", "YouTube"
		case "wiki":
			u, name = "https://"+map[string]string{"lt": "lt", "en": "en", "ru": "ru"}[curLang]+".wikipedia.org/w/index.php?search=", "Wikipedia"
		}
		out = append(out, CmdResult{"web", fmt.Sprintf(L("Ieškoti %s: %s"), name, m[2]), "", "open:" + u + queryEscape(m[2])})
	} else if len(out) == 0 || len(q) > 2 {
		out = append(out, CmdResult{"web", L("Ieškoti internete: ") + q, "Google", "open:https://www.google.com/search?q=" + queryEscape(q)})
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func queryEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
