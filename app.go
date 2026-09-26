// MeowField_AutoGomokuGo —— Wails 桌面应用（Go 版）。
package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"context"
	"syscall"
	"unsafe"

	"MeowField_AutoGomokuGo/internal/autoplay"
	"MeowField_AutoGomokuGo/internal/engine"
		"MeowField_AutoGomokuGo/internal/storage"
	"MeowField_AutoGomokuGo/internal/updater"
	"MeowField_AutoGomokuGo/internal/version"
	"MeowField_AutoGomokuGo/internal/vision"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assetsFS embed.FS

const (
	repoURL  = "https://github.com/Tsundeer/MeowField_AutoGomokuGo"
	appTitle = "MeowField_AutoGomoku · 开放空间"
)

var (
	shell32dll = syscall.NewLazyDLL("shell32.dll")
	procShellE = shell32dll.NewProc("ShellExecuteW")
	procIsAdm  = shell32dll.NewProc("IsUserAnAdmin") // 注意：IsUserAnAdmin 在 shell32
)

// App Wails 绑定层。
type App struct {
	ctx      context.Context
	svc      *autoplay.Service
	settings map[string]any
}

// Version 版本。
func (a *App) Version() string { return version.Version }

// GetSettings 读取设置。
func (a *App) GetSettings() map[string]any { return a.settings }

// SaveSettings 保存并热更新。
func (a *App) SaveSettings(m map[string]any) error {
	if err := storage.SaveSettings(m); err != nil {
		return err
	}
	a.settings = storage.LoadSettings()
	a.svc.SetSettings(autoplay.Settings{
		OurColor:      toS(a.settings["our_color"]),
		EngineKind:    toS(a.settings["engine"]),
		MoveDelay:     toF(a.settings["move_delay"]),
		EngineThreads: int(toF(a.settings["engine_threads"])),
		ThinkLimit:    toF(a.settings["think_limit"]),
		ClickOffsetX:  int(toF(a.settings["click_offset_x"])),
		ClickOffsetY:  int(toF(a.settings["click_offset_y"])),
		MateRush:      toB(a.settings["mate_rush"]),
		GPUDevice:     toS(a.settings["gpu_device"]),
	})
	return nil
}

// StartAuto 开始自动对弈。
func (a *App) StartAuto() { a.svc.SetActive(true) }

// StopAuto 停止自动对弈。
func (a *App) StopAuto() { a.svc.SetActive(false) }

// TestShot 截图测试识别。
func (a *App) TestShot() { a.svc.RequestTestShot() }

// OpenDebugDir 打开调试目录。
func (a *App) OpenDebugDir() {
	verb, _ := syscall.UTF16PtrFromString("open")
	arg, _ := syscall.UTF16PtrFromString(storage.DebugDir())
	procShellE.Call(0, uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(arg)), 0, 0, 1)
}

// EngineInfo 引擎可用性（UI 动态构建下拉）。
type EngineInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Note      string `json:"note"`
}

// ListEngines 扫描 engines/ 返回各引擎可用性。
func (a *App) ListEngines() []EngineInfo {
	f := engine.DetectEngines()
	out := []EngineInfo{
		{Name: "rapfi", Available: len(f.Rapfi) > 0, Note: "CPU 强引擎（推荐）"},
		{Name: "jax", Available: f.Jax != "", Note: "GPU: CUDA/TensorRT（需自装运行时）"},
		{Name: "katagomo", Available: f.Katagomo != "", Note: "GPU: CUDA（1.7GB 自备，放 engines/katagomo/）"},
		{Name: "simple", Available: true, Note: "内置简易引擎（兜底）"},
	}
	if a.settings["engine"] == "auto" {
		// auto 顺序：rapfi 优先
		out = append([]EngineInfo{{Name: "auto", Available: true,
			Note: "自动选择可用引擎（推荐）"}}, out...)
	}
	return out
}

// CheckUpdate 检查更新（多端点降级 + 缓存）。
func (a *App) CheckUpdate() updater.Result {
	return updater.Check(version.Version, true)
}

