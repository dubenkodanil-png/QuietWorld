package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var user = syscall.NewLazyDLL("user32.dll")
var kernel = syscall.NewLazyDLL("kernel32.dll")
var ole = syscall.NewLazyDLL("ole32.dll")
var hwnd, controller, view uintptr
var target string
var closing bool
var startupErr error
var webDLL *syscall.DLL
var handlerEnv, handlerCtl, handlerClose comHandler
var vEnv, vCtl, vClose [4]uintptr
var unknown = guid{0, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}
type comHandler struct {
	V   *[4]uintptr
	IID guid
}
type rect struct{ L, T, R, B int32 }
type point struct{ X, Y int32 }
type msg struct {
	H       uintptr
	M       uint32
	W, L    uintptr
	T       uint32
	P       point
	Private uint32
}
type wndclass struct {
	Size, Style                   uint32
	Proc                          uintptr
	ClsExtra, WndExtra            int32
	Instance, Icon, Cursor, Brush uintptr
	Menu, Class                   *uint16
	SmallIcon                     uintptr
}

func ptr[T any](p *T) uintptr                { return uintptr(unsafe.Pointer(p)) }
func utf(s string) *uint16                   { p, _ := syscall.UTF16PtrFromString(s); return p }
func call(n string, args ...uintptr) uintptr { r, _, _ := user.NewProc(n).Call(args...); return r }
func com(p uintptr, idx int, args ...uintptr) uintptr {
	v := *(*uintptr)(unsafe.Pointer(p))
	f := *(*uintptr)(unsafe.Pointer(v + uintptr(idx)*unsafe.Sizeof(p)))
	r, _, _ := syscall.SyscallN(f, append([]uintptr{p}, args...)...)
	return r
}
func failed(r uintptr) bool { return int32(r) < 0 }
func showError(s string) {
	call("MessageBoxW", 0, ptr(utf(s)), ptr(utf("Тихий мир 5.6.0")), 16)
}
func fail(where string, r uintptr) {
	startupErr = fmt.Errorf("%s (0x%08X). Проверьте установку Microsoft Edge WebView2 Runtime.", where, uint32(r))
	call("PostMessageW", hwnd, 0x8002, 0, 0)
}
func qi(this, riid, out uintptr) uintptr {
	if out == 0 {
		return 0x80004003
	}
	*(*uintptr)(unsafe.Pointer(out)) = 0
	if riid == 0 {
		return 0x80004002
	}
	h := (*comHandler)(unsafe.Pointer(this))
	g := *(*guid)(unsafe.Pointer(riid))
	if g == unknown || g == h.IID {
		*(*uintptr)(unsafe.Pointer(out)) = this
		return 0
	}
	return 0x80004002
}
func addref(this uintptr) uintptr  { return 2 }
func release(this uintptr) uintptr { return 1 }
func resize() {
	if controller == 0 {
		return
	}
	var r rect
	call("GetClientRect", hwnd, ptr(&r))
	com(controller, 6, ptr(&r))
	runtime.KeepAlive(&r)
}
func envDone(this, hr, p uintptr) uintptr {
	if failed(hr) || p == 0 {
		fail("Не удалось создать окружение WebView2", hr)
		return 0
	}
	r := com(p, 3, hwnd, ptr(&handlerCtl))
	if failed(r) {
		fail("Не удалось создать контроллер WebView2", r)
	}
	return 0
}
func ctlDone(this, hr, p uintptr) uintptr {
	if failed(hr) || p == 0 {
		fail("Не удалось создать окно WebView2", hr)
		return 0
	}
	controller = p
	com(p, 1)
	r := com(p, 25, ptr(&view))
	if failed(r) || view == 0 {
		fail("Не удалось получить WebView2", r)
		return 0
	}
	resize()
	com(p, 4, 1)
	var settings uintptr
	if !failed(com(view, 3, ptr(&settings))) && settings != 0 {
		com(settings, 8, 1)
		com(settings, 14, 0)
		com(settings, 16, 0)
		com(settings, 2)
	}
	r = com(view, 5, ptr(utf(target)))
	if failed(r) {
		fail("Не удалось открыть игру", r)
	}
	return 0
}
func closeDone(this, hr, result uintptr) uintptr { call("PostMessageW", hwnd, 0x8002, 0, 0); return 0 }
func wndProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x8003:
		applyDesktopSize(w, l)
		return 0
	case 5:
		resize()
		return 0
	case 3:
		if controller != 0 {
			com(controller, 23)
		}
		return 0
	case 0x0010:
		if closing {
			return 0
		}
		closing = true
		if view != 0 {
			r := com(view, 29, ptr(utf("window.qwClose ? window.qwClose() : true")), ptr(&handlerClose))
			if !failed(r) {
				call("SetTimer", h, 1, 3000, 0)
				return 0
			}
		}
		call("DestroyWindow", h)
		return 0
	case 0x0113:
		if w == 1 {
			call("KillTimer", h, 1)
			call("DestroyWindow", h)
		}
		return 0
	case 0x8002:
		call("KillTimer", h, 1)
		call("DestroyWindow", h)
		return 0
	case 2:
		call("PostQuitMessage", 0)
		return 0
	}
	return call("DefWindowProcW", h, uintptr(m), w, l)
}
func ver(path string) []int {
	s := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
	var a []int
	for _, n := range strings.Split(s, ".") {
		v, _ := strconv.Atoi(n)
		a = append(a, v)
	}
	return a
}
func findRuntime() string {
	var paths []string
	for _, key := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA", "ProgramW6432"} {
		if base := os.Getenv(key); base != "" {
			p, _ := filepath.Glob(filepath.Join(base, "Microsoft", "EdgeWebView", "Application", "*", "EBWebView", "x64", "EmbeddedBrowserWebView.dll"))
			paths = append(paths, p...)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := ver(paths[i]), ver(paths[j])
		for n := 0; n < len(a) && n < len(b); n++ {
			if a[n] != b[n] {
				return a[n] > b[n]
			}
		}
		return len(a) > len(b)
	})
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}
func loadRuntime(path string) (*syscall.DLL, error) {
	h, _, e := kernel.NewProc("LoadLibraryExW").Call(ptr(utf(path)), 0, 8)
	if h == 0 {
		return nil, e
	}
	return &syscall.DLL{Name: path, Handle: syscall.Handle(h)}, nil
}
func desktop(url, profile string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// WebView2 executes in a real Win32 window, not a browser tab.
	target = url
	call("SetProcessDPIAware")
	r, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
	if failed(r) {
		return fmt.Errorf("COM initialization: 0x%08X", uint32(r))
	}
	defer ole.NewProc("CoUninitialize").Call()
	if e := os.MkdirAll(profile, 0700); e != nil {
		return e
	}
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disable-background-timer-throttling --disable-renderer-backgrounding --disable-backgrounding-occluded-windows")
	inst, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	cursor := call("LoadCursorW", 0, 32512)
	icon := call("LoadIconW", 0, 32512)
	class := utf("QuietWorldDesktopV3")
	wc := wndclass{Size: uint32(unsafe.Sizeof(wndclass{})), Proc: syscall.NewCallback(wndProc), Instance: inst, Cursor: cursor, Icon: icon, SmallIcon: icon, Brush: 6, Class: class}
	if call("RegisterClassExW", ptr(&wc)) == 0 {
		return fmt.Errorf("Не удалось зарегистрировать окно")
	}
	width, height := call("GetSystemMetrics", 0), call("GetSystemMetrics", 1)
	if width > 1440 {
		width = 1440
	}
	if height > 1000 {
		height = 1000
	}
	if width > 80 {
		width -= 64
	}
	if height > 80 {
		height -= 80
	}
	hwnd = call("CreateWindowExW", 0, ptr(class), ptr(utf("Тихий мир — 5.6.0 | Живая симуляция")), 0x00CF0000, 0x80000000, 0x80000000, width, height, 0, 0, inst, 0)
	if hwnd == 0 {
		return fmt.Errorf("Не удалось открыть окно Windows")
	}
	defer func() {
		if controller != 0 {
			com(controller, 24)
			if view != 0 {
				com(view, 2)
			}
			com(controller, 2)
		}
	}()
	call("ShowWindow", hwnd, 5)
	call("UpdateWindow", hwnd)
	query, add, rel := syscall.NewCallback(qi), syscall.NewCallback(addref), syscall.NewCallback(release)
	vEnv = [4]uintptr{query, add, rel, syscall.NewCallback(envDone)}
	vCtl = [4]uintptr{query, add, rel, syscall.NewCallback(ctlDone)}
	vClose = [4]uintptr{query, add, rel, syscall.NewCallback(closeDone)}
	handlerEnv = comHandler{&vEnv, guid{0x4e8a3389, 0xc9d8, 0x4bd2, [8]byte{0xb6, 0xb5, 0x12, 0x4f, 0xee, 0x6c, 0xc1, 0x4d}}}
	handlerCtl = comHandler{&vCtl, guid{0x6c4819f3, 0xc9b7, 0x4260, [8]byte{0x81, 0x27, 0xc9, 0xf5, 0xbd, 0xe7, 0xf6, 0x8c}}}
	handlerClose = comHandler{&vClose, guid{0x49511172, 0xcc67, 0x4bca, [8]byte{0x99, 0x23, 0x13, 0x71, 0x12, 0xf4, 0xc4, 0xcc}}}
	// Use a standard loader if supplied next to the executable; otherwise use the documented runtime export.
	exe, _ := os.Executable()
	loader := filepath.Join(filepath.Dir(exe), "WebView2Loader.dll")
	var proc *syscall.Proc
	if _, e := os.Stat(loader); e == nil {
		if d, e := syscall.LoadDLL(loader); e == nil {
			if p, e := d.FindProc("CreateCoreWebView2EnvironmentWithOptions"); e == nil {
				webDLL = d
				proc = p
			}
		}
	}
	if proc != nil {
		r, _, _ = proc.Call(0, ptr(utf(profile)), 0, ptr(&handlerEnv))
	} else {
		path := findRuntime()
		if path == "" {
			call("DestroyWindow", hwnd)
			return fmt.Errorf("Не найден Microsoft Edge WebView2 Runtime (x64). Установите Evergreen Runtime с сайта Microsoft и запустите игру снова.")
		}
		d, e := loadRuntime(path)
		if e != nil {
			call("DestroyWindow", hwnd)
			return fmt.Errorf("Не удалось загрузить WebView2 Runtime: %v", e)
		}
		webDLL = d
		proc, e = d.FindProc("CreateWebViewEnvironmentWithOptionsInternal")
		if e != nil {
			call("DestroyWindow", hwnd)
			return fmt.Errorf("Несовместимая версия WebView2 Runtime: %v", e)
		}
		r, _, _ = proc.Call(1, 0, ptr(utf(profile)), 0, ptr(&handlerEnv))
	}
	if failed(r) {
		call("DestroyWindow", hwnd)
		return fmt.Errorf("WebView2 initialization: 0x%08X", uint32(r))
	}
	var m msg
	for {
		v := call("GetMessageW", ptr(&m), 0, 0, 0)
		if int32(v) <= 0 {
			break
		}
		call("TranslateMessage", ptr(&m))
		call("DispatchMessageW", ptr(&m))
	}
	return startupErr
}

