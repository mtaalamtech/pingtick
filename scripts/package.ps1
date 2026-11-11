# Builds PingTick.exe and the release zip in the dist folder.
#   powershell -ExecutionPolicy Bypass -File scripts\package.ps1 -Version 1.1.0
param(
    [Parameter(Mandatory = $true)][string]$Version
)
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root 'dist'
$stage = Join-Path $dist 'PingTick'
$zip = Join-Path $dist "PingTick_v${Version}_windows_x64.zip"

if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
New-Item -ItemType Directory -Force $stage | Out-Null

Push-Location $root
try {
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }

    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    go build -trimpath -ldflags "-s -w -H=windowsgui" -o (Join-Path $stage 'PingTick.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

Copy-Item (Join-Path $root 'README.md'), (Join-Path $root 'LICENSE'), (Join-Path $root 'THIRD-PARTY-NOTICES.txt') $stage

Compress-Archive -Path $stage -DestinationPath $zip
$hash = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
"$hash  $(Split-Path -Leaf $zip)" | Out-File -Encoding ascii "$zip.sha256"

Write-Host "Built $zip"
Write-Host "SHA256 $hash"
