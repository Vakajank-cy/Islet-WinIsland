package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Weather from Open-Meteo (free, no account). Location: the city in
// miestas.txt if the user wrote one, otherwise the approximate IP location.
type Weather struct {
	Has     bool
	City    string
	Temp    float64
	Code    int
	IsDay   bool
	Days    []WeatherDay
	Updated time.Time
	Err     string
}

type WeatherDay struct {
	Date     time.Time
	Code     int
	Max, Min float64
}

type WeatherStore struct {
	mu      sync.Mutex
	cur     Weather
	client  *http.Client
	OnReady func()
	cityMod time.Time
}

func NewWeatherStore() *WeatherStore {
	return &WeatherStore{client: &http.Client{Timeout: 12 * time.Second}}
}

func (s *WeatherStore) Current() Weather {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func cityPath() string { return filepath.Join(filepath.Dir(configPath()), "miestas.txt") }

func readCity() (string, time.Time) {
	st, err := os.Stat(cityPath())
	if err != nil {
		return "", time.Time{}
	}
	b, _ := os.ReadFile(cityPath())
	line := strings.TrimSpace(strings.TrimPrefix(string(b), "\uFEFF"))
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return line, st.ModTime()
}

// Start refreshes every 20 minutes, or soon after miestas.txt changes.
func (s *WeatherStore) Start() {
	go func() {
		next := time.Now()
		for {
			if _, mod := readCity(); !mod.Equal(s.cityMod) {
				s.cityMod = mod
				next = time.Now()
			}
			if !time.Now().Before(next) {
				w := s.fetch()
				s.mu.Lock()
				if w.Has || !s.cur.Has {
					s.cur = w
				}
				s.mu.Unlock()
				if s.OnReady != nil {
					s.OnReady()
				}
				next = time.Now().Add(20 * time.Minute)
				if !w.Has {
					next = time.Now().Add(2 * time.Minute)
				}
			}
			time.Sleep(3 * time.Second)
		}
	}()
}

func (s *WeatherStore) getJSON(u string, out any) error {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Islet/1.0 (Windows)")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *WeatherStore) locate() (string, float64, float64, error) {
	if city, _ := readCity(); city != "" {
		var g struct {
			Results []struct {
				Name      string  `json:"name"`
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"results"`
		}
		u := "https://geocoding-api.open-meteo.com/v1/search?count=1&language=lt&name=" + url.QueryEscape(city)
		if err := s.getJSON(u, &g); err == nil && len(g.Results) > 0 {
			r := g.Results[0]
			return r.Name, r.Latitude, r.Longitude, nil
		}
		return "", 0, 0, errors.New("miestas nerastas: " + city)
	}
	var ip struct {
		Success   bool    `json:"success"`
		City      string  `json:"city"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if err := s.getJSON("https://ipwho.is/", &ip); err == nil && ip.Success {
		return ip.City, ip.Latitude, ip.Longitude, nil
	}
	return "Vilnius", 54.6872, 25.2797, nil
}

func (s *WeatherStore) fetch() Weather {
	city, lat, lon, err := s.locate()
	if err != nil {
		return Weather{Err: err.Error()}
	}
	var r struct {
		Current struct {
			Temp  float64 `json:"temperature_2m"`
			Code  int     `json:"weather_code"`
			IsDay int     `json:"is_day"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Code []int     `json:"weather_code"`
			Max  []float64 `json:"temperature_2m_max"`
			Min  []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	u := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f"+
		"&current=temperature_2m,weather_code,is_day&daily=weather_code,temperature_2m_max,temperature_2m_min"+
		"&timezone=auto&forecast_days=6", lat, lon)
	if err := s.getJSON(u, &r); err != nil {
		return Weather{Err: L("nepavyko gauti orų")}
	}
	w := Weather{Has: true, City: city, Temp: r.Current.Temp, Code: r.Current.Code, IsDay: r.Current.IsDay == 1, Updated: time.Now()}
	for i, d := range r.Daily.Time {
		if i >= len(r.Daily.Code) || i >= len(r.Daily.Max) || i >= len(r.Daily.Min) {
			break
		}
		t, _ := time.Parse("2006-01-02", d)
		w.Days = append(w.Days, WeatherDay{t, r.Daily.Code[i], r.Daily.Max[i], r.Daily.Min[i]})
	}
	return w
}

// ---------- WMO weather codes ----------

type wxKind int

const (
	wxClear wxKind = iota
	wxPartly
	wxCloudy
	wxFog
	wxRain
	wxSnow
	wxStorm
)

func weatherKind(code int) wxKind {
	switch {
	case code == 0 || code == 1:
		return wxClear
	case code == 2:
		return wxPartly
	case code == 3:
		return wxCloudy
	case code == 45 || code == 48:
		return wxFog
	case code >= 71 && code <= 77, code == 85, code == 86:
		return wxSnow
	case code >= 95:
		return wxStorm
	case code >= 51:
		return wxRain
	}
	return wxCloudy
}

func weatherText(code int) string { return L(weatherTextLT(code)) }

func weatherTextLT(code int) string {
	switch code {
	case 0:
		return "Giedra"
	case 1:
		return "Beveik giedra"
	case 2:
		return "Mažai debesuota"
	case 3:
		return "Debesuota"
	case 45, 48:
		return "Rūkas"
	case 51, 53, 55:
		return "Dulksna"
	case 56, 57:
		return "Lijundra"
	case 61:
		return "Silpnas lietus"
	case 63:
		return "Lietus"
	case 65:
		return "Smarkus lietus"
	case 66, 67:
		return "Šąlantis lietus"
	case 71:
		return "Silpnas sniegas"
	case 73:
		return "Sninga"
	case 75:
		return "Smarkiai sninga"
	case 77:
		return "Sniego kruopos"
	case 80, 81:
		return "Liūtis"
	case 82:
		return "Smarki liūtis"
	case 85, 86:
		return "Snygis"
	case 95:
		return "Perkūnija"
	case 96, 99:
		return "Perkūnija su kruša"
	}
	return "Orai"
}

func fmtTemp(t float64) string {
	v := int(math.Round(t))
	if v == 0 {
		return "0°"
	}
	return fmt.Sprintf("%d°", v)
}
