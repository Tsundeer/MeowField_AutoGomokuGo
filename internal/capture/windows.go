//go:build windows

// Package capture：游戏窗口查找、客户区截屏、前台化、鼠标点击。
// 对应 Python 版 window.py，含 PrintWindow 客户区裁剪修复与自身进程排除。
package capture

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"syscall"
	"time"

	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procEnumWindows        = user32.NewProc("EnumWindows")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procGetWindowTextW     = user32.NewProc("GetWindowTextW")
	procGetWindowTextLenW  = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procGetClientRect      = user32.NewProc("GetClientRect")
	procClientToScreen     = user32.NewProc("ClientToScreen")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procPrintWindow        = user32.NewProc("PrintWindow")
	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procSetForegroundWnd   = user32.NewProc("SetForegroundWindow")
	procIsIconic           = user32.NewProc("IsIconic")
	procShowWindow         = user32.NewProc("ShowWindow")
	procKeybdEvent         = user32.NewProc("keybd_event")
	procSetCursorPos       = user32.NewProc("SetCursorPos")
	procSendInput          = user32.NewProc("SendInput")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procGetForegroundWnd   = user32.NewProc("GetForegroundWindow")

	procCreateCompatibleDC   = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBmp  = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject         = gdi32.NewProc("SelectObject")
	procDeleteObject         = gdi32.NewProc("DeleteObject")
	procDeleteDC             = gdi32.NewProc("DeleteDC")
	procGetDIBits            = gdi32.NewProc("GetDIBits")
	procGetModuleFileNameW   = kernel32.NewProc("GetModuleFileNameW")
	procOpenProcess          = kernel32.NewProc("OpenProcess")
	procQueryFullProcessName = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
)

const (
	pwRenderFullContent = 0x2
	inputMouse          = 0
	mouseLeftDown       = 0x0002
	mouseLeftUp         = 0x0004
	swRestore           = 9
	vkMenu              = 0x12
	keyeventfKeyup      = 0x0002
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type point struct{ X, Y int32 }

type bmiHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type mouseInput struct {
	Dx, Dy        int32
	MouseData     uint32
	DwFlags       uint32
	Time          uint32
	ExtraInfo     uintptr
}

type inputStruct struct {
	Type uint32
	Mi   mouseInput
	_    [8]byte // 对齐补齐（INPUT 联合体较大）
}

// WindowInfo 找到的窗口。
type WindowInfo struct {
	HWND    uintptr
	Title   string
	Process string
}

// TitleKeywords 窗口标题关键词（开放空间）。
var TitleKeywords = []string{"开放空间"}

// ProcessHints 进程名提示（launcher.exe 优先）。
var ProcessHints = []string{"launcher.exe"}

// FindGameWindow 按标题关键词找游戏窗口；排除自身进程；多个时优先进程名匹配。
func FindGameWindow() (WindowInfo, error) {
	selfPID := uint32(os.Getpid())
	var candidates []WindowInfo
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		if isWindowVisible(hwnd) == 0 {
			return 1
		}
		title := windowText(hwnd)
		if title == "" {
			return 1
		}
		for _, k := range TitleKeywords {
			if strings.Contains(title, k) {
				var pid uint32
				procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
				if pid == selfPID {
					return 1 // 排除自身
				}
				candidates = append(candidates, WindowInfo{hwnd, title, processName(pid)})
			}
			break
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	if len(candidates) == 0 {
		return WindowInfo{}, fmt.Errorf("未找到标题包含 %v 的窗口，请确认游戏已启动", TitleKeywords)
	}
	for _, w := range candidates {
		pl := strings.ToLower(w.Process)
		for _, hint := range ProcessHints {
			if strings.Contains(pl, hint) {
				return w, nil
			}
		}
	}
	return candidates[0], nil
}

func isWindowVisible(h uintptr) uintptr { r, _, _ := procIsWindowVisible.Call(h); return r }

func windowText(h uintptr) string {
	n, _, _ := procGetWindowTextLenW.Call(h)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func processName(pid uint32) string {
	h, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if h == 0 {
		return "?"
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 512)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessName.Call(h, uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&buf[0])))
	if r == 0 {
		return "?"
	}
	name := syscall.UTF16ToString(buf[:size])
	if i := strings.LastIndexAny(name, "\\/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// ClientRectScreen 客户区在屏幕上的 (left, top, w, h)。
func ClientRectScreen(hwnd uintptr) (int, int, int, int, error) {
	var rc rect
	if r, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); r == 0 {
		return 0, 0, 0, 0, fmt.Errorf("GetClientRect 失败")
	}
	var pt point
	procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&pt)))
	w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("客户区尺寸异常")
	}
	return int(pt.X), int(pt.Y), int(w), int(h), nil
}

// CaptureClient 截取客户区（PrintWindow 整窗渲染后按客户区裁剪，
// 失败回退 BitBlt 屏幕抓取——与 Python 版双通道一致）。
func CaptureClient(hwnd uintptr) (image.Image, int, int, error) {
	left, top, w, h, err := ClientRectScreen(hwnd)
	if err != nil {
		return nil, 0, 0, err
	}
	if img := printWindow(hwnd); img != nil && imageStdDev(img) > 2.5 {
		return img, left, top, nil
	}
	return bitBltScreen(left, top, w, h), left, top, nil
}

