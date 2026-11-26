# Syncme installer for Windows 10/11. In PowerShell (no admin needed):
#   irm https://raw.githubusercontent.com/CosmicKittu/Syncme/main/scripts/install-windows.ps1 | iex
# Installs syncme.exe to %LOCALAPPDATA%\Programs\Syncme, adds it to PATH,
# starts it now and at every login, and adds Explorer "Send to > Phone (Syncme)".
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'CosmicKittu/Syncme'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$dir  = Join-Path $env:LOCALAPPDATA 'Programs\Syncme'
$exe  = Join-Path $dir 'syncme.exe'
New-Item -ItemType Directory -Force -Path $dir | Out-Null

# Stop a running copy so the file can be replaced.
Get-Process syncme -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 500

if ($env:SYNCME_BINARY) {
    Copy-Item $env:SYNCME_BINARY $exe -Force
} else {
    $url = "https://github.com/$repo/releases/latest/download/syncme-windows-$arch.exe"
    Write-Host "Downloading $url"
    Invoke-WebRequest -Uri $url -OutFile $exe -UseBasicParsing
}
Write-Host "Installed $exe ($(& $exe version))"

# Add to the user's PATH.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $dir)) {
    $newPath = (@($userPath, $dir) | Where-Object { $_ }) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path += ";$dir"
    Write-Host "Added $dir to your PATH (open a new terminal to use 'syncme' everywhere)."
}

& $exe install

# Allow syncme through Windows Firewall on private networks (asks for admin once).
if (-not $env:SYNCME_NO_FIREWALL) {
    try {
        $rule = "advfirewall firewall delete rule name=Syncme & netsh advfirewall firewall add rule name=Syncme dir=in action=allow program=`"$exe`" profile=private enable=yes"
        Start-Process -FilePath cmd.exe -ArgumentList "/c netsh $rule" -Verb RunAs -WindowStyle Hidden -Wait
        Write-Host "Firewall rule added (private networks)."
    } catch {
        Write-Host "Skipped the firewall rule. If Windows asks later, click Allow for Private networks."
    }
}

Write-Host ""
Write-Host "Done. Now run:  syncme pair"
