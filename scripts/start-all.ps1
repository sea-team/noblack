param(
    [int]$Port = 8080,
    [int]$ModelPort = 8091,
    [int]$ModelThreads = 2,
    [switch]$EnableModels
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $runnerArgs = @("--port", $Port, "--model-port", $ModelPort, "--threads", $ModelThreads)
    if ($EnableModels) { $runnerArgs += "--enable-models" }
    python .\scripts\start_all.py @runnerArgs
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally { Pop-Location }
