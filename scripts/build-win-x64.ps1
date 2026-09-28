# MeowField_AutoGomokuGo 打包脚本（Go/Wails 版）
# 产物：
#   artifacts\MeowField_AutoGomokuGo-{ver}-win-x64.zip        便携版（exe + 引擎）
#   artifacts\MeowField_AutoGomokuGo-{ver}-win-x64-Setup.exe  安装版（需 Inno Setup 6）
param([switch]$SkipInstaller)
$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

$initFile = Join-Path $repoRoot "internal\version\version.go"
if (-not (Test-Path $initFile)) { $initFile = Join-Path $repoRoot "internal\version\version.go" }
$verText = Get-Content (Join-Path $repoRoot "internal\version\version.go") -Raw
if ($verText -notmatch 'Version\s*=\s*"(\d+\.\d+\.\d+)"') { throw "无法读取版本号" }
$version = $Matches[1]
Write-Host "== MeowField_AutoGomokuGo (Go) v$version ==" -ForegroundColor Cyan

# ---- 定位 wails CLI ----
$wails = Get-Command wails -ErrorAction SilentlyContinue
if (-not $wails) {
    $cand = Join-Path $env:USERPROFILE "go/bin/wails.exe"
    if (Test-Path $cand) { $wails = $cand } else { throw "未找到 wails CLI（go install github.com/wailsapp/wails/v2/cmd/wails@latest）" }
}

# ---- 清理与构建 ----
$artifacts = Join-Path $repoRoot "artifacts"
if (Test-Path $artifacts) { Remove-Item $artifacts -Recurse -Force }
Remove-Item (Join-Path $repoRoot "build\bin") -Recurse -Force -ErrorAction SilentlyContinue
& $wails build -ldflags "-s -w"
if ($LASTEXITCODE -ne 0) { throw "wails build 失败" }

# ---- 便携版 zip：exe + engines ----
$stage = Join-Path $repoRoot "build\stage"
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
New-Item -ItemType Directory -Force -Path $stage | Out-Null
Copy-Item (Join-Path $repoRoot "build\bin\MeowField_AutoGomokuGo.exe") $stage
Copy-Item (Join-Path $repoRoot "engines") (Join-Path $stage "engines") -Recurse
Copy-Item (Join-Path $repoRoot "README.md") $stage -ErrorAction SilentlyContinue
Copy-Item (Join-Path $repoRoot "LICENSE") $stage -ErrorAction SilentlyContinue

$publishArt = Join-Path $artifacts "publish"
New-Item -ItemType Directory -Force -Path $publishArt | Out-Null
$zip = Join-Path $publishArt "MeowField_AutoGomokuGo-$version-win-x64.zip"
# ---- JAX CUDA DLL 单独成包（GPU 增强包，主包保持轻量）----
$jaxDir = Join-Path $stage "engines\jax"
$cudaDlls = @("cudart64_110.dll","cublas64_11.dll","cublaslt64_11.dll",
  "cudnn64_8.dll","cudnn_adv_infer64_8.dll","cudnn_cnn_infer64_8.dll",
  "cudnn_ops_infer64_8.dll","cufft64_10.dll","zlibwapi.dll",
  "cudnn64_9.dll","cudnn_adv64_9.dll","cudnn_cnn64_9.dll",
  "cudnn_engines_precompiled64_9.dll","cudnn_engines_runtime_compiled64_9.dll",
  "cudnn_graph64_9.dll","cudnn_heuristic64_9.dll","cudnn_ops64_9.dll")
$present = $cudaDlls | Where-Object { Test-Path (Join-Path $jaxDir $_) }
if ($present.Count -gt 0) {
    $gpuDir = Join-Path $repoRoot "build\jax-cuda-dlls"
    New-Item -ItemType Directory -Force -Path $gpuDir | Out-Null
    foreach ($d in $present) {
        Copy-Item (Join-Path $jaxDir $d) $gpuDir -Force
        Remove-Item (Join-Path $jaxDir $d) -Force
    }
    $gpuZip = Join-Path $publishArt "MeowField_AutoGomokuGo-$version-jax-cuda-dlls.zip"
    Compress-Archive -Path "$gpuDir\*" -DestinationPath $gpuZip -Force
    Remove-Item $gpuDir -Recurse -Force
    # 主包 jax config 复位为 cpu（CUDA DLL 已抽走，避免无运行时用户挂起）
    $jaxCfg = Join-Path $jaxDir "configs\config.toml"
    if (Test-Path $jaxCfg) {
        (Get-Content $jaxCfg -Raw) -replace 'device = "cuda"', 'device = "cpu"' |
            Set-Content $jaxCfg -Encoding UTF8
    }
    Write-Host "JAX GPU 增强包: $gpuZip" -ForegroundColor Green

Compress-Archive -Path "$stage\*" -DestinationPath $zip -Force
Write-Host "便携版: $zip" -ForegroundColor Green

}


# ---- 安装版 ----
if (-not $SkipInstaller) {
    $iscc = @("$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
              "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
              "$env:ProgramFiles\Inno Setup 6\ISCC.exe") |
        Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
    if (-not $iscc) { Write-Warning "未找到 Inno Setup 6，跳过安装器"; return }
    & $iscc "/DMyAppVersion=$version" "/DStageDir=$stage" `
        (Join-Path $repoRoot "installer\MeowField_AutoGomokuGo.iss")
    if ($LASTEXITCODE -ne 0) { throw "Inno Setup 编译失败" }
    Write-Host "安装版: artifacts\installer\" -ForegroundColor Green
}
Write-Host "== 完成 v$version ==" -ForegroundColor Cyan
