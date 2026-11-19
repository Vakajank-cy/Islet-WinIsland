//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Windows publishes "what is playing" (Spotify, browsers, Media Player…)
// through the System Media Transport Controls. A small helper PowerShell
// script (MTA, so Windows can deliver updates) reads it:
//   - timeline every 250 ms, song info every second
//   - commands on stdin: "seek <seconds>", "ocr <id> <path>"
//   - one JSON line per change on stdout
const mediaScript = `$ErrorActionPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
  $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and
  $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
function Await($op, [Type]$t) {
  $task = $asTask.MakeGenericMethod($t).Invoke($null, @($op))
  $null = $task.Wait(4000)
  $task.Result
}
$null = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager, Windows.Media.Control, ContentType = WindowsRuntime]
$null = [Windows.Storage.Streams.IRandomAccessStreamWithContentType, Windows.Storage.Streams, ContentType = WindowsRuntime]
$null = [Windows.Storage.StorageFile, Windows.Storage, ContentType = WindowsRuntime]
$null = [Windows.Storage.Streams.DataReader, Windows.Storage.Streams, ContentType = WindowsRuntime]
$null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType = WindowsRuntime]
$null = [Windows.Graphics.Imaging.SoftwareBitmap, Windows.Foundation, ContentType = WindowsRuntime]
$null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Foundation, ContentType = WindowsRuntime]
$mgrType = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]
$propType = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties]
$mgr = Await ($mgrType::RequestAsync()) $mgrType
$dir = $args[0]

function Emit($o) {
  try { [Console]::Out.WriteLine(($o | ConvertTo-Json -Compress)); [Console]::Out.Flush() } catch { exit }
}

function Save-Cover($p, $file) {
  try {
    if (-not $p.Thumbnail) { return $false }
    $ras = Await ($p.Thumbnail.OpenReadAsync()) ([Windows.Storage.Streams.IRandomAccessStreamWithContentType])
    if (-not $ras -or $ras.Size -lt 100) { return $false }
    $ok = $false
    try {
      $st = [System.IO.WindowsRuntimeStreamExtensions]::AsStreamForRead($ras)
      $fs = [System.IO.File]::Create($file)
      $st.CopyTo($fs); $fs.Close(); $st.Dispose()
      $ok = (Get-Item $file).Length -gt 100
    } catch { $ok = $false }
    if (-not $ok) {
      # some players only allow reading through a DataReader
      $in = $ras.GetInputStreamAt(0)
      $rd = [Windows.Storage.Streams.DataReader]::new($in)
      $n = Await ($rd.LoadAsync([uint32]$ras.Size)) ([uint32])
      if ($n -lt 100) { return $false }
      $bytes = New-Object byte[] $n
      $rd.ReadBytes($bytes)
      [System.IO.File]::WriteAllBytes($file, $bytes)
      $ok = $true
    }
    return $ok
  } catch { return $false }
}

function Do-Ocr($id, $path) {
  try {
    $file = Await ([Windows.Storage.StorageFile]::GetFileFromPathAsync($path)) ([Windows.Storage.StorageFile])
    $stream = Await ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
    $dec = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
    $bmp = Await ($dec.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
    $eng = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
    if (-not $eng) { return [ordered]@{ kind = 'ocr'; id = $id; text = ''; error = 'nera OCR kalbos' } }
    $res = Await ($eng.RecognizeAsync($bmp)) ([Windows.Media.Ocr.OcrResult])
    $lines = @($res.Lines | ForEach-Object { $_.Text })
    $stream.Dispose()
    return [ordered]@{ kind = 'ocr'; id = $id; text = ($lines -join "` + "`" + `n"); lines = $lines.Count }
  } catch { return [ordered]@{ kind = 'ocr'; id = $id; text = ''; error = $_.Exception.Message } }
}

$stdin = New-Object System.IO.StreamReader([Console]::OpenStandardInput(), [System.Text.Encoding]::UTF8)
$read = $stdin.ReadLineAsync()
$i = 0; $last = ''; $key = ''; $title = ''; $artist = ''; $album = ''; $app = ''
$cover = ''; $coverTries = 0; $coverN = 0; $lastUpd = 0; $same = 0

while ($true) {
  # commands from the island
  while ($read.IsCompleted) {
    $cmd = $read.Result
    if ($cmd -eq $null) { exit }
    $read = $stdin.ReadLineAsync()
    $parts = $cmd.Split(' ', 3)
    if ($parts[0] -eq 'seek') {
      $s = $mgr.GetCurrentSession()
      if ($s) { $null = $s.TryChangePlaybackPositionAsync([int64]([double]::Parse($parts[1], [Globalization.CultureInfo]::InvariantCulture) * 10000000)) }
    } elseif ($parts[0] -eq 'bright') {
      try {
        if ($parts[1] -eq 'set') {
          $m = Get-CimInstance -Namespace root/WMI -ClassName WmiMonitorBrightnessMethods -ErrorAction Stop | Select-Object -First 1
          Invoke-CimMethod -InputObject $m -MethodName WmiSetBrightness -Arguments @{ Timeout = [uint32]1; Brightness = [byte]$parts[2] } | Out-Null
        }
        $b = (Get-CimInstance -Namespace root/WMI -ClassName WmiMonitorBrightness -ErrorAction Stop | Select-Object -First 1).CurrentBrightness
        Emit ([ordered]@{ kind = 'bright'; text = [string]$b })
      } catch { Emit ([ordered]@{ kind = 'bright'; text = '-1' }) }
    } elseif ($parts[0] -eq 'ocr') {
      Emit (Do-Ocr $parts[1] $parts[2])
    }
  }

  $o = [ordered]@{ kind = 'media' }
  try {
    $s = $null
    foreach ($x in $mgr.GetSessions()) {
      if ([int]$x.GetPlaybackInfo().PlaybackStatus -eq 4) { $s = $x; break }
    }
    if (-not $s) { $s = $mgr.GetCurrentSession() }
    if ($s) {
      $app = [string]$s.SourceAppUserModelId
      if (($i % 4) -eq 0 -or $title -eq '') {
        $p = Await ($s.TryGetMediaPropertiesAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties])
        if ($p) {
          $title = [string]$p.Title; $artist = [string]$p.Artist; $album = [string]$p.AlbumTitle
          $k = "$app|$artist|$title"
          if ($k -ne $key) { $key = $k; $cover = ''; $coverTries = 0 }
          if ($cover -eq '' -and $coverTries -lt 12 -and $title -ne '') {
            $coverTries++
            $coverN++
            $f = Join-Path $dir ("cover" + ($coverN % 2) + ".img")
            if (Save-Cover $p $f) { $cover = "$f|$coverN" }
          }
        }
      }
      $tl = $s.GetTimelineProperties()
      $pb = $s.GetPlaybackInfo()
      $o.app = $app; $o.title = $title; $o.artist = $artist; $o.album = $album; $o.cover = $cover
      $o.status = [int]$pb.PlaybackStatus
      $o.pos = [double]$tl.Position.TotalSeconds
      $o.end = [double]$tl.EndTime.TotalSeconds
      $o.updated = [int64]$tl.LastUpdatedTime.ToUnixTimeMilliseconds()
      if ($o.status -eq 4 -and $o.updated -eq $lastUpd) { $same++ } else { $same = 0 }
      $lastUpd = $o.updated
      if ($same -ge 24) { $mgr = Await ($mgrType::RequestAsync()) $mgrType; $same = 0 }
    } else { $title = '' }
  } catch { }
  $line = ($o | ConvertTo-Json -Compress)
  if ($line -ne $last -or ($i % 4) -eq 0) {
    $last = $line
    try { [Console]::Out.WriteLine($line); [Console]::Out.Flush() } catch { exit }
  }
  $i++
  Start-Sleep -Milliseconds 250
}
`

