param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("Start", "Stop")]
    [string]$Action,

    [ValidateSet("Full", "Keywords")]
    [string]$Mode = "Full"
)

$ErrorActionPreference = "Stop"
$Root = $PSScriptRoot
$DataDir = Join-Path $Root "data"
$LogDir = Join-Path $Root "logs"
$GoExecutable = Join-Path $Root "noblack.exe"
$ModelExecutable = Join-Path $Root "noblack-model.exe"
$GoPidFile = Join-Path $Root "data\noblack.pid"
$ModelPidFile = Join-Path $Root "data\noblack-model.pid"

function Import-NoblackConfig {
    $configFile = Join-Path $Root "config.env"
    if (-not (Test-Path -LiteralPath $configFile -PathType Leaf)) {
        return
    }
    foreach ($rawLine in Get-Content -LiteralPath $configFile -Encoding UTF8) {
        $line = $rawLine.Trim()
        if ($line.Length -eq 0 -or $line.StartsWith("#")) {
            continue
        }
        $separator = $line.IndexOf("=")
        if ($separator -lt 1) {
            continue
        }
        $key = $line.Substring(0, $separator).Trim()
        $value = $line.Substring($separator + 1)
        if ($key -notmatch "^NB_[A-Z0-9_]+$") {
            continue
        }
        $existing = [Environment]::GetEnvironmentVariable($key, "Process")
        if ([string]::IsNullOrEmpty($existing)) {
            [Environment]::SetEnvironmentVariable($key, $value, "Process")
        }
    }
}

function Get-NoblackSetting([string]$Name, [string]$DefaultValue) {
    $value = [Environment]::GetEnvironmentVariable($Name, "Process")
    if ([string]::IsNullOrEmpty($value)) {
        return $DefaultValue
    }
    return $value
}

