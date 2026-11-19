package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The settings and karaoke pages are small local web pages served by the
// island itself at http://127.0.0.1:47821 — easier to type text there than
// in a tiny always-on-top strip. Only this computer can open them.

type SettingsDTO struct {
	Theme      string `json:"theme"` // "dark" | "light"
	Size       int    `json:"size"`  // 0 small, 1 normal, 2 large
	Monitor    int    `json:"monitor"`
	RobotColor int    `json:"robotColor"`
	Face       int    `json:"face"`
	Sound      bool   `json:"sound"`
	Notify     bool   `json:"notify"`
	LyricsBar  bool   `json:"lyricsBar"`
	Translate  bool   `json:"translate"`
	AutoHide   bool   `json:"autoHide"`
	Kbd        bool   `json:"kbd"`
	Devices    bool   `json:"devices"`
	NetNotify  bool   `json:"netNotify"`
	Autostart  bool   `json:"autostart"`
	Hooks      bool   `json:"hooks"`
	City       string `json:"city"`
	Links      string `json:"links"`

	Lang        string   `json:"lang"`
	CoverGlow   bool     `json:"coverGlow"`
	KaraokeFill bool     `json:"karaokeFill"`
	ClipTrans   bool     `json:"clipTrans"`
	MicCam      bool     `json:"micCam"`
	Downloads   bool     `json:"downloads"`
	Messages    bool     `json:"messages"`
	Moods       bool     `json:"moods"`
	Fact        bool     `json:"fact"`
	DND         bool     `json:"dnd"`
	DNDFocus    bool     `json:"dndFocus"`
	CalURL      string   `json:"calUrl"`
	Birthdays   string   `json:"birthdays"`
	Clocks      []string `json:"clocks"`
	Cities      []string `json:"cities,omitempty"`    // read-only: keys
	CityNames   []string `json:"cityNames,omitempty"` // read-only: shown names

	Keys    map[string]string `json:"keys,omitempty"`    // shortcut per action ("default" = reset)
	KeyBusy map[string]string `json:"keyBusy,omitempty"` // read-only: wanted but taken
	Notice  string            `json:"notice,omitempty"`  // read-only: one-off message

	Monitors []string `json:"monitors,omitempty"` // read-only
	Colors   []string `json:"colors,omitempty"`
	Faces    []string `json:"faces,omitempty"`
}

type LineDTO struct {
	T  float64 `json:"t"`
	Tx string  `json:"text"`
	Tr string  `json:"tr,omitempty"`
}

type NowDTO struct {
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	Artist    string    `json:"artist"`
	Playing   bool      `json:"playing"`
	Pos       float64   `json:"pos"`
	End       float64   `json:"end"`
	Estimated bool      `json:"estimated"`
	State     int       `json:"state"`
	Lines     []LineDTO `json:"lines,omitempty"`
	Plain     string    `json:"plain,omitempty"`
	Offset    float64   `json:"offset"`
	HasCover  bool      `json:"hasCover"`
	Colors    [2]string `json:"colors"`
}

// Backend is implemented by the Windows app (calls run on its UI thread).
type Backend interface {
	Settings() SettingsDTO
	ApplySettings(SettingsDTO) error
	Reminders() []Reminder
	AddReminder(text string, at time.Time)
	DelReminder(id int)
	Now() NowDTO
	Cover() []byte
	AdjustOffset(delta float64)
	Favs() []Fav
	DelFav(i int)
	Todos() []Todo
	AddTodo(text string)
	ToggleTodo(id int)
	DelTodo(id int)
}

var (
	webBackend Backend
	webToken   = newToken()
)

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func settingsURL(section string) string {
	u := "http://" + listenAddr + "/nustatymai"
	if section != "" {
		u += "#" + section
	}
	return u
}

var karaokeTexts = map[string][2]string{
	"Laukiama muzikos…":        {"Waiting for music…", "Ждём музыку…"},
	"Visas ekranas":            {"Full screen", "Во весь экран"},
	"Šiuo metu niekas negroja": {"Nothing is playing right now", "Сейчас ничего не играет"},
	"Niekas negroja":           {"Nothing playing", "Ничего не играет"},
	"Laikas apytikslis – jei žodžiai vėluoja, naudok ±0,5 s": {"Approximate time – use ±0.5 s if the lyrics lag", "Время примерное – используйте ±0,5 с"},
	"Ieškoma žodžių…":                           {"Looking for lyrics…", "Ищем текст…"},
	"Instrumentinė daina":                       {"Instrumental", "Инструментальная"},
	"Šiai dainai yra tik žodžiai be laiko žymų": {"Only untimed lyrics exist for this song", "Есть только текст без таймингов"},
	"Žodžių nerasta":                            {"No lyrics found", "Текст не найден"},
}

func karaokeURL() string { return "http://" + listenAddr + "/karaoke" }