type MediaWatcher struct {
	mu       sync.Mutex
	cur      *Media
	stdin    io.WriteCloser
	cover    string
	OnTrack  func()                        // song, play state or cover changed
	OnOCR    func(id, text, errMsg string) // OCR finished
	OnBright func(percent int)             // -1 = not supported
	ocrID    int
	localPos *struct {
		pos float64
		at  time.Time
	}
	clock clockTracker
}

func (w *MediaWatcher) Current() *Media {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil || time.Since(w.cur.Seen) > 5*time.Second {
		return nil
	}
	m := *w.cur
	now := time.Now()
	if !m.TimelineOK() {
		// the app (e.g. YouTube in a browser) gives no song time: count it ourselves
		w.clock.Update(&m, now)
		m.Pos, m.Updated, m.Estimated = w.clock.Pos(now), now.UnixMilli(), true
		return &m
	}
	// right after a click-to-seek, trust our own position until Windows catches up
	if lp := w.localPos; lp != nil && time.Since(lp.at) < 2500*time.Millisecond {
		m.Pos = lp.pos
		m.Updated = lp.at.UnixMilli()
	}
	return &m
}

// Seek jumps the current song to sec seconds.
func (w *MediaWatcher) Seek(sec float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stdin == nil {
		return
	}
	w.localPos = &struct {
		pos float64
		at  time.Time
	}{sec, time.Now()}
	fmt.Fprintf(w.stdin, "seek %.3f\n", sec)
}