function Get-VerifiedProcess([string]$PidFile, [string]$ExpectedExecutable) {
    if (-not (Test-Path -LiteralPath $PidFile -PathType Leaf)) {
        return $null
    }
    $pidText = (Get-Content -LiteralPath $PidFile -TotalCount 1).Trim()
    $recordedPid = 0
    if (-not [int]::TryParse($pidText, [ref]$recordedPid)) {
        Remove-Item -LiteralPath $PidFile -Force
        return $null
    }
    $processInfo = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $recordedPid) -ErrorAction SilentlyContinue
    if ($null -eq $processInfo -or [string]::IsNullOrEmpty($processInfo.ExecutablePath)) {
        Remove-Item -LiteralPath $PidFile -Force
        return $null
    }
    $actualPath = [IO.Path]::GetFullPath([string]$processInfo.ExecutablePath)
    $expectedPath = [IO.Path]::GetFullPath($ExpectedExecutable)
    if (-not [string]::Equals($actualPath, $expectedPath, [StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $PidFile -Force
        return $null
    }
    return Get-Process -Id $recordedPid -ErrorAction SilentlyContinue
}

function Assert-NotRunning([string]$PidFile, [string]$Executable, [string]$Label) {
    $process = Get-VerifiedProcess $PidFile $Executable
    if ($null -ne $process) {
        throw "$Label is already running (PID=$($process.Id))"
    }
}

function Stop-RecordedProcess([string]$PidFile, [string]$Executable, [string]$Label) {
    $process = Get-VerifiedProcess $PidFile $Executable
    if ($null -eq $process) {
        return
    }
    Stop-Process -Id $process.Id
    try {
        Wait-Process -Id $process.Id -Timeout 10 -ErrorAction Stop
    }
    catch {
        $verified = Get-VerifiedProcess $PidFile $Executable
        if ($null -ne $verified) {
            Stop-Process -Id $verified.Id -Force
            Wait-Process -Id $verified.Id -Timeout 5 -ErrorAction SilentlyContinue
        }
    }
    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
    Write-Host "[noblack] stopped $Label"
}

function Stop-NoblackServices {
    Stop-RecordedProcess $GoPidFile $GoExecutable "Go service"
    Stop-RecordedProcess $ModelPidFile $ModelExecutable "model service"
}

function Test-PortListening([int]$Port) {
    $connection = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue
    return $null -ne $connection
}

function Get-VerifiedListeningProcess([int]$Port, [string]$ExpectedExecutable) {
    $connection = Get-NetTCPConnection `
        -State Listen `
        -LocalPort $Port `
        -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $connection) {
        return $null
    }
    $processInfo = Get-CimInstance `
        Win32_Process `
        -Filter ("ProcessId = {0}" -f $connection.OwningProcess) `
        -ErrorAction SilentlyContinue
    if ($null -eq $processInfo -or [string]::IsNullOrEmpty($processInfo.ExecutablePath)) {
        return $null
    }
    $actualPath = [IO.Path]::GetFullPath([string]$processInfo.ExecutablePath)
    $expectedPath = [IO.Path]::GetFullPath($ExpectedExecutable)
    if (-not [string]::Equals($actualPath, $expectedPath, [StringComparison]::OrdinalIgnoreCase)) {
        return $null
    }
    return Get-Process -Id $connection.OwningProcess -ErrorAction SilentlyContinue
}

function Wait-ModelReady([int]$Port, [int]$ProcessId) {
    for ($attempt = 0; $attempt -lt 180; $attempt++) {
        if ($null -eq (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
            return $false
        }
        try {
            $health = Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/health" -f $Port) -TimeoutSec 2
            if (
                $health.ok -eq $true -and
                $health.device -eq "cpu" -and
                $health.models -contains "lite" -and
                $health.models -contains "macbert"
            ) {
                return $true
            }
        }
        catch {
        }
        Start-Sleep -Seconds 1
    }
    return $false
}

function Wait-GoReady([int]$Port, [int]$ProcessId) {
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        if ($null -eq (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
            return $false
        }
        try {
            $health = Invoke-RestMethod -Uri ("http://127.0.0.1:{0}/health" -f $Port) -TimeoutSec 2
            if ($health.code -eq 200) {
                return $true
            }
        }
        catch {
        }
        Start-Sleep -Seconds 1
    }
    return $false
}

function Start-NoblackServices {
    Import-NoblackConfig
    New-Item -ItemType Directory -Force -Path $DataDir, $LogDir | Out-Null

    $wordsFile = Join-Path $DataDir "words.json"
    if (-not (Test-Path -LiteralPath $wordsFile -PathType Leaf)) {
        throw "word database is missing: $wordsFile"
    }

    Assert-NotRunning $GoPidFile $GoExecutable "Go service"
    if ($Mode -eq "Full") {
        Assert-NotRunning $ModelPidFile $ModelExecutable "model service"
    }

    $address = Get-NoblackSetting "NB_ADDR" ":8080"
    $modelPort = [int](Get-NoblackSetting "NB_MODEL_PORT" "8091")
    $goPort = [int]$address.Substring($address.LastIndexOf(":") + 1)
    if (Test-PortListening $goPort) {
        throw "Go port is already in use: $goPort"
    }
    if ($Mode -eq "Full" -and (Test-PortListening $modelPort)) {
        throw "Model port is already in use: $modelPort"
    }

    $modelUrl = ""
    try {
        if ($Mode -eq "Full") {
            [Environment]::SetEnvironmentVariable("NB_PACKAGE_ROOT", $Root, "Process")
            [Environment]::SetEnvironmentVariable("NB_MODEL_HOST", "127.0.0.1", "Process")
            [Environment]::SetEnvironmentVariable("NB_MODEL_PORT", [string]$modelPort, "Process")
            [Environment]::SetEnvironmentVariable("NB_MODEL_THREADS", (Get-NoblackSetting "NB_MODEL_THREADS" "2"), "Process")
            [Environment]::SetEnvironmentVariable("NB_MODEL_COMBINE_POLICY", (Get-NoblackSetting "NB_MODEL_COMBINE_POLICY" "max"), "Process")
            [Environment]::SetEnvironmentVariable("NB_MODEL_PASS_THRESHOLD", (Get-NoblackSetting "NB_MODEL_PASS_THRESHOLD" "0.15"), "Process")
            [Environment]::SetEnvironmentVariable("NB_MODEL_BLOCK_THRESHOLD", (Get-NoblackSetting "NB_MODEL_BLOCK_THRESHOLD" "0.5"), "Process")
            [Environment]::SetEnvironmentVariable("NB_LITE_MODEL", (Join-Path $Root "models\lite-production-v1"), "Process")
            [Environment]::SetEnvironmentVariable("NB_MACBERT_MODEL", (Join-Path $Root "models\macbert-production-v1"), "Process")

            $modelProcess = Start-Process `
                -FilePath $ModelExecutable `
                -WorkingDirectory $Root `
                -RedirectStandardOutput (Join-Path $LogDir "noblack-model.log") `
                -RedirectStandardError (Join-Path $LogDir "noblack-model.error.log") `
                -PassThru
            Set-Content -LiteralPath $ModelPidFile -Value $modelProcess.Id -Encoding ASCII
            if (-not (Wait-ModelReady $modelPort $modelProcess.Id)) {
                throw "Model service failed to become ready; see $LogDir"
            }
            $modelListener = Get-VerifiedListeningProcess $modelPort $ModelExecutable
            if ($null -eq $modelListener) {
                throw "Model listener process could not be verified; see $LogDir"
            }
            Set-Content -LiteralPath $ModelPidFile -Value $modelListener.Id -Encoding ASCII
            $modelUrl = "http://127.0.0.1:$modelPort"
        }

        $watch = Get-NoblackSetting "NB_WATCH" "true"
        $goArguments = @(
            "-addr", ('"{0}"' -f $address),
            "-words", ('"{0}"' -f $wordsFile),
            ("-watch={0}" -f $watch),
            "-model-service-url", ('"{0}"' -f $modelUrl)
        )
        $statsFile = Get-NoblackSetting "NB_STATS" ""
        if (-not [string]::IsNullOrEmpty($statsFile)) {
            $goArguments += @("-stats-file", ('"{0}"' -f $statsFile))
        }
        $token = Get-NoblackSetting "NB_TOKEN" ""
        if (-not [string]::IsNullOrEmpty($token)) {
            $goArguments += @("-token", ('"{0}"' -f $token))
        }
        if ((Get-NoblackSetting "NB_CI" "false") -eq "true") {
            $goArguments += "-ci"
        }

        $goProcess = Start-Process `
            -FilePath $GoExecutable `
            -ArgumentList $goArguments `
            -WorkingDirectory $Root `
            -RedirectStandardOutput (Join-Path $LogDir "noblack.log") `
            -RedirectStandardError (Join-Path $LogDir "noblack.error.log") `
            -PassThru
        Set-Content -LiteralPath $GoPidFile -Value $goProcess.Id -Encoding ASCII
        if (-not (Wait-GoReady $goPort $goProcess.Id)) {
            throw "Go service failed to become ready; see $LogDir"
        }
    }
    catch {
        Stop-NoblackServices
        throw
    }

    if ($Mode -eq "Full") {
        Write-Host "[noblack] keyword + dual-model service ready: http://127.0.0.1:$goPort"
    }
    else {
        Write-Host "[noblack] keyword service ready: http://127.0.0.1:$goPort"
    }
}

if ($Action -eq "Stop") {
    Stop-NoblackServices
}
else {
    Start-NoblackServices
}
