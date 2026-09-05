//go:build windows

// Package tray 提供零依赖的 Windows 系统托盘图标（Shell_NotifyIcon + Win32 消息循环）。
// 托盘菜单：打开页面 / 退出。左键或右键单击图标都会弹出菜单。
//
// 初始化每一步都写日志（配合 --tray 模式下 main 的日志重定向，位于 %TEMP%\zre.log），
// 便于排查"图标不可见"类问题。
package tray

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

//go:embed app.ico
var iconData []byte

type Options struct {
	Tooltip string
	OnOpen  func() // 菜单"打开页面"
	OnQuit  func() // 菜单"退出"（消息循环结束后调用）
}

const (
	wmApp       = 0x8000
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmNull      = 0x0000
	wmDestroy   = 0x0002

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10
	niifInfo   = 0x01

	imageIcon      = 1
	lrLoadFromFile = 0x10
	lrDefaultSize  = 0x40

	tpmRightButton = 0x02
	tpmReturnCmd   = 0x0100
	mfString       = 0x00
	mfSeparator    = 0x800

	idOpen = 1
	idQuit = 2

	idAppIcon = 32512 // IDI_APPLICATION
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [128]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  uintptr
	LpszClassName uintptr
	HIconSm       uintptr
}

type point struct {
	X, Y int32
}

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

const trayCallback = wmApp + 1

var (
	hwnd              uintptr
	nid               notifyIconData
	opts              Options
	hIcon             uintptr
	msgTaskbarCreated uintptr
)

func wndProc(h, m, w, l uintptr) uintptr {
	switch m {
	case trayCallback:
		if lo := l & 0xFFFF; lo == wmLButtonUp || lo == wmRButtonUp {
			showMenu(h)
		}
		return 0
	case msgTaskbarCreated:
		// explorer 重启会清空所有托盘图标，收到该广播后重新注册
		log.Println("[tray] 收到 TaskbarCreated（explorer 重启），重新注册托盘图标")
		if shellNotify(nimAdd) {
			log.Println("[tray] 重新注册成功")
		} else {
			log.Println("[tray] 重新注册失败")
		}
		return 0
	case wmDestroy:
		shellNotify(nimDelete)
		if hIcon != 0 {
			procDestroyIcon.Call(hIcon)
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(h, m, w, l)
	return r
}

func showMenu(h uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	appendItem(menu, mfString, idOpen, "打开页面")
	appendItem(menu, mfSeparator, 0, "")
	appendItem(menu, mfString, idQuit, "退出")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(h) // 保证点击菜单外可关闭
	ret, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y), 0, h, 0)
	procDestroyMenu.Call(menu)
	procPostMessageW.Call(h, wmNull, 0, 0)

	switch ret {
	case idOpen:
		if opts.OnOpen != nil {
			opts.OnOpen()
		}
	case idQuit:
		procDestroyWindow.Call(h)
	}
}

func appendItem(menu, flags, id uintptr, text string) {
	var p uintptr
	if text != "" {
		if sp, err := syscall.UTF16PtrFromString(text); err == nil {
			p = uintptr(unsafe.Pointer(sp))
		}
	}
	procAppendMenuW.Call(menu, flags, id, p)
}

func shellNotify(code uint32) bool {
	r, _, _ := procShellNotifyIconW.Call(uintptr(code), uintptr(unsafe.Pointer(&nid)))
	return r != 0
}

func shellNotifyData(code uint32, d *notifyIconData) bool {
	r, _, _ := procShellNotifyIconW.Call(uintptr(code), uintptr(unsafe.Pointer(d)))
	return r != 0
}

