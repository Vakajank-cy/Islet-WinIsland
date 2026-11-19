# Builds Islet.exe (Windows, 64-bit). Needs Go 1.24+.
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-H windowsgui -s -w" -o Islet.exe .
Write-Host "Built Islet.exe"
