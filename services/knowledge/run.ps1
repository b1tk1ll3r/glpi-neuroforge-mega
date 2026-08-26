param(
    [switch]$NoEnv
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$ProjectRoot = $PSScriptRoot
Set-Location $ProjectRoot

function Import-DotEnv {
    param([Parameter(Mandatory = $true)][string]$Path)

    Get-Content -LiteralPath $Path | ForEach-Object {
        $line = $_.Trim()
        if (-not $line -or $line.StartsWith("#")) { return }

        $parts = $line.Split("=", 2)
        if ($parts.Count -ne 2) { return }

        $name = $parts[0].Trim()
        $value = $parts[1].Trim()
        if (-not $name) { return }

        if (($value.StartsWith('"') -and $value.EndsWith('"')) -or
            ($value.StartsWith("'") -and $value.EndsWith("'"))) {
            $value = $value.Substring(1, $value.Length - 2)
        }

        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

if (-not $NoEnv) {
    $envFile = Join-Path $ProjectRoot ".env"
    if (Test-Path -LiteralPath $envFile) {
        Import-DotEnv -Path $envFile
    }
    else {
        Write-Warning ".env nicht gefunden. Es werden vorhandene Umgebungsvariablen und Programm-Defaults verwendet."
    }
}

# Migration helper for .env files from older ZIP versions. These values were Docker-only
# and are invalid when the agent is started natively with `go run` on Windows.
if ($env:DATA_DIR -eq "/app/data") {
    $env:DATA_DIR = Join-Path $ProjectRoot "data"
    Write-Warning "DATA_DIR=/app/data ist ein Docker-Pfad; verwende lokal '$env:DATA_DIR'."
}
if ($env:KNOWLEDGE_DIR -eq "/app/knowledge") {
    $env:KNOWLEDGE_DIR = Join-Path $ProjectRoot "knowledge"
    Write-Warning "KNOWLEDGE_DIR=/app/knowledge ist ein Docker-Pfad; verwende lokal '$env:KNOWLEDGE_DIR'."
}
if ($env:OLLAMA_URL -eq "http://ollama:11434") {
    $env:OLLAMA_URL = "http://localhost:11434"
    Write-Warning "OLLAMA_URL=http://ollama:11434 ist der Docker-Hostname; verwende lokal '$env:OLLAMA_URL'."
}

if (-not $env:DATA_DIR) {
    $env:DATA_DIR = Join-Path $ProjectRoot "data"
}
if (-not $env:KNOWLEDGE_DIR) {
    $env:KNOWLEDGE_DIR = Join-Path $ProjectRoot "knowledge"
}
if (-not $env:OLLAMA_URL) {
    $env:OLLAMA_URL = "http://localhost:11434"
}

New-Item -ItemType Directory -Force -Path $env:DATA_DIR | Out-Null

if (-not (Test-Path -LiteralPath $env:KNOWLEDGE_DIR -PathType Container)) {
    throw "Knowledge-Verzeichnis nicht gefunden: '$env:KNOWLEDGE_DIR'. Prüfe KNOWLEDGE_DIR in .env."
}

Write-Host "GLPI AI Agent (native Windows)"
Write-Host "  DATA_DIR      = $env:DATA_DIR"
Write-Host "  KNOWLEDGE_DIR = $env:KNOWLEDGE_DIR"
Write-Host "  OLLAMA_URL    = $env:OLLAMA_URL"
Write-Host ""

go run ./cmd/server
exit $LASTEXITCODE
