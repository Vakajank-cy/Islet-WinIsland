package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Sound       bool              `json:"sound"`
	Notify      bool              `json:"notify"`
	RobotColor  int               `json:"robotColor"`
	Face        int               `json:"face"`
	TimerPreset int               `json:"timerPreset"`
	AutoHide    bool              `json:"autoHide"`
	LyricsBar   bool              `json:"lyricsBar"`
	Theme       string            `json:"theme"`
	Size        int               `json:"size"`
	Monitor     int               `json:"monitor"`
	Translate   bool              `json:"translate"`
	Kbd         bool              `json:"kbd"`
	Keys        map[string]string `json:"keys,omitempty"` // own shortcuts
	KeyNote     bool              `json:"keyNote,omitempty"`
	Devices     bool              `json:"devices"`
	NetNotify   bool              `json:"netNotify"`

	Lang         string     `json:"lang"`
	CoverGlow    bool       `json:"coverGlow"`
	KaraokeFill  bool       `json:"karaokeFill"`
	ClipTrans    bool       `json:"clipTrans"`
	MicCam       bool       `json:"micCam"`
	Downloads    bool       `json:"downloads"`
	Messages     bool       `json:"messages"`
	Moods        bool       `json:"moods"`
	Fact         bool       `json:"fact"`
	DND          bool       `json:"dnd"`
	DNDFocus     bool       `json:"dndFocus"`
	CalURL       string     `json:"calUrl"`
	Birthdays    []Birthday `json:"birthdays"`
	Clocks       []string   `json:"clocks"`
	LastFactDay  string     `json:"lastFactDay"`
	LastBdayDay  string     `json:"lastBdayDay"`
	GameBest     int        `json:"gameBest"`
	FirstRunDone bool       `json:"firstRunDone"`
}

// appName is shown in the tray, menus and pages.
const appName = "Islet"

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, appName, "config.json")
}

// migrateDataDir moves settings from the folder older versions used
// (%APPDATA%\DynamicIsland) to %APPDATA%\Islet, once.
func migrateDataDir() {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	old, cur := filepath.Join(dir, "DynamicIsland"), filepath.Join(dir, appName)
	if _, err := os.Stat(cur); err == nil {
		return
	}
	if st, err := os.Stat(old); err == nil && st.IsDir() {
		os.Rename(old, cur)
	}
}

func LoadConfig() Config {
	c := Config{Sound: true, Notify: true, AutoHide: true, LyricsBar: true, Theme: "dark", Size: 1,
		Translate: true, Kbd: true, Devices: true, NetNotify: true,
		Lang: "lt", CoverGlow: true, KaraokeFill: true, ClipTrans: true, MicCam: true, Downloads: true, Messages: true,
		Moods: true, Fact: true}
	if b, err := os.ReadFile(configPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.RobotColor < 0 || c.RobotColor >= len(robotColors) {
		c.RobotColor = 0
	}
	if c.Face < 0 || c.Face >= len(robotFaces) {
		c.Face = 0
	}
	if c.Size < 0 || c.Size > 2 {
		c.Size = 1
	}
	if c.Theme != "light" {
		c.Theme = "dark"
	}
	return c
}

func (c Config) Save() {
	p := configPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := json.MarshalIndent(c, "", "  ")
	os.WriteFile(p, b, 0o644)
}