// Resize the client area, without changing the monitor's system resolution.
func resizeDesktop(p Preferences) {
	if hwnd == 0 {
		return
	}
	if p.Mode == "max" {
		call("PostMessageW", hwnd, 0x8003, 0, 0)
	} else {
		call("PostMessageW", hwnd, 0x8003, uintptr(p.Width), uintptr(p.Height))
	}
}
func applyDesktopSize(w, h uintptr) {
	if w == 0 {
		call("ShowWindow", hwnd, 3)
		return
	}
	call("ShowWindow", hwnd, 9)
	r := rect{0, 0, int32(w), int32(h)}
	call("AdjustWindowRectEx", ptr(&r), 0x00CF0000, 0, 0)
	var area rect
	if call("SystemParametersInfoW", 48, 0, ptr(&area), 0) == 0 {
		area = rect{0, 0, int32(call("GetSystemMetrics", 0)), int32(call("GetSystemMetrics", 1))}
	}
	width, height := r.R-r.L, r.B-r.T
	if width > area.R-area.L {
		width = area.R - area.L
	}
	if height > area.B-area.T {
		height = area.B - area.T
	}
	x, y := area.L+(area.R-area.L-width)/2, area.T+(area.B-area.T-height)/2
	call("SetWindowPos", hwnd, 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0x14)
}
