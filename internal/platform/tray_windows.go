//go:build windows

package platform

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	trayMessage     = wmApp + 1

	nimAdd        = 0x00000000
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	notifyIconVersion4 = 4
	mfString           = 0x00000000
	tpmRightButton     = 0x0002

	menuOpen = 1001
	menuExit = 1002

	idiApplication = 32512
	idcArrow       = 32512
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	HWnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClass struct {
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
}

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
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procRegisterClassW      = user32.NewProc("RegisterClassW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

// StartTray adds a native Windows notification-area icon. It is pure Win32/syscall,
// so the main binary stays CGO-free and remains buildable with Go 1.20 for Windows 7.
func StartTray(localURL string) (func(), error) {
	ready := make(chan error, 1)
	stop := make(chan struct{}, 1)
	go trayLoop(localURL, ready, stop)
	if err := <-ready; err != nil {
		return func() {}, err
	}
	return func() {
		select {
		case stop <- struct{}{}:
		default:
		}
	}, nil
}

func trayLoop(localURL string, ready chan<- error, stop <-chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := syscall.UTF16PtrFromString("LANShareTrayWindow")
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)

	var nid notifyIconData
	var hwnd uintptr
	wndProc := syscall.NewCallback(func(h uintptr, message uint32, wParam, lParam uintptr) uintptr {
		switch message {
		case trayMessage:
			switch uint32(lParam) {
			case wmLButtonDblClk:
				_ = OpenURL(localURL)
			case wmRButtonUp:
				showTrayMenu(h, localURL)
			}
			return 0
		case wmCommand:
			switch uint16(wParam & 0xffff) {
			case menuOpen:
				_ = OpenURL(localURL)
			case menuExit:
				_, _, _ = procPostMessageW.Call(h, wmClose, 0, 0)
			}
			return 0
		case wmClose:
			if nid.HWnd != 0 {
				_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
				nid.HWnd = 0
			}
			procPostQuitMessage.Call(0)
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
		ret, _, _ := procDefWindowProcW.Call(h, uintptr(message), wParam, lParam)
		return ret
	})

	wc := wndClass{LpfnWndProc: wndProc, HInstance: hInstance, HIcon: icon, HCursor: cursor, LpszClassName: className}
	atom, _, err := procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		ready <- fmt.Errorf("RegisterClassW: %v", err)
		return
	}
	hwnd, _, err = procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, hInstance, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW: %v", err)
		return
	}

	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = 1
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = trayMessage
	nid.HIcon = icon
	copy(nid.SzTip[:], syscall.StringToUTF16("LAN Share - double click to open"))
	ok, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 {
		ready <- fmt.Errorf("Shell_NotifyIconW: %v", err)
		return
	}
	nid.UVersion = notifyIconVersion4
	_, _, _ = procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&nid)))
	ready <- nil

	go func() {
		<-stop
		_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}()

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func showTrayMenu(hwnd uintptr, localURL string) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	openText, _ := syscall.UTF16PtrFromString("打开 LAN Share")
	exitText, _ := syscall.UTF16PtrFromString("退出")
	procAppendMenuW.Call(menu, mfString, menuOpen, uintptr(unsafe.Pointer(openText)))
	procAppendMenuW.Call(menu, mfString, menuExit, uintptr(unsafe.Pointer(exitText)))
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	_ = localURL
}
