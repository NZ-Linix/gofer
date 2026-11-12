# gofer installer for Windows:
#   irm https://raw.githubusercontent.com/NZ-Linix/gofer/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'NZ-Linix/gofer'
$base = "https://github.com/$repo/releases/latest/download"

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'ARM64' { 'arm64' }
  'AMD64' { 'amd64' }
  default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$asset = "gofer_windows_$arch.exe"

$dir = Join-Path $env:LOCALAPPDATA 'Programs\gofer'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$tmp = Join-Path $env:TEMP "gofer-$([guid]::NewGuid()).exe"

Write-Host "Downloading $asset..."
Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile $tmp
$sums = (Invoke-WebRequest -UseBasicParsing "$base/checksums.txt").Content
if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }

$line = ($sums -split "`n") | Where-Object { $_ -match " $([regex]::Escape($asset))\s*$" } | Select-Object -First 1
$expected = if ($line) { ($line -split ' ')[0].Trim() } else { '' }
$actual = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLower()
if (-not $expected -or $expected -ne $actual) {
  Remove-Item $tmp -Force
  throw 'Checksum verification failed.'
}

Move-Item -Force $tmp (Join-Path $dir 'gofer.exe')

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $dir)) {
  [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$dir").TrimStart(';'), 'User')
  $env:Path += ";$dir"
  Write-Host "Added $dir to your PATH (open a new terminal to use it everywhere)."
}

Write-Host "Installed gofer to $dir\gofer.exe"
Write-Host '  Next: gofer login <username>'
