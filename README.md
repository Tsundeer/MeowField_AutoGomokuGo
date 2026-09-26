# MeowField_AutoGomokuGo 使用说明书

**版本 2.0.2（Go 版）** · 作者与软件署名：薮猫 · 项目仓库：github.com/Tsundeer/MeowField_AutoGomokuGo

面向 Windows 的《开放空间》五子棋自动识别与对弈工具。
本仓库为 **Go + Wails 重写版**（原 Python 版见 MeowField_AutoGomoku）：
单文件 exe（34MB）、启动 <1 秒、内存占用低，界面与功能对齐 Python 版。

## 功能

- 棋盘识别：面板定位 + 13×13 网格拟合 + 落子分类（分辨率无关，金标准测试保护）
- 轮次/执色自适应：子数定轮次、点击铁证锁颜色，中盘随时接管
- 先手开局自动下中心 H7
- 引擎：Rapfi（Gomocup 冠军开源引擎，子进程 Gomocup 协议）+ 内置纯 Go 兜底引擎
- 点击偏移自动校准 + 手动偏移设置；悬停预览过滤；前台校验与自动重试
- 深浅色主题；GitHub Release 更新检查（API/atom/网页三级降级 + 缓存）
- 启动自动申请管理员权限（游戏常以管理员运行，否则点击被 UIPI 拦截）

## 安装

- **方式 A**：[Releases](../../releases) 下载 `MeowField_AutoGomokuGo-{ver}-win-x64.zip`，
  解压后运行 `MeowField_AutoGomokuGo.exe`（需 WebView2 运行时，Win10 21H2+/Win11 自带）。
- **方式 B**：下载 `-Setup.exe` 安装版。

## 使用

1. 启动游戏，进入五子棋对局界面。
2. 启动本程序（首次弹 UAC 请选择「是」——无管理员权限自动点击会被系统拦截）。
3. 「截图测试识别」确认识别正常 → 选执色 → 「开始自动对弈」。

## 从源码构建

需要 Go 1.25+ 与 Wails CLI：

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails build -ldflags "-s -w"
# 产物：build/bin/MeowField_AutoGomokuGo.exe（engines/ 需与 exe 同目录）
```

或直接：`powershell -ExecutionPolicy Bypass -File scripts\build-win-x64.ps1`
（自动产出便携 zip 与 Inno Setup 安装器）。

## 测试

```bash
go test ./...
```

- `internal/vision`：金标准样本识别、多分辨率（720p~1440p）、合成棋子回读
- `internal/engine`：连五/堵四/中盘/天元/自对弈
- `internal/autoplay`：接管、执色学习与纠正、同批差分停摆回归、先手开局等 9 场景

## 架构

```
app.go                     Wails 装配 + 管理员提权 + 入口
frontend/                  深浅色主题界面（HTML/CSS/JS）
internal/domain            棋盘模型与轮次判定（纯逻辑）
internal/vision            棋盘识别（HSV 面板/网格投影/交叉点分类）
internal/capture           窗口查找、PrintWindow/BitBlt 截屏、SendInput 点击
internal/engine            Rapfi 协议客户端 + 内置 α-β 引擎
internal/autoplay          自动对弈状态机
internal/storage           设置（原子写+备份）、日志目录
internal/updater           GitHub Release 更新检查（API/atom/网页降级）
```

## 致谢与许可

- 本项目代码基于 **GPL-3.0** 开源（见 [LICENSE](LICENSE)）。
- 引擎 [Rapfi](https://github.com/dhbloo/rapfi)（Gomocup 冠军引擎）版权归
  原作者所有，遵循 GPL-3.0，源码可在其仓库获取。

> 免责声明：仅供学习交流使用，请自行评估在游戏中使用自动化工具的合规风险。
