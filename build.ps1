# Baut Server und Agent für Windows und Linux (macOS-Agent: siehe README, braucht einen Mac).
# Aufruf:  .\build.ps1 [-Server https://fernwartung.example.com]
param([string]$Server = "")
$ErrorActionPreference = "Stop"
$dist = Join-Path $PSScriptRoot "dist"
New-Item -ItemType Directory -Force $dist | Out-Null
$ld = "-s -w"
if ($Server) { $ld += " -X main.defaultServer=$Server" }
$env:CGO_ENABLED = "0"

function Build($os, $arch, $pkg, $out, $flags) {
    $env:GOOS = $os; $env:GOARCH = $arch
    go build -trimpath -ldflags $flags -o (Join-Path $dist $out) $pkg
    if ($LASTEXITCODE) { throw "Build fehlgeschlagen: $out" }
    Write-Host "OK  $out"
}

Build windows amd64 ./cmd/server fernwartung-server-windows-amd64.exe "-s -w"
Build windows amd64 ./cmd/agent  fernwartung-agent-windows-amd64.exe  "$ld -H=windowsgui"
Build linux   amd64 ./cmd/server fernwartung-server-linux-amd64 "-s -w"
Build linux   arm64 ./cmd/server fernwartung-server-linux-arm64 "-s -w"
Build linux   amd64 ./cmd/agent  fernwartung-agent-linux-amd64  $ld
Build linux   arm64 ./cmd/agent  fernwartung-agent-linux-arm64  $ld
