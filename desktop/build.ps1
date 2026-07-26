$ErrorActionPreference = "Stop"
$outputDir = Join-Path $PSScriptRoot "..\dist"
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
Push-Location $PSScriptRoot
try {
    go run .\tools\icon -input .\build\appicon.png -output .\build\windows\icon.ico
    if ($LASTEXITCODE -ne 0) {
        throw "Icon generation failed with exit code $LASTEXITCODE"
    }
    go run github.com/wailsapp/wails/v2/cmd/wails@v2.12.0 build -s -skipbindings -o ASMRoner.exe
    if ($LASTEXITCODE -ne 0) {
        throw "Wails build failed with exit code $LASTEXITCODE"
    }
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "build\bin\ASMRoner.exe") -Destination (Join-Path $outputDir "ASMRoner.exe") -Force
}
finally {
    Pop-Location
}
Write-Host "Built: $outputDir\ASMRoner.exe"
