$ErrorActionPreference = 'Stop'

$Repo   = 'Grupo-Impulso-Digital/gravity-cli'
$Binary = 'gravity'
$Api    = if ($env:GRAVITY_RELEASES_API) { $env:GRAVITY_RELEASES_API.TrimEnd('/') } else { 'https://api.github.com' }

function Info($msg) { Write-Host "==> $msg" -ForegroundColor Blue }
function Warn($msg) { Write-Host "warning: $msg" -ForegroundColor Yellow }
function Die($msg)  { Write-Host "error: $msg" -ForegroundColor Red; exit 1 }

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Headers = @{ 'User-Agent' = 'gravity-installer'; 'Accept' = 'application/vnd.github+json' }
if ($env:GH_TOKEN) { $Headers['Authorization'] = "Bearer $env:GH_TOKEN" }

function Get-NewestOfMajor([string]$major) {
  $best = $null
  foreach ($page in 1..5) {
    try {
      $releases = Invoke-RestMethod -Uri "$Api/repos/$Repo/releases?per_page=100&page=$page" -Headers $Headers
    } catch {
      Die "could not list the releases of $Repo"
    }
    if (-not $releases -or $releases.Count -eq 0) { break }
    foreach ($r in $releases) {
      if ($r.tag_name -match '^v(\d+)\.(\d+)\.(\d+)$' -and $Matches[1] -eq $major) {
        $v = [version]::new([int]$Matches[1], [int]$Matches[2], [int]$Matches[3])
        if (-not $best -or $v -gt $best) { $best = $v }
      }
    }
  }
  if ($best) { return "v$($best.Major).$($best.Minor).$($best.Build)" }
  return $null
}

$version = if ($env:GRAVITY_VERSION) { $env:GRAVITY_VERSION } else { '1' }
switch -Regex ($version) {
  '^latest$' {
    try {
      $tag = (Invoke-RestMethod -Uri "$Api/repos/$Repo/releases/latest" -Headers $Headers).tag_name
    } catch {
      Die "could not resolve the latest release of $Repo"
    }
    break
  }
  '^v?(\d{1,2})$' {
    $tag = Get-NewestOfMajor $Matches[1]
    if (-not $tag) { Die "no v$($Matches[1]).x.y release of $Repo exists yet" }
    break
  }
  '^v\d+\.\d+\.\d+' { $tag = $version; break }
  '^\d+\.\d+\.\d+' { $tag = "v$version"; break }
  default { Die "GRAVITY_VERSION must be a major version (1), a release (v1.2.3) or latest; got '$version'" }
}
if (-not $tag) { Die 'could not determine the release tag' }
if ($env:GRAVITY_RESOLVE_ONLY) { Write-Output $tag; exit 0 }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  'x86'   { Die 'unsupported architecture x86 (32-bit); gravity ships amd64/arm64 only' }
  default { Die "unsupported architecture '$env:PROCESSOR_ARCHITECTURE'" }
}

$ver     = $tag.TrimStart('v')
$archive = "${Binary}_${ver}_windows_${arch}.zip"
$base    = "https://github.com/$Repo/releases/download/$tag"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("gravity-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Info "Downloading $Binary $tag (windows/$arch)"
  $archivePath = Join-Path $tmp $archive
  try {
    Invoke-WebRequest -Uri "$base/$archive" -OutFile $archivePath -UseBasicParsing -Headers @{ 'User-Agent' = 'gravity-installer' }
  } catch {
    Die "download failed: $base/$archive"
  }

  $sumsPath = Join-Path $tmp 'checksums.txt'
  try {
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sumsPath -UseBasicParsing -Headers @{ 'User-Agent' = 'gravity-installer' }
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

  Expand-Archive -Path $archivePath -DestinationPath $tmp -Force
  $exe = Join-Path $tmp "$Binary.exe"
  if (-not (Test-Path $exe)) { Die "archive did not contain $Binary.exe" }

  $dir = if ($env:GRAVITY_INSTALL_DIR) { $env:GRAVITY_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$Binary" }
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Copy-Item -Path $exe -Destination (Join-Path $dir "$Binary.exe") -Force
  Info "Installed $tag to $dir\$Binary.exe"

  if (-not $env:GRAVITY_INSTALL_DIR) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($userPath -split ';') -contains $dir)) {
      [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
      $env:Path = "$env:Path;$dir"
      Warn "Added $dir to your user PATH. Open a new terminal for it to take effect."
    }
  }

  & (Join-Path $dir "$Binary.exe") version
} finally {
  Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
