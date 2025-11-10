Param(
    [ValidateSet("chord", "koorde")]
    [string]$Overlay = "chord",

    [int]$Count = 3,

    [int]$BasePort = 8080,

    [switch]$RedirectMode
)

if ($Count -lt 1) {
    throw "Count must be at least 1."
}

$ErrorActionPreference = "Stop"

Write-Host "Building node binary..." -ForegroundColor Cyan
$projectRoot = Split-Path -Parent $PSScriptRoot
$binaryPath = Join-Path $projectRoot "bin\slbwc-node.exe"

New-Item -ItemType Directory -Force -Path (Split-Path $binaryPath) | Out-Null

Push-Location $projectRoot
try {
    go build -o $binaryPath ./cmd/node
} finally {
    Pop-Location
}

Write-Host "Starting $Count node(s) with overlay '$Overlay'..." -ForegroundColor Cyan

$processes = @()
$seedAddress = ""

for ($i = 0; $i -lt $Count; $i++) {
    $port = $BasePort + $i
    $address = "127.0.0.1:$port"
    $args = @("--overlay", $Overlay, "--address", $address)

    if ($seedAddress -ne "") {
        $args += @("--seed", $seedAddress)
    }

    if ($RedirectMode.IsPresent) {
        $args += @("--mode", "redirect")
    }

    $proc = Start-Process -FilePath $binaryPath `
        -ArgumentList $args `
        -WorkingDirectory $projectRoot `
        -PassThru `
        -NoNewWindow
    $processes += $proc

    Write-Host ("Started node {0} (PID {1}) -> http://{2}" -f ($i + 1), $proc.Id, $address)

    if ($seedAddress -eq "") {
        $seedAddress = $address
    }
}

Write-Host ""
Write-Host "Nodes are running in the background. Press Enter to stop them." -ForegroundColor Yellow
[void][System.Console]::ReadLine()

Write-Host "Stopping nodes..." -ForegroundColor Cyan
foreach ($proc in $processes) {
    if (!$proc.HasExited) {
        $proc.Kill()
        $proc.WaitForExit()
    }
}

Write-Host "All nodes stopped." -ForegroundColor Green