func localHost(r *http.Request) bool {
	h := r.Host
	return h == listenAddr || h == "localhost:47821"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func registerWeb(mux *http.ServeMux) {
	page := func(html string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !localHost(r) || webBackend == nil {
				http.Error(w, "nepasiekiama", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			out := strings.Replace(html, "{{TOKEN}}", webToken, 1)
			if html == karaokeHTML && curLang != "lt" {
				for lt, tr := range karaokeTexts {
					t := tr[0]
					if curLang == "ru" {
						t = tr[1]
					}
					out = strings.ReplaceAll(out, lt, t)
				}
			}
			w.Write([]byte(out))
		}
	}
	mux.HandleFunc("/nustatymai", page(settingsHTML))
	mux.HandleFunc("/karaoke", page(karaokeHTML))

	api := func(h func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !localHost(r) || webBackend == nil || r.Header.Get("X-Island-Token") != webToken {
				http.Error(w, "draudžiama", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/settings", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var s SettingsDTO
			if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err := webBackend.ApplySettings(s); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		writeJSON(w, webBackend.Settings())
	}))
	mux.HandleFunc("/api/reminders", api(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var in struct {
				Text string `json:"text"`
				At   string `json:"at"` // "2006-01-02T15:04" local time
				In   int    `json:"in"` // or: minutes from now
			}
			json.NewDecoder(r.Body).Decode(&in)
			at := time.Now().Add(time.Duration(in.In) * time.Minute)
			if in.At != "" {
				t, err := time.ParseInLocation("2006-01-02T15:04", in.At, time.Local)
				if err != nil {
					http.Error(w, "blogas laikas", 400)
					return
				}
				at = t
			}
			webBackend.AddReminder(in.Text, at)
		case http.MethodDelete:
			id, _ := strconv.Atoi(r.URL.Query().Get("id"))
			webBackend.DelReminder(id)
		}
		writeJSON(w, webBackend.Reminders())
	}))
	mux.HandleFunc("/api/favs", api(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			i, _ := strconv.Atoi(r.URL.Query().Get("i"))
			webBackend.DelFav(i)
		}
		writeJSON(w, webBackend.Favs())
	}))
	mux.HandleFunc("/api/todos", api(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		switch r.Method {
		case http.MethodPost:
			var in struct {
				Text string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			webBackend.AddTodo(in.Text)
		case http.MethodPatch:
			webBackend.ToggleTodo(id)
		case http.MethodDelete:
			webBackend.DelTodo(id)
		}
		writeJSON(w, webBackend.Todos())
	}))
	mux.HandleFunc("/api/now", api(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, webBackend.Now())
	}))
	mux.HandleFunc("/api/offset", api(func(w http.ResponseWriter, r *http.Request) {
		d, _ := strconv.ParseFloat(r.URL.Query().Get("d"), 64)
		webBackend.AdjustOffset(d)
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/cover", func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r) || webBackend == nil || r.URL.Query().Get("t") != webToken {
			http.Error(w, "draudžiama", http.StatusForbidden)
			return
		}
		b := webBackend.Cover()
		if len(b) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(b)
	})
}

