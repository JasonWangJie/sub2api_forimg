#Requires -Version 5.1
<#
.SYNOPSIS
  启动后端：若监听端口已被占用则先结束占用进程，再执行 go run ./cmd/server。

.EXAMPLE
  .\run-server.ps1
  .\run-server.ps1 -Port 8080
  $env:PORT=9090; .\run-server.ps1
#>
[CmdletBinding()]
param(
  [int]$Port = 0
)

$ErrorActionPreference = 'Stop'

$BackendRoot = if ($PSScriptRoot) {
  # scripts/run-server.ps1 -> backend
  if ((Split-Path -Leaf $PSScriptRoot) -eq 'scripts') {
    Split-Path -Parent $PSScriptRoot
  } else {
    $PSScriptRoot
  }
} else {
  (Get-Location).Path
}

Set-Location $BackendRoot

if (-not $env:DATA_DIR -or [string]::IsNullOrWhiteSpace($env:DATA_DIR)) {
  $env:DATA_DIR = $BackendRoot
}

function Resolve-GoExecutable {
  $command = Get-Command go -CommandType Application -ErrorAction SilentlyContinue |
    Select-Object -First 1
  if ($command) {
    return $command.Source
  }

  $candidates = @()
  $toolchainRoot = $null
  if ($env:GOROOT) {
    $candidates += (Join-Path $env:GOROOT 'bin\go.exe')
  }
  if ($env:ProgramFiles) {
    $candidates += (Join-Path $env:ProgramFiles 'Go\bin\go.exe')
  }
  if ($env:LOCALAPPDATA) {
    $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\Go\bin\go.exe')

    $goModPath = Join-Path $BackendRoot 'go.mod'
    $requiredVersion = $null
    if (Test-Path $goModPath) {
      $versionLine = Get-Content $goModPath -Encoding UTF8 |
        Where-Object { $_ -match '^go\s+([0-9]+(?:\.[0-9]+)+)\s*$' } |
        Select-Object -First 1
      if ($versionLine -and $versionLine -match '^go\s+([0-9]+(?:\.[0-9]+)+)\s*$') {
        $requiredVersion = $Matches[1]
      }
    }

    $toolchainRoot = Join-Path $env:LOCALAPPDATA 'CodexToolchains'
    if ($requiredVersion) {
      $candidates += (Join-Path $toolchainRoot "go$requiredVersion\go\bin\go.exe")
    }
  }

  foreach ($candidate in @($candidates | Select-Object -Unique)) {
    if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
      return $candidate
    }
  }

  if ($toolchainRoot -and (Test-Path $toolchainRoot)) {
    $managedCandidates = @(
      Get-ChildItem -Path (Join-Path $toolchainRoot 'go*\go\bin\go.exe') -File -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending |
        Select-Object -ExpandProperty FullName
    )
    foreach ($candidate in $managedCandidates) {
      return $candidate
    }
  }

  throw '[run-server] Go was not found. Install the version declared in go.mod and add its bin directory to PATH.'
}

function Get-ServerPort {
  param([int]$Override)

  if ($Override -gt 0) { return $Override }
  if ($env:PORT -match '^\d+$') { return [int]$env:PORT }
  if ($env:SERVER_PORT -match '^\d+$') { return [int]$env:SERVER_PORT }

  $configPath = Join-Path $BackendRoot 'config.yaml'
  if (Test-Path $configPath) {
    $lines = Get-Content -Path $configPath -Encoding UTF8
    $inServer = $false
    foreach ($line in $lines) {
      if ($line -match '^\s*server\s*:') {
        $inServer = $true
        continue
      }
      if ($inServer -and $line -match '^\S') {
        break
      }
      if ($inServer -and $line -match '^\s*port\s*:\s*(\d+)\s*$') {
        return [int]$Matches[1]
      }
    }
  }
  return 8080
}

function Stop-PortListeners {
  param([int]$ListenPort)

  $pids = @()
  try {
    $pids = @(
      Get-NetTCPConnection -LocalPort $ListenPort -State Listen -ErrorAction SilentlyContinue |
        Select-Object -ExpandProperty OwningProcess -Unique |
        Where-Object { $_ -and $_ -gt 0 }
    )
  } catch {
    # Fallback for environments without Get-NetTCPConnection
  }

  if (-not $pids -or $pids.Count -eq 0) {
    $netstat = netstat -ano 2>$null | Select-String -Pattern ":$ListenPort\s+.*LISTENING"
    foreach ($match in $netstat) {
      if ($match.Line -match '\s(\d+)\s*$') {
        $pidValue = [int]$Matches[1]
        if ($pidValue -gt 0) { $pids += $pidValue }
      }
    }
    $pids = @($pids | Select-Object -Unique)
  }

  if (-not $pids -or $pids.Count -eq 0) {
    Write-Host "[run-server] port $ListenPort is free"
    return
  }

  foreach ($procId in $pids) {
    $proc = Get-Process -Id $procId -ErrorAction SilentlyContinue
    $name = if ($proc) { $proc.ProcessName } else { 'unknown' }
    Write-Host "[run-server] port $ListenPort in use by PID $procId ($name), killing..."
    & taskkill /PID $procId /F 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) {
      Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
    }
  }

  $deadline = (Get-Date).AddSeconds(8)
  while ((Get-Date) -lt $deadline) {
    $still = @(
      Get-NetTCPConnection -LocalPort $ListenPort -State Listen -ErrorAction SilentlyContinue
    )
    if (-not $still -or $still.Count -eq 0) { break }
    Start-Sleep -Milliseconds 200
  }

  $left = @(Get-NetTCPConnection -LocalPort $ListenPort -State Listen -ErrorAction SilentlyContinue)
  if ($left -and $left.Count -gt 0) {
    throw "[run-server] failed to free port $ListenPort"
  }
  Write-Host "[run-server] port $ListenPort released"
}

$listenPort = Get-ServerPort -Override $Port
$goExecutable = Resolve-GoExecutable
$goVersion = (& $goExecutable version) -join ' '
Write-Host "[run-server] DATA_DIR=$($env:DATA_DIR)"
Write-Host "[run-server] GO=$goExecutable ($goVersion)"
Write-Host "[run-server] starting go run ./cmd/server on :$listenPort"
Stop-PortListeners -ListenPort $listenPort

& $goExecutable run ./cmd/server
exit $LASTEXITCODE