func toS(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "auto"
}

func toB(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	}
	return false
}

func toF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

func isAdmin() bool {
	r, _, _ := procIsAdm.Call()
	return r != 0
}

// elevateIfRequired 强制提权：非管理员 -> runas 重启自身；
// 用户取消 UAC 则弹窗说明并退出（无权限时自动点击会被 UIPI 拦截）。
func elevateIfRequired() bool {
	if isAdmin() {
		log.Println("以管理员权限运行")
		return true
	}
	log.Println("当前为普通权限，请求管理员权限（UAC）…")
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	arg, _ := syscall.UTF16PtrFromString(`"` + exe + `"`)
	r, _, _ := procShellE.Call(0, uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(arg)), 0, 0, 1)
	if r > 32 {
		log.Println("已发起提权重启，本进程退出")
		return false
	}
	log.Println("管理员提权被取消，按策略退出")
	msgbox := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString(appTitle)
	text, _ := syscall.UTF16PtrFromString(
		"本工具需要管理员权限才能自动点击游戏（游戏通常以管理员运行，" +
			"普通权限的模拟点击会被系统拦截）。\n\n请重新启动并在 UAC 弹窗中选择「是」。")
	msgbox.Call(0, uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)), 0x30)
	return false
}

func setupFileLogger() {
	logFile := filepath.Join(storage.LogsDir(), "app.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime)
}

func boardPayload(det *vision.Detection, svc *autoplay.Service) map[string]any {
	stones := []map[string]any{}
	for _, st := range det.Stones() {
		stones = append(stones, map[string]any{"label": st.Label, "color": st.Color})
	}
	await := []int{}
	if ar, ac, ok := svc.Awaiting(); ok {
		await = []int{ar, ac}
	}
	return map[string]any{"stones": stones, "awaiting": await, "count": len(stones)}
}

func run() int {
	setupFileLogger()
	// Wails 绑定生成（-tags bindings）会运行本二进制，此时跳过提权等启动逻辑
	if bindingGen {
		wails.Run(&options.App{})
		return 0
	}
	if !elevateIfRequired() {
		return 0
	}
	settings := storage.LoadSettings()

	var appObj *App
	emit := func(kind string, payload any) {
		if appObj == nil || appObj.ctx == nil {
			log.Printf("[%s] %v", kind, payload)
			return
		}
		if kind == "board" {
			if det, ok := payload.(*vision.Detection); ok {
				wruntime.EventsEmit(appObj.ctx, "board",
					boardPayload(det, appObj.svc))
				return
			}
		}
		wruntime.EventsEmit(appObj.ctx, kind, payload)
	}

	svc := autoplay.NewService(autoplay.Settings{
		OurColor:      toS(settings["our_color"]),
		EngineKind:    toS(settings["engine"]),
		MoveDelay:     toF(settings["move_delay"]),
		EngineThreads: int(toF(settings["engine_threads"])),
		ThinkLimit:    toF(settings["think_limit"]),
		ClickOffsetX:  int(toF(settings["click_offset_x"])),
		ClickOffsetY:  int(toF(settings["click_offset_y"])),
		MateRush:      toB(settings["mate_rush"]),
		GPUDevice:     toS(settings["gpu_device"]),
	}, func(f string, a ...any) {
		msg := fmt.Sprintf(f, a...)
		log.Print(msg)
		emit("log", msg)
	}, emit)
	appObj = &App{svc: svc, settings: settings}

	err := wails.Run(&options.App{
		Title:     appTitle,
		Width:     1120,
		Height:    720,
		MinWidth:  1060,
		MinHeight: 660,
		AssetServer: &assetserver.Options{
			Assets: assetsFS,
		},
		OnStartup: func(ctx context.Context) {
			appObj.ctx = ctx
			svc.Start()
		},
		OnShutdown: func(ctx context.Context) { svc.Stop() },
		Bind:       []interface{}{appObj},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
		},
	})
	if err != nil {
		log.Fatalf("UI 退出: %v", err)
	}
	return 0
}

func main() { os.Exit(run()) }
