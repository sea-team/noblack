param()
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$runtime = Join-Path $root ".runtime"
New-Item -ItemType Directory -Force -Path $runtime | Out-Null

# 版本与 .build/*-venv 构建环境保持一致, 避免运行时与打包时的 torch 版本不符。
python -m pip install --target $runtime `
  --index-url https://download.pytorch.org/whl/cpu `
  torch==2.6.0+cpu
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

python -m pip install --target $runtime `
  --upgrade --no-cache-dir `
  -r (Join-Path $root "model_service\requirements.txt")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "CPU 模型运行依赖已安装到 $runtime" -ForegroundColor Green
