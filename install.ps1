# gravity-cli installer (Windows / PowerShell). The app's Download button hands
# users:
#
#   irm https://raw.githubusercontent.com/Grupo-Impulso-Digital/gravity-cli/main/install.ps1 | iex
#
# It downloads the right prebuilt binary for the host architecture from the
# latest GitHub Release, verifies its checksum, drops it on PATH, and works on
# Windows PowerShell 5.1+ and PowerShell 7+.
#
# Windows users can also `scoop install gravity` (see the README); this script is
# the scoop-free one-liner path.
#
# Overrides (env vars):
#   GRAVITY_VERSION      tag to install (e.g. v0.1.0); default: latest
#   GRAVITY_INSTALL_DIR  install location; default: %LOCALAPPDATA%\Programs\gravity

$ErrorActionPreference = 'Stop'

$Repo   = 'Grupo-Impulso-Digital/gravity-cli'
$Binary = 'gravity'

function Info($msg) { Write-Host "==> $msg" -ForegroundColor Blue }
function Warn($msg) { Write-Host "warning: $msg" -ForegroundColor Yellow }
function Die($msg)  { Write-Host "error: $msg" -ForegroundColor Red; exit 1 }

# TLS 1.2 for Windows PowerShell 5.1, whose default can predate it and break the
# GitHub API/download calls.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# --- detect architecture (match GoReleaser's GOARCH names) -------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  'x86'   { Die 'unsupported architecture x86 (32-bit); gravity ships amd64/arm64 only' }
  default { Die "unsupported architecture '$env:PROCESSOR_ARCHITECTURE'" }
}

# --- resolve the release tag -------------------------------------------------
$version = if ($env:GRAVITY_VERSION) { $env:GRAVITY_VERSION } else { 'latest' }
if ($version -eq 'latest') {
  # /releases/latest excludes pre-releases, so this always resolves to stable.
  try {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" `
      -Headers @{ 'User-Agent' = 'gravity-installer' }
    $tag = $rel.tag_name
  } catch {
    Die "could not resolve the latest release (is the repo public and has it shipped a release yet?)"
  }
} else {
  $tag = $version
}
if (-not $tag) { Die 'could not determine the release tag' }
$ver = $tag.TrimStart('v')

$archive = "${Binary}_${ver}_windows_${arch}.zip"
$base    = "https://github.com/$Repo/releases/download/$tag"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("gravity-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  # --- download --------------------------------------------------------------
  Info "Downloading $Binary $tag (windows/$arch)"
  $archivePath = Join-Path $tmp $archive
  try {
    Invoke-WebRequest -Uri "$base/$archive" -OutFile $archivePath -UseBasicParsing `
      -Headers @{ 'User-Agent' = 'gravity-installer' }
  } catch {
    Die "download failed: $base/$archive"
  }

  # --- verify checksum (best-effort; hard-fail only on an actual mismatch) ---
  $sumsPath = Join-Path $tmp 'checksums.txt'
  try {
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sumsPath -UseBasicParsing `
      -Headers @{ 'User-Agent' = 'gravity-installer' }
  } catch { $sumsPath = $null }

  if ($sumsPath) {
    $line = Select-String -Path $sumsPath -Pattern ([regex]::Escape($archive)) | Select-Object -First 1
    if (-not $line) { Die "no checksum listed for $archive" }
    $expected = ($line.Line -split '\s+')[0].ToLower()
    $actual   = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
    if ($expected -ne $actual) { Die "checksum mismatch for $archive (expected $expected, got $actual)" }
    Info 'Checksum verified'
  } else {
    Warn 'checksums.txt unavailable; skipping checksum verification'
  }

  # --- extract ---------------------------------------------------------------
  Expand-Archive -Path $archivePath -DestinationPath $tmp -Force
  $exe = Join-Path $tmp "$Binary.exe"
  if (-not (Test-Path $exe)) { Die "archive did not contain $Binary.exe" }

  # --- choose install dir ----------------------------------------------------
  $dir = if ($env:GRAVITY_INSTALL_DIR) {
    $env:GRAVITY_INSTALL_DIR
  } else {
    Join-Path $env:LOCALAPPDATA "Programs\$Binary"
  }
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Copy-Item -Path $exe -Destination (Join-Path $dir "$Binary.exe") -Force
  Info "Installed to $dir\$Binary.exe"

  # --- ensure the install dir is on the user PATH ----------------------------
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $onPath = ($userPath -split ';') -contains $dir
  if (-not $onPath) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
    $env:Path = "$env:Path;$dir"
    Warn "Added $dir to your user PATH. Open a new terminal for it to take effect."
  }

  & (Join-Path $dir "$Binary.exe") version
} finally {
  Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