// showBalloon 弹出托盘气泡通知（图标在隐藏区时气泡也会显示，可自证图标存活）。
func showBalloon(title, text string) {
	mod := nid
	mod.UFlags = nifInfo
	mod.DwInfoFlags = niifInfo
	if t, err := syscall.UTF16FromString(title); err == nil {
		copy(mod.SzInfoTitle[:], t)
	}
	if t, err := syscall.UTF16FromString(text); err == nil {
		copy(mod.SzInfo[:], t)
	}
	if !shellNotifyData(nimModify, &mod) {
		log.Println("[tray] 气泡通知发送失败")
	} else {
		log.Println("[tray] 气泡通知已发送（若可见则说明图标条目存活）")
	}
}

// Run 创建托盘图标并进入消息循环（阻塞），直到用户选择"退出"。
func Run(o Options) error {
	opts = o
	runtime.LockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)

	className, _ := syscall.UTF16PtrFromString("ZRETrayClass")
	title, _ := syscall.UTF16PtrFromString("ZRE")
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hInst,
		LpszClassName: uintptr(unsafe.Pointer(className)),
	}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return os.NewSyscallError("RegisterClassExW", err)
	}
	log.Println("[tray] 窗口类已注册")

	hwnd, _, _ = procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, hInst, 0)
	if hwnd == 0 {
		return os.NewSyscallError("CreateWindowExW", syscall.GetLastError())
	}
	log.Printf("[tray] 消息窗口已创建 hwnd=0x%x", hwnd)

	// explorer 重启时 Windows 会广播该消息，需要重新注册图标
	if p, _, err := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(mustUTF16("TaskbarCreated")))); p != 0 {
		msgTaskbarCreated = p
	} else {
		log.Println("[tray] 注册 TaskbarCreated 失败:", err)
	}

	// 图标：内嵌 ICO 写到可靠的可写目录后按文件加载；失败则退回系统默认图标。
	// 注意不能用 os.TempDir()：Git Bash / zcode 终端里 TMP=/tmp（POSIX 风格），
	// 在 Windows 上会解析成不存在的 C:	mp，导致图标静默加载失败（图标不可见的根因）。
	iconPath := filepath.Join(cacheDir(), "zre-tray.ico")
	if err := os.WriteFile(iconPath, iconData, 0o644); err != nil {
		log.Println("[tray] 写入图标文件失败:", err)
	}
	iconPathPtr, _ := syscall.UTF16PtrFromString(iconPath)
	hIcon, _, _ = procLoadImageW.Call(0, uintptr(unsafe.Pointer(iconPathPtr)),
		imageIcon, 0, 0, lrDefaultSize|lrLoadFromFile)
	if hIcon != 0 {
		log.Println("[tray] 自定义图标加载成功:", iconPath)
	} else {
		hIcon, _, _ = procLoadIconW.Call(0, uintptr(idAppIcon))
		log.Printf("[tray] 自定义图标加载失败（err=%v），已退回系统默认图标", syscall.GetLastError())
	}

	nid = notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(nid)),
		HWnd:             hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: trayCallback,
		HIcon:            hIcon,
	}
	if tip, err := syscall.UTF16FromString(o.Tooltip); err == nil {
		copy(nid.SzTip[:], tip)
	}
	if !shellNotify(nimAdd) {
		return os.NewSyscallError("Shell_NotifyIconW(NIM_ADD)", syscall.GetLastError())
	}
	log.Println("[tray] 托盘图标注册成功（NIM_ADD 返回真）；若任务栏看不到，请在 设置 → 个性化 → 任务栏 → 其他系统托盘图标 中开启 ZRE，并检查溢出区 ^")

	showBalloon("ZRE", "ZRE 正在后台运行；点击任务栏 ^ 可找到图标，右键图标可打开页面或退出。")

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 { // WM_QUIT
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func mustUTF16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// cacheDir 返回 guaranteed-writable 的缓存目录：%LocalAppData%\zre，
// 不依赖 TMP 环境变量；失败时退回可执行文件所在目录。
func cacheDir() string {
	if base, err := os.UserCacheDir(); err == nil {
		d := filepath.Join(base, "zre")
		if os.MkdirAll(d, 0o755) == nil {
			return d
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}
