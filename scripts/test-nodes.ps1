Param(
    [string[]]$Nodes = @("127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"),

    [string]$TestUrl = "https://example.com/",

    [int]$TimeoutSeconds = 30
)

if ($Nodes.Count -lt 1) {
    throw "Provide at least one node address."
}

$ErrorActionPreference = "Stop"

function Wait-ForHealth {
    param(
        [string]$Address,
        [int]$TimeoutSeconds
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $resp = Invoke-WebRequest -Uri ("http://{0}/healthz" -f $Address) -Method GET -UseBasicParsing -TimeoutSec 5
            if ($resp.StatusCode -eq 204) {
                return $true
            }
        } catch {
        }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

Write-Host "Waiting for nodes to become healthy..." -ForegroundColor Cyan
foreach ($node in $Nodes) {
    if (-not (Wait-ForHealth -Address $node -TimeoutSeconds $TimeoutSeconds)) {
        throw "Node $node did not report healthy within timeout."
    }
    Write-Host ("  {0} healthy" -f $node)
}

$target = ("http://{0}/cache?url={1}" -f $Nodes[0], [uri]::EscapeDataString($TestUrl))
Write-Host ""
Write-Host ("Requesting resource via {0}" -f $Nodes[0]) -ForegroundColor Cyan
$response = Invoke-WebRequest -Uri $target -Method GET -UseBasicParsing -TimeoutSec 30
Write-Host ("  Status: {0}" -f $response.StatusCode)
Write-Host ("  X-Cache-Status: {0}" -f $response.Headers["X-Cache-Status"])

if ($Nodes.Count -gt 1) {
    $headTarget = ("http://{0}/cache?url={1}" -f $Nodes[1], [uri]::EscapeDataString($TestUrl))
    Write-Host ""
    Write-Host ("Verifying cached HEAD via {0}" -f $Nodes[1]) -ForegroundColor Cyan
    $headResponse = Invoke-WebRequest -Uri $headTarget -Method HEAD -UseBasicParsing -TimeoutSec 30
    Write-Host ("  Status: {0}" -f $headResponse.StatusCode)
    Write-Host ("  X-Cache-Status: {0}" -f $headResponse.Headers["X-Cache-Status"])
    Write-Host ("  Cache TTL: {0}" -f $headResponse.Headers["X-Cache-TTL"])
}

Write-Host ""
Write-Host "Test complete." -ForegroundColor Green