func printWindow(hwnd uintptr) image.Image {
	var wr rect
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr))); r == 0 {
		return nil
	}
	ww, wh := int(wr.Right-wr.Left), int(wr.Bottom-wr.Top)
	var cr rect
	if r, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&cr))); r == 0 {
		return nil
	}
	var pt point
	procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&pt)))
	ox, oy := int(pt.X)-int(wr.Left), int(pt.Y)-int(wr.Top)
	cw, ch := int(cr.Right-cr.Left), int(cr.Bottom-cr.Top)
	if cw <= 0 || ch <= 0 || ox < 0 || oy < 0 || ox+cw > ww || oy+ch > wh {
		return nil
	}
	hdc, _, _ := procGetDC.Call(hwnd)
	if hdc == 0 {
		return nil
	}
	defer procReleaseDC.Call(hwnd, hdc)
	mem, _, _ := procCreateCompatibleDC.Call(hdc)
	if mem == 0 {
		return nil
	}
	defer procDeleteDC.Call(mem)
	bmp, _, _ := procCreateCompatibleBmp.Call(hdc, uintptr(ww), uintptr(wh))
	if bmp == 0 {
		return nil
	}
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(mem, bmp)
	defer procSelectObject.Call(mem, old)
	if r, _, _ := procPrintWindow.Call(hwnd, mem, pwRenderFullContent); r == 0 {
		return nil
	}
	var bmi bmiHeader
	bmi.Size = uint32(unsafe.Sizeof(bmi))
	bmi.Width = int32(ww)
	bmi.Height = -int32(wh) // 顶行在前
	bmi.Planes = 1
	bmi.BitCount = 32
	buf := make([]uint8, ww*wh*4)
	if r, _, _ := procGetDIBits.Call(mem, bmp, 0, uintptr(wh),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bmi)), 0); r == 0 {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, cw, ch))
	for y := 0; y < ch; y++ {
		srcRow := buf[((oy+y)*ww+ox)*4 : ((oy+y)*ww+ox+cw)*4]
		for x := 0; x < cw; x++ {
			// DIB 为 BGRA
			out.SetRGBA(x, y, color.RGBA{srcRow[x*4+2], srcRow[x*4+1], srcRow[x*4], 255})
		}
	}
	return out
}

func bitBltScreen(left, top, w, h int) image.Image {
	// 屏幕区域抓取（GetDC(0) + GetDIBits）
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return image.NewRGBA(image.Rect(0, 0, w, h))
	}
	defer procReleaseDC.Call(0, hdc)
	mem, _, _ := procCreateCompatibleDC.Call(hdc)
	defer procDeleteDC.Call(mem)
	bmp, _, _ := procCreateCompatibleBmp.Call(hdc, uintptr(w), uintptr(h))
	defer procDeleteObject.Call(bmp)
	procSelectObject.Call(mem, bmp)
	// BitBlt SRCCOPY
	user32.NewProc("BitBlt").Call(mem, 0, 0, uintptr(w), uintptr(h), hdc,
		uintptr(left), uintptr(top), 0x00CC0020)
	var bmi bmiHeader
	bmi.Size = uint32(unsafe.Sizeof(bmi))
	bmi.Width = int32(w)
	bmi.Height = -int32(h)
	bmi.Planes = 1
	bmi.BitCount = 32
	buf := make([]uint8, w*h*4)
	procGetDIBits.Call(mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bmi)), 0)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := buf[y*w*4 : (y+1)*w*4]
		for x := 0; x < w; x++ {
			out.SetRGBA(x, y, color.RGBA{row[x*4+2], row[x*4+1], row[x*4], 255})
		}
	}
	return out
}

func imageStdDev(img image.Image) float64 {
	b := img.Bounds()
	var sum, sumSq float64
	n := float64(b.Dx() * b.Dy())
	if n == 0 {
		return 0
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			v := (float64(r>>8) + float64(g>>8) + float64(bl>>8)) / 3
			sum += v
			sumSq += v * v
		}
	}
	mean := sum / n
	return math.Sqrt(sumSq/n - mean*mean)
}

// FocusWindow 前台化（含 Alt 键绕过与最小化恢复）。
func FocusWindow(hwnd uintptr) {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procSetForegroundWnd.Call(hwnd)
	procKeybdEvent.Call(vkMenu, 0, keyeventfKeyup, 0)
}

// IsForeground 窗口是否在前台。
func IsForeground(hwnd uintptr) bool {
	r, _, _ := procGetForegroundWnd.Call()
	return r == hwnd
}

// MoveMouse 移动鼠标。
func MoveMouse(x, y int) { procSetCursorPos.Call(uintptr(x), uintptr(y)) }

// ClickAt 移动并左键单击（hold 为按下持续时间）。
func ClickAt(x, y int, hold time.Duration) {
	MoveMouse(x, y)
	time.Sleep(50 * time.Millisecond)
	miDown := mouseInput{DwFlags: mouseLeftDown}
	in := inputStruct{Type: inputMouse, Mi: miDown}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	time.Sleep(hold)
	miUp := mouseInput{DwFlags: mouseLeftUp}
	in2 := inputStruct{Type: inputMouse, Mi: miUp}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in2)), unsafe.Sizeof(in2))
}

// GetCursorPos 当前光标位置。
func GetCursorPos() (int, int, bool) {
	var pt point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return 0, 0, false
	}
	return int(pt.X), int(pt.Y), true
}


// WarmPoint 预热/中性鼠标点（客户区顶部中央，避开按钮与棋盘）。
func WarmPoint(hwnd uintptr) (p image.Point) {
	left, top, w, _, err := ClientRectScreen(hwnd)
	if err != nil {
		return image.Point{X: left + w/2, Y: top + 8}
	}
	return image.Point{X: left + w/2, Y: top + 8}
}