const settingsHTML = `<!DOCTYPE html>
<html lang="lt"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Islet</title>
<style>
:root{--bg:#0b0b0d;--card:#151518;--line:#26262b;--text:#f2f2f5;--muted:#9a9aa3;--acc:#0a84ff;--ok:#30d158}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 "Segoe UI",system-ui,sans-serif}
.wrap{max-width:860px;margin:0 auto;padding:28px 20px 80px}
h1{font-size:26px;margin:0 0 4px}.sub{color:var(--muted);margin:0 0 18px}
nav{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:18px;position:sticky;top:0;background:var(--bg);padding:10px 0;z-index:2}
nav a{color:var(--muted);text-decoration:none;padding:6px 12px;border-radius:99px;background:var(--card);font-size:13px}
nav a:hover{color:var(--text)}
section{background:var(--card);border:1px solid var(--line);border-radius:18px;padding:20px 22px;margin-bottom:16px}
h2{font-size:17px;margin:0 0 12px}
.row{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:9px 0;border-top:1px solid var(--line)}
.row:first-of-type{border-top:0}.row small{display:block;color:var(--muted);font-size:12.5px}
.seg{display:inline-flex;flex-wrap:wrap;background:#0e0e10;border:1px solid var(--line);border-radius:18px;padding:3px}
.seg button{border:0;background:none;color:var(--muted);padding:6px 14px;border-radius:99px;cursor:pointer;font:inherit;font-size:13px}
.seg button.on{background:var(--text);color:#000}
.sw{position:relative;width:44px;height:26px;flex:none}.sw input{display:none}
.sw span{position:absolute;inset:0;background:#3a3a3f;border-radius:99px;transition:.2s;cursor:pointer}
.sw span:after{content:"";position:absolute;left:3px;top:3px;width:20px;height:20px;border-radius:50%;background:#fff;transition:.2s}
.sw input:checked+span{background:var(--ok)}.sw input:checked+span:after{left:21px}
.swatches{display:flex;gap:10px;flex-wrap:wrap}.swatch{width:30px;height:30px;border-radius:50%;cursor:pointer;border:3px solid transparent}
.swatch.on{border-color:#fff}
input[type=text],input[type=datetime-local],textarea,select{background:#0e0e10;color:var(--text);border:1px solid var(--line);border-radius:10px;padding:9px 12px;font:inherit}
textarea{width:100%;min-height:130px;font-family:Consolas,monospace;font-size:13px}
.btn{background:var(--text);color:#000;border:0;border-radius:99px;padding:9px 18px;font:inherit;font-weight:600;cursor:pointer}
.btn.ghost{background:#2a2a2f;color:var(--text)}
.item{display:flex;align-items:center;gap:12px;padding:9px 12px;background:#0e0e10;border-radius:12px;margin-top:8px}
.item b{color:#ff9f0a;min-width:110px}.item span{flex:1}.item button{background:none;border:0;color:var(--muted);cursor:pointer;font-size:18px}
.item.done span{text-decoration:line-through;color:var(--muted)}.item input[type=checkbox]{width:18px;height:18px}
.add{display:flex;gap:8px;flex-wrap:wrap;margin-top:12px}.add input[type=text]{flex:1;min-width:200px}
.chips{display:flex;flex-wrap:wrap;gap:8px}.chip{padding:6px 12px;border-radius:99px;background:#0e0e10;border:1px solid var(--line);cursor:pointer;font-size:13px;color:var(--muted)}
.chip.on{background:var(--acc);color:#fff;border-color:var(--acc)}
.toast{position:fixed;bottom:22px;left:50%;transform:translateX(-50%);background:var(--text);color:#000;padding:8px 18px;border-radius:99px;font-weight:600;opacity:0;transition:.25s}
.toast.on{opacity:1}
.hint{color:var(--muted);font-size:12.5px;margin-top:8px}
kbd{background:#2a2a2f;border-radius:6px;padding:1px 7px;font-size:12px}
.kk{display:flex;align-items:center;gap:6px}.mini{background:#2a2a2f;color:inherit;border:0;border-radius:8px;padding:4px 10px;font:inherit;font-size:12px;cursor:pointer}.mini:hover{background:#3a3a40}
small.busy{color:#ff9f0a}
</style></head><body><div class="wrap">
<h1>Islet</h1><p class="sub" data-t="sub"></p>
<nav>
<a href="#kalba" data-t="n_lang"></a><a href="#isvaizda" data-t="n_look"></a><a href="#funkcijos" data-t="n_feat"></a><a href="#uzduotys" data-t="n_todo"></a>
<a href="#priminimai" data-t="n_rem"></a><a href="#kalendorius" data-t="n_cal"></a><a href="#gimtadieniai" data-t="n_bday"></a><a href="#orai" data-t="n_weather"></a>
<a href="#nuorodos" data-t="n_links"></a><a href="#megstamos" data-t="n_favs"></a><a href="#klavisai" data-t="n_keys"></a><a href="/karaoke" target="_blank">Karaoke ↗</a></nav>

<section id="kalba"><h2 data-t="n_lang"></h2>
<div class="row"><div data-t="lang"></div><div class="seg" data-k="lang"><button data-v="lt">Lietuvių</button><button data-v="en">English</button><button data-v="ru">Русский</button></div></div>
</section>

<section id="isvaizda"><h2 data-t="n_look"></h2>
<div class="row"><div data-t="theme"></div><div class="seg" data-k="theme"><button data-v="dark" data-t="dark"></button><button data-v="light" data-t="light"></button></div></div>
<div class="row"><div data-t="size"></div><div class="seg" data-k="size"><button data-v="0" data-t="small"></button><button data-v="1" data-t="medium"></button><button data-v="2" data-t="large"></button></div></div>
<div class="row"><div><span data-t="monitor"></span><small data-t="monitor_h"></small></div><select id="monitor"></select></div>
<div class="row"><div data-t="color"></div><div class="swatches" id="colors"></div></div>
<div class="row"><div data-t="face"></div><div class="seg" data-k="face" id="faces"></div></div>
</section>

<section id="funkcijos"><h2 data-t="n_feat"></h2><div id="toggles"></div></section>

<section id="uzduotys"><h2 data-t="n_todo"></h2><div id="todos"></div>
<div class="add"><input type="text" id="todoText" data-tp="todo_ph"><button class="btn" id="todoAdd" data-t="add"></button></div></section>

<section id="priminimai"><h2 data-t="n_rem"></h2><div id="rems"></div>
<div class="add"><input type="text" id="remText" data-tp="rem_ph"><input type="datetime-local" id="remAt"><button class="btn" id="remAdd" data-t="add"></button></div>
<div class="hint"><span data-t="quick"></span> <a href="#" data-in="10" class="qi">10 min</a> · <a href="#" data-in="30" class="qi">30 min</a> · <a href="#" data-in="60" class="qi">1 h</a> · <a href="#" data-in="120" class="qi">2 h</a></div></section>

<section id="kalendorius"><h2 data-t="n_cal"></h2>
<p class="hint" style="margin-top:0" data-t="cal_h"></p>
<div class="add"><input type="text" id="cal" placeholder="https://calendar.google.com/calendar/ical/…/basic.ics"><button class="btn ghost" id="calSave" data-t="save"></button></div></section>

<section id="gimtadieniai"><h2 data-t="n_bday"></h2>
<p class="hint" style="margin-top:0" data-t="bday_h"></p><textarea id="bdays" placeholder="Tomas | 03-15"></textarea>
<div style="margin-top:10px"><button class="btn" id="bdaySave" data-t="save"></button></div></section>

<section id="orai"><h2 data-t="n_weather"></h2>
<div class="row"><div><span data-t="city"></span><small data-t="city_h"></small></div><div><input type="text" id="city"> <button class="btn ghost" id="citySave" data-t="save"></button></div></div>
<div class="row" style="display:block"><div data-t="clocks" style="margin-bottom:8px"></div><div class="chips" id="clocks"></div></div></section>

<section id="nuorodos"><h2 data-t="n_links"></h2>
<p class="hint" style="margin-top:0" data-t="links_h"></p><textarea id="links"></textarea>
<div style="margin-top:10px"><button class="btn" id="linksSave" data-t="save"></button></div></section>

<section id="megstamos"><h2 data-t="n_favs"></h2><div id="favs"></div></section>

<section id="klavisai"><h2 data-t="n_keys"></h2><div id="keys"></div></section>
</div><div class="toast" id="toast"></div>
<script>
const T="{{TOKEN}}";let S={};
const TR={
lt:{sub:"Nustatymai išsaugomi iškart. Šis puslapis veikia tik tavo kompiuteryje.",n_lang:"Kalba",n_look:"Išvaizda",n_feat:"Funkcijos",n_todo:"Užduotys",n_rem:"Priminimai",n_cal:"Kalendorius",n_bday:"Gimtadieniai",n_weather:"Orai ir laikrodžiai",n_links:"Nuorodos",n_favs:"Mėgstamos dainos",n_keys:"Spartieji klavišai",
lang:"Programos kalba",theme:"Tema",dark:"Tamsi",light:"Šviesi",size:"Dydis",small:"Maža",medium:"Vidutinė",large:"Didelė",monitor:"Monitorius",monitor_h:"Kuriame ekrane rodyti salelę",color:"Robotuko spalva",face:"Veidukas",
todo_ph:"Nauja užduotis…",add:"Pridėti",save:"Išsaugoti",rem_ph:"Ką priminti?",quick:"Greitai:",saved:"Išsaugota",fail:"Nepavyko",remAdded:"Priminimas pridėtas",badTime:"Blogas laikas",none:"Nieko nėra.",
cal_h:"Įklijuok kalendoriaus iCal (.ics) nuorodą. Google kalendoriuje: Nustatymai → tavo kalendorius → „Slaptas adresas iCal formatu“. Įvykiai atsiras skirtuke „Planai“, o likus 10 min gausi priminimą.",
bday_h:"Viena eilutė – vienas žmogus: Vardas | MM-DD. Gimtadienio dieną salelė primins.",city:"Miestas",city_h:"Palik tuščią – nustatys automatiškai",clocks:"Pasaulio laikrodžiai (iki 3)",
links_h:"Viena eilutė – vienas mygtukas: Pavadinimas | adresas. Iki 8 mygtukų.",
k_change:"Keisti",k_press:"Spausk naujus klavišus…",k_busy:"užimtas kitos programos",k_reset:"Grąžinti numatytąjį",k_none:"nėra laisvo derinio",k_cmd:"Komandų juosta (skaičiuotuvas, programos, valiuta, priminimai)",k_shot:"Pažymėtos srities nuotrauka",k_color:"Spalvų rinkiklis",k_hide:"Paslėpti / rodyti salelę",k_bright:"Ctrl + ratukas ant salelės – ekrano šviesumas",k_vol:"Ratukas ant salelės – garsumas",
t:{sound:["Garsai","Pranešimų garsai"],notify:["Windows pranešimai","Pranešimai ekrano kampe"],dnd:["Netrukdyti","Slepia pranešimus (priminimai ir Claude leidimai lieka)"],dndFocus:["Netrukdyti per fokusavimą","Automatiškai, kol veikia laikmatis"],
lyricsBar:["Dainos žodžiai juostelėje",""],karaokeFill:["Karaoke spalvinimas","Dainuojama eilutė užsipildo spalva"],coverGlow:["Švytėjimas pagal viršelį","Juostelė šviečia albumo spalva pagal muziką"],translate:["Dainų vertimas","Po eilute rodo vertimą į programos kalbą"],
clipTrans:["Siūlyti išversti nukopijuotą tekstą",""],autoHide:["Slėpti per pilną ekraną","Filmai, žaidimai, prezentacijos"],kbd:["Klaviatūros kalba ir Caps Lock",""],devices:["USB, pelė, klaviatūra, telefonas, ausinės","Praneša prijungus ar atjungus"],
netNotify:["Interneto ryšys",""],micCam:["Mikrofono ir kameros indikatorius","Taškelis, kai programa tave girdi ar mato"],downloads:["Atsisiuntimų pranešimai",""],messages:["Discord, Messenger ir kt. žinutės","Pagal langų pavadinimus, pvz. (3) Discord"],
moods:["Robotuko nuotaikos","Naktį miega, su muzika linguoja"],fact:["Dienos faktas","Kartą per dieną ryte"],autostart:["Paleisti kartu su Windows",""],hooks:["Claude Code ryšys",""]}},
en:{sub:"Settings are saved instantly. This page only works on your computer.",n_lang:"Language",n_look:"Appearance",n_feat:"Features",n_todo:"To-do",n_rem:"Reminders",n_cal:"Calendar",n_bday:"Birthdays",n_weather:"Weather & clocks",n_links:"Links",n_favs:"Favourite songs",n_keys:"Shortcuts",
lang:"App language",theme:"Theme",dark:"Dark",light:"Light",size:"Size",small:"Small",medium:"Medium",large:"Large",monitor:"Monitor",monitor_h:"Which screen shows the island",color:"Robot colour",face:"Face",
todo_ph:"New task…",add:"Add",save:"Save",rem_ph:"What to remind?",quick:"Quick:",saved:"Saved",fail:"Failed",remAdded:"Reminder added",badTime:"Invalid time",none:"Nothing here.",
cal_h:"Paste your calendar's iCal (.ics) link. In Google Calendar: Settings → your calendar → \"Secret address in iCal format\". Events appear in the Plans tab and you get a reminder 10 min before.",
bday_h:"One line per person: Name | MM-DD. The island reminds you on the day.",city:"City",city_h:"Leave empty for automatic",clocks:"World clocks (up to 3)",
links_h:"One line per button: Name | address. Up to 8 buttons.",
k_change:"Change",k_press:"Press the new keys…",k_busy:"is taken by another program",k_reset:"Back to default",k_none:"no free shortcut",k_cmd:"Command bar (calculator, apps, currency, reminders)",k_shot:"Screenshot of an area",k_color:"Colour picker",k_hide:"Hide / show the island",k_bright:"Ctrl + wheel on the island – screen brightness",k_vol:"Wheel on the island – volume",
t:{sound:["Sounds","Notification sounds"],notify:["Windows notifications","Notifications in the corner"],dnd:["Do not disturb","Hides notifications (reminders and Claude permissions stay)"],dndFocus:["Do not disturb while focusing","Automatic while the timer runs"],
lyricsBar:["Lyrics in the strip",""],karaokeFill:["Karaoke colouring","The sung line fills with colour"],coverGlow:["Album colour glow","The strip glows in the album colour with the music"],translate:["Lyrics translation","Shows a translation under each line"],
clipTrans:["Offer to translate copied text",""],autoHide:["Hide in full screen","Movies, games, presentations"],kbd:["Keyboard language and Caps Lock",""],devices:["USB, mouse, keyboard, phone, headphones","Tells you when connected or removed"],
netNotify:["Internet connection",""],micCam:["Microphone and camera indicator","A dot when an app can hear or see you"],downloads:["Download notifications",""],messages:["Discord, Messenger and other messages","From window titles, e.g. (3) Discord"],
moods:["Robot moods","Sleeps at night, nods to music"],fact:["Fact of the day","Once a day in the morning"],autostart:["Start with Windows",""],hooks:["Claude Code connection",""]}},
ru:{sub:"Настройки сохраняются сразу. Эта страница работает только на вашем компьютере.",n_lang:"Язык",n_look:"Внешний вид",n_feat:"Функции",n_todo:"Задачи",n_rem:"Напоминания",n_cal:"Календарь",n_bday:"Дни рождения",n_weather:"Погода и часы",n_links:"Ссылки",n_favs:"Любимые песни",n_keys:"Горячие клавиши",
lang:"Язык программы",theme:"Тема",dark:"Тёмная",light:"Светлая",size:"Размер",small:"Маленький",medium:"Средний",large:"Большой",monitor:"Монитор",monitor_h:"На каком экране показывать",color:"Цвет робота",face:"Лицо",
todo_ph:"Новая задача…",add:"Добавить",save:"Сохранить",rem_ph:"О чём напомнить?",quick:"Быстро:",saved:"Сохранено",fail:"Не удалось",remAdded:"Напоминание добавлено",badTime:"Неверное время",none:"Пусто.",
cal_h:"Вставьте ссылку iCal (.ics) календаря. В Google Календаре: Настройки → ваш календарь → «Закрытый адрес в формате iCal». События появятся во вкладке «Планы», напоминание за 10 минут.",
bday_h:"Одна строка – один человек: Имя | ММ-ДД. В день рождения остров напомнит.",city:"Город",city_h:"Оставьте пустым – определится автоматически",clocks:"Мировые часы (до 3)",
links_h:"Одна строка – одна кнопка: Название | адрес. До 8 кнопок.",
k_change:"Изменить",k_press:"Нажмите новые клавиши…",k_busy:"занято другой программой",k_reset:"Вернуть по умолчанию",k_none:"нет свободного сочетания",k_cmd:"Командная строка (калькулятор, программы, валюта, напоминания)",k_shot:"Скриншот области",k_color:"Пипетка цвета",k_hide:"Скрыть / показать остров",k_bright:"Ctrl + колесо на острове – яркость экрана",k_vol:"Колесо на острове – громкость",
t:{sound:["Звуки","Звуки уведомлений"],notify:["Уведомления Windows","Уведомления в углу экрана"],dnd:["Не беспокоить","Скрывает уведомления (напоминания и разрешения Claude остаются)"],dndFocus:["Не беспокоить при фокусе","Автоматически, пока идёт таймер"],
lyricsBar:["Текст песни в полоске",""],karaokeFill:["Караоке-заливка","Текущая строка заполняется цветом"],coverGlow:["Свечение цвета обложки","Полоска светится цветом альбома в такт"],translate:["Перевод песен","Перевод под каждой строкой"],
clipTrans:["Предлагать перевод скопированного текста",""],autoHide:["Скрывать в полноэкранном режиме","Фильмы, игры, презентации"],kbd:["Язык клавиатуры и Caps Lock",""],devices:["USB, мышь, клавиатура, телефон, наушники","Сообщает о подключении и отключении"],
netNotify:["Интернет-соединение",""],micCam:["Индикатор микрофона и камеры","Точка, когда приложение вас слышит или видит"],downloads:["Уведомления о загрузках",""],messages:["Discord, Messenger и др.","По заголовкам окон, например (3) Discord"],
moods:["Настроение робота","Ночью спит, под музыку кивает"],fact:["Факт дня","Раз в день утром"],autostart:["Запускать с Windows",""],hooks:["Связь с Claude Code",""]}}};
const TOG=["sound","notify","dnd","dndFocus","lyricsBar","karaokeFill","coverGlow","translate","clipTrans","autoHide","kbd","devices","netNotify","micCam","downloads","messages","moods","fact","autostart","hooks"];
const L=()=>TR[S.lang]||TR.lt;
const api=(p,o={})=>fetch(p,{...o,headers:{"X-Island-Token":T,"Content-Type":"application/json"}}).then(r=>{if(!r.ok)throw new Error("x");return r.json()});
const toast=t=>{const e=document.getElementById("toast");e.textContent=t||L().saved;e.classList.add("on");clearTimeout(e._t);e._t=setTimeout(()=>e.classList.remove("on"),t?4000:1300)};
function save(){return api("/api/settings",{method:"POST",body:JSON.stringify(S)}).then(s=>{S=s;render();toast(s.notice)}).catch(()=>toast(L().fail))}
function texts(){const d=L();document.documentElement.lang=S.lang||"lt";
 document.querySelectorAll("[data-t]").forEach(e=>{if(d[e.dataset.t]!==undefined)e.textContent=d[e.dataset.t]});
 document.querySelectorAll("[data-tp]").forEach(e=>e.placeholder=d[e.dataset.tp]||"");
 keysList(d)}
// shortcuts: the one in use, a warning if the wanted one is taken, and a recorder
let rec=null;
function keysList(d){const k=document.getElementById("keys");k.innerHTML="";const K=S.keys||{},B=S.keyBusy||{};
 [["cmd",d.k_cmd],["shot",d.k_shot],["color",d.k_color],["hide",d.k_hide],["",d.k_vol],["",d.k_bright]].forEach(([n,label])=>{const r=document.createElement("div");r.className="row";
  r.innerHTML='<div><span></span><small class="busy"></small></div><div class="kk"><kbd></kbd></div>';r.querySelector("span").textContent=label;
  if(!n){r.querySelector("kbd").remove();k.appendChild(r);return}
  r.querySelector("kbd").textContent=rec===n?d.k_press:(K[n]||d.k_none);
  if(B[n])r.querySelector(".busy").textContent=B[n]+" "+d.k_busy;
  const ch=document.createElement("button");ch.className="mini";ch.textContent=d.k_change;ch.onclick=()=>{rec=n;keysList(d)};
  const rs=document.createElement("button");rs.className="mini";rs.textContent="↺";rs.title=d.k_reset;rs.onclick=()=>{S.keys={...(S.keys||{}),[n]:"default"};rec=null;save()};
  r.querySelector(".kk").append(ch,rs);k.appendChild(r)})}
window.addEventListener("keydown",e=>{if(!rec)return;e.preventDefault();
 if(e.key==="Escape"){rec=null;keysList(L());return}
 let key="";const c=e.code||"";
 if(/^Key[A-Z]$/.test(c))key=c.slice(3);else if(/^Digit\d$/.test(c))key=c.slice(5);else if(c==="Space")key="Space";else if(/^F([1-9]|1[0-2])$/.test(c))key=c;else return;
 const m=[];if(e.ctrlKey)m.push("Ctrl");if(e.shiftKey)m.push("Shift");if(e.altKey)m.push("Alt");if(e.metaKey)m.push("Win");
 if(!e.ctrlKey&&!e.altKey&&!e.metaKey){toast(L().k_press);return}
 const n=rec;rec=null;S.keys={...(S.keys||{}),[n]:[...m,key].join("+")};save()});
function render(){texts();
 document.querySelectorAll(".seg[data-k]").forEach(g=>{const k=g.dataset.k;g.querySelectorAll("button").forEach(b=>b.classList.toggle("on",String(S[k])===b.dataset.v))});
 const m=document.getElementById("monitor");m.innerHTML="";(S.monitors||["1"]).forEach((n,i)=>m.add(new Option(n,i)));m.value=S.monitor;
 const c=document.getElementById("colors");c.innerHTML="";const cols=["conic-gradient(#ff9f0a 0 25%,#ffd60a 0 50%,#30d158 0 75%,#0a84ff 0)","#f2f2f7","#ff7eb6","#bf5af2","#66d4cf","#ff453a","#64d2ff"];
 (S.colors||[]).forEach((n,i)=>{const d=document.createElement("div");d.className="swatch"+(i===S.robotColor?" on":"");d.title=n;d.style.background=cols[i]||"#888";d.onclick=()=>{S.robotColor=i;save()};c.appendChild(d)});
 const f=document.getElementById("faces");f.innerHTML="";(S.faces||[]).forEach((n,i)=>{const b=document.createElement("button");b.dataset.v=String(i);b.textContent=n;b.className=String(S.face)===String(i)?"on":"";b.onclick=()=>{S.face=i;save()};f.appendChild(b)});
 const t=document.getElementById("toggles");t.innerHTML="";TOG.forEach(k=>{const [l,h]=L().t[k]||[k,""];const r=document.createElement("div");r.className="row";r.innerHTML='<div><span></span><small></small></div><label class="sw"><input type="checkbox"'+(S[k]?" checked":"")+'><span></span></label>';r.querySelector("div span").textContent=l;r.querySelector("small").textContent=h;r.querySelector("input").onchange=e=>{S[k]=e.target.checked;save()};t.appendChild(r)});
 const ck=document.getElementById("clocks");ck.innerHTML="";(S.cities||[]).forEach((key,i)=>{const on=(S.clocks||[]).includes(key);const x=document.createElement("div");x.className="chip"+(on?" on":"");x.textContent=S.cityNames[i];x.onclick=()=>{let l=(S.clocks||[]).filter(v=>v!==key);if(!on){l.push(key);if(l.length>3)l.shift()}S.clocks=l;save()};ck.appendChild(x)});
 for(const [id,k] of [["city","city"],["links","links"],["cal","calUrl"],["bdays","birthdays"]]){const e=document.getElementById(id);if(document.activeElement!==e)e.value=S[k]||""}
}
document.querySelectorAll(".seg[data-k]").forEach(g=>g.querySelectorAll("button").forEach(b=>b.onclick=()=>{const k=g.dataset.k;S[k]=/^\d+$/.test(b.dataset.v)?+b.dataset.v:b.dataset.v;save().then(()=>{loadLists()})}));
document.getElementById("monitor").onchange=e=>{S.monitor=+e.target.value;save()};
const bind=(btn,id,k)=>document.getElementById(btn).onclick=()=>{S[k]=document.getElementById(id).value.trim();save()};
bind("citySave","city","city");bind("calSave","cal","calUrl");
document.getElementById("linksSave").onclick=()=>{S.links=document.getElementById("links").value;save()};
document.getElementById("bdaySave").onclick=()=>{S.birthdays=document.getElementById("bdays").value;save()};
function pad(n){return String(n).padStart(2,"0")}
function localInput(d){return d.getFullYear()+"-"+pad(d.getMonth()+1)+"-"+pad(d.getDate())+"T"+pad(d.getHours())+":"+pad(d.getMinutes())}
document.getElementById("remAt").value=localInput(new Date(Date.now()+15*60000));
function list(el,items,fmt){const e=document.getElementById(el);e.innerHTML=items.length?"":'<div class="hint">'+L().none+'</div>';items.forEach(it=>e.appendChild(fmt(it)))}
function rems(l){list("rems",l,r=>{const d=new Date(r.at);const x=document.createElement("div");x.className="item";x.innerHTML="<b></b><span></span><button>×</button>";x.querySelector("b").textContent=d.toLocaleString(S.lang||"lt",{month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit"});x.querySelector("span").textContent=r.text;x.querySelector("button").onclick=()=>api("/api/reminders?id="+r.id,{method:"DELETE"}).then(rems);return x})}
function todos(l){list("todos",l,t=>{const x=document.createElement("div");x.className="item"+(t.done?" done":"");x.innerHTML='<input type="checkbox"'+(t.done?" checked":"")+'><span></span><button>×</button>';x.querySelector("span").textContent=t.text;x.querySelector("input").onchange=()=>api("/api/todos?id="+t.id,{method:"PATCH"}).then(todos);x.querySelector("button").onclick=()=>api("/api/todos?id="+t.id,{method:"DELETE"}).then(todos);return x})}
function favs(l){list("favs",l.map((f,i)=>({...f,i})),f=>{const x=document.createElement("div");x.className="item";x.innerHTML="<b>♥</b><span></span><button>×</button>";x.querySelector("span").textContent=f.artist+" – "+f.title;x.querySelector("button").onclick=()=>api("/api/favs?i="+f.i,{method:"DELETE"}).then(favs);return x})}
function addRem(body){api("/api/reminders",{method:"POST",body:JSON.stringify(body)}).then(l=>{rems(l);toast(L().remAdded);document.getElementById("remText").value=""}).catch(()=>toast(L().badTime))}
document.getElementById("remAdd").onclick=()=>addRem({text:document.getElementById("remText").value,at:document.getElementById("remAt").value});
document.querySelectorAll(".qi").forEach(a=>a.onclick=e=>{e.preventDefault();addRem({text:document.getElementById("remText").value,in:+a.dataset.in})});
document.getElementById("todoAdd").onclick=()=>{const v=document.getElementById("todoText").value.trim();if(v)api("/api/todos",{method:"POST",body:JSON.stringify({text:v})}).then(l=>{todos(l);document.getElementById("todoText").value=""})};
document.getElementById("todoText").onkeydown=e=>{if(e.key==="Enter")document.getElementById("todoAdd").click()};
function loadLists(){api("/api/reminders").then(rems);api("/api/todos").then(todos);api("/api/favs").then(favs)}
api("/api/settings").then(s=>{S=s;render();loadLists()});
</script></body></html>`