// SyncTo tells the island "the song is at sec right now" (for apps without a timeline).
func (w *MediaWatcher) SyncTo(sec float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clock.Set(sec, time.Now())
}

// OCR asks Windows to read the text in an image file.
func (w *MediaWatcher) OCR(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stdin == nil {
		return false
	}
	w.ocrID++
	fmt.Fprintf(w.stdin, "ocr %d %s\n", w.ocrID, path)
	return true
}

// Brightness asks for the screen brightness; set >= 0 changes it first.
func (w *MediaWatcher) Brightness(set int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stdin == nil {
		return false
	}
	if set >= 0 {
		fmt.Fprintf(w.stdin, "bright set %d\n", set)
	} else {
		fmt.Fprintf(w.stdin, "bright get\n")
	}
	return true
}

// CoverPath returns the album-art file for the current song ("" if none).
func (w *MediaWatcher) CoverPath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cover
}

func (w *MediaWatcher) Start() {
	go func() {
		for {
			w.run()
			time.Sleep(5 * time.Second) // restart if the helper stopped
		}
	}()
}

type helperLine struct {
	Kind  string `json:"kind"`
	Cover string `json:"cover"`
	ID    string `json:"id"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

func (w *MediaWatcher) run() {
	dir := filepath.Dir(configPath())
	os.MkdirAll(dir, 0o755)
	script := filepath.Join(dir, "media.ps1")
	// UTF-8 with BOM so Windows PowerShell 5 reads it correctly
	os.WriteFile(script, append([]byte{0xEF, 0xBB, 0xBF}, []byte(mediaScript)...), 0o644)

	cmd := exec.Command("powershell.exe", "-Mta", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if cmd.Start() != nil {
		return
	}
	w.mu.Lock()
	w.stdin = in
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.stdin = nil
		w.mu.Unlock()
		in.Close()
		cmd.Process.Kill()
	}()

	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var h helperLine
		if json.Unmarshal(sc.Bytes(), &h) != nil {
			continue
		}
		if h.Kind == "bright" {
			if w.OnBright != nil {
				n, err := strconv.Atoi(strings.TrimSpace(h.Text))
				if err != nil {
					n = -1
				}
				w.OnBright(n)
			}
			continue
		}
		if h.Kind == "ocr" {
			if w.OnOCR != nil {
				w.OnOCR(h.ID, h.Text, h.Error)
			}
			continue
		}
		var m Media
		json.Unmarshal(sc.Bytes(), &m)
		m.Seen = time.Now()
		w.mu.Lock()
		prev := w.cur
		if m.Title == "" {
			w.cur = nil
		} else {
			w.cur = &m
		}
		coverChanged := h.Cover != w.cover
		w.cover = h.Cover
		changed := coverChanged || (prev == nil) != (w.cur == nil) ||
			(prev != nil && w.cur != nil && (prev.Key() != w.cur.Key() || prev.Status != w.cur.Status ||
				absf(prev.Position(m.Seen)-w.cur.Position(m.Seen)) > 1.5))
		if changed {
			w.localPos = nil // Windows now knows the real position
		}
		w.mu.Unlock()
		if changed && w.OnTrack != nil {
			w.OnTrack()
		}
	}
	cmd.Wait()
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