const karaokeHTML = `<!DOCTYPE html>
<html lang="lt"><head><meta charset="utf-8"><title>Karaoke · Islet</title>
<style>
html,body{margin:0;height:100%;background:#000;color:#fff;font-family:"Segoe UI",system-ui,sans-serif;overflow:hidden}
#bg{position:fixed;inset:-60px;background-size:cover;background-position:center;filter:blur(60px) brightness(.45);transition:background-image .6s}
#grad{position:fixed;inset:0;background:radial-gradient(ellipse at center,rgba(0,0,0,0) 0,rgba(0,0,0,.55) 100%)}
.top{position:fixed;top:24px;left:32px;right:32px;display:flex;align-items:center;gap:16px;z-index:2}
.top img{width:64px;height:64px;border-radius:12px;object-fit:cover;background:#222}
.top .t{font-size:22px;font-weight:700}.top .a{color:#bbb}
.ctl{margin-left:auto;display:flex;gap:8px}.ctl button{background:rgba(255,255,255,.12);color:#fff;border:0;border-radius:99px;padding:8px 14px;font:inherit;cursor:pointer}
.ctl button:hover{background:rgba(255,255,255,.22)}
#lines{position:fixed;left:0;right:0;top:50%;transform:translateY(-50%);text-align:center;padding:0 6vw;z-index:2}
.l{font-size:clamp(20px,2.6vw,34px);color:rgba(255,255,255,.35);margin:14px 0;transition:all .35s;font-weight:600}
.l.cur{font-size:clamp(34px,5vw,68px);color:#fff;font-weight:800}
.tr{font-size:clamp(18px,2.2vw,30px);color:#64d2ff;margin-top:-6px}
#msg{font-size:28px;color:#aaa}
.bar{position:fixed;left:32px;right:32px;bottom:28px;height:4px;background:rgba(255,255,255,.15);border-radius:4px;z-index:2}
.bar i{display:block;height:100%;width:0;background:#fff;border-radius:4px}
.est{position:fixed;bottom:44px;left:32px;color:#999;font-size:13px;z-index:2}
.cd{display:inline-flex;align-items:center;gap:14px}.cd b{font-weight:800;font-variant-numeric:tabular-nums}
.cdb{width:min(260px,40vw);height:5px;margin:14px auto 0;background:rgba(255,255,255,.15);border-radius:5px;overflow:hidden}.cdb i{display:block;height:100%;width:0;background:#fff;border-radius:5px}
</style></head><body><div id="bg"></div><div id="grad"></div>
<div class="top"><img id="cov" alt=""><div><div class="t" id="title">…</div><div class="a" id="artist"></div></div>
<div class="ctl"><button id="m">−0,5 s</button><button id="p">+0,5 s</button><button id="fs">Visas ekranas</button></div></div>
<div id="lines"><div id="msg">Laukiama muzikos…</div></div>
<div class="est" id="est"></div><div class="bar"><i id="pr"></i></div>
<script>
const T="{{TOKEN}}";let D=null,t0=0,key="",hc=null;
const H={"X-Island-Token":T};
function poll(){fetch("/api/now",{headers:H}).then(r=>r.json()).then(d=>{D=d;t0=performance.now();
 if(d.key!==key||d.hasCover!==hc){key=d.key;hc=d.hasCover;lastI=-2;document.getElementById("title").textContent=d.title||"Niekas negroja";document.getElementById("artist").textContent=d.artist||"";
  const u=d.hasCover?"/cover?t="+T+"&k="+encodeURIComponent(d.key):"";document.getElementById("cov").src=u||"data:,";
  document.getElementById("bg").style.backgroundImage=u?"url('"+u+"')":"linear-gradient(135deg,"+d.colors[0]+","+d.colors[1]+")";}
 document.getElementById("est").textContent=d.estimated?"Laikas apytikslis – jei žodžiai vėluoja, naudok ±0,5 s":"";
}).catch(()=>{})}
setInterval(poll,400);poll();
function esc(s){return s.replace(/[&<>]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;"}[c]))}
let lastI=-2,lastG=0;
function frame(){requestAnimationFrame(frame);if(!D)return;
 const pos=D.pos+(D.playing?(performance.now()-t0)/1000:0)+0.2+D.offset;
 document.getElementById("pr").style.width=D.end>0?Math.min(100,pos/D.end*100)+"%":"0";
 const el=document.getElementById("lines");
 if(!D.title){if(lastI!==-3){el.innerHTML='<div id="msg">Šiuo metu niekas negroja</div>';lastI=-3}return}
 if(!D.lines||!D.lines.length){const m=D.state===1?"Ieškoma žodžių…":D.state===5?"Instrumentinė daina":D.plain?"Šiai dainai yra tik žodžiai be laiko žymų":"Žodžių nerasta";if(lastI!==-4){el.innerHTML='<div id="msg">'+m+'</div>';lastI=-4}return}
 let i=-1;for(let k=0;k<D.lines.length;k++){if(D.lines[k].t<=pos)i=k;else break}
 // intro or a long instrumental part: count down to the next sung line
 let gap=null;
 if(i<0||!(D.lines[i].text||"").trim()){let n=i+1;while(n<D.lines.length&&!(D.lines[n].text||"").trim())n++;
  if(n<D.lines.length&&D.lines[n].t-pos>2.5)gap={from:i<0?0:D.lines[i].t,to:D.lines[n].t}}
 const g=gap?1:0;
 if(i!==lastI||g!==lastG){lastI=i;lastG=g;let h="";
  for(let k=i-2;k<=i+2;k++){const L=D.lines[k];let tx=L?(L.text||"♪"):"",cls="l"+(k===i?" cur":"");
   if(k===i&&gap){h+='<div class="'+cls+'"><span class="cd">♪ <b id="cdt"></b></span><div class="cdb"><i id="cdp"></i></div></div>';continue}
   if(i<0&&k===-1&&gap){h+='<div class="l cur"><span class="cd">♪ <b id="cdt"></b></span><div class="cdb"><i id="cdp"></i></div></div>';continue}
   h+='<div class="'+cls+'">'+(tx?esc(tx):"&nbsp;")+'</div>';if(k===i&&L&&L.tr)h+='<div class="tr">'+esc(L.tr)+'</div>'}
  el.innerHTML=h}
 if(gap){const left=Math.max(0,gap.to-pos),s=Math.ceil(left);const t=document.getElementById("cdt"),p=document.getElementById("cdp");
  if(t)t.textContent=Math.floor(s/60)+":"+String(s%60).padStart(2,"0");
  if(p)p.style.width=Math.min(100,(pos-gap.from)/Math.max(0.1,gap.to-gap.from)*100)+"%"}}
requestAnimationFrame(frame);
const off=d=>fetch("/api/offset?d="+d,{headers:H}).then(poll);
document.getElementById("m").onclick=()=>off(-0.5);document.getElementById("p").onclick=()=>off(0.5);
document.getElementById("fs").onclick=()=>document.documentElement.requestFullscreen&&document.documentElement.requestFullscreen();
</script></body></html>`
