package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func startUpdater(helper, self, next, version string) error {
	c := exec.Command(helper, "--apply-update", strconv.Itoa(os.Getpid()), self, next, version)
	c.Dir = filepath.Dir(self)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if e := c.Start(); e != nil {
		return e
	}
	return c.Process.Release()
}
func requestUpdateExit() {
	if hwnd != 0 {
		call("PostMessageW", hwnd, 0x8002, 0, 0)
	} else {
		os.Exit(0)
	}
}
func applyUpdate(args []string) {
	if len(args) != 4 {
		return
	}
	pid, e := strconv.Atoi(args[0])
	if e != nil || pid <= 0 {
		return
	}
	target, next, version := args[1], args[2], args[3]
	self, e := os.Executable()
	if e != nil {
		return
	}
	if filepath.Dir(self) != filepath.Dir(next) || filepath.Dir(filepath.Dir(self)) != filepath.Dir(target) || filepath.Base(target) != "QuietWorld.exe" || !newer(version, appVersion) {
		showError("Неверные параметры обновления.")
		return
	}
	h, _, _ := kernel.NewProc("OpenProcess").Call(0x00100000, 0, uintptr(pid))
	if h != 0 {
		r, _, _ := kernel.NewProc("WaitForSingleObject").Call(h, 30000)
		kernel.NewProc("CloseHandle").Call(h)
		if r != 0 {
			showError("Игра не завершилась. Обновление отменено; исходные файлы не изменены.")
			return
		}
	}
	backup := target + ".previous"
	if e = os.Remove(backup); e != nil && !os.IsNotExist(e) {
		showError("Не удалось убрать старую резервную копию: " + e.Error())
		return
	}
	// The window is closed and the old executable should be unlocked now.
	for i := 0; i < 50; i++ {
		e = os.Rename(target, backup)
		if e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e != nil {
		showError("Не удалось заменить игру: " + e.Error())
		_ = exec.Command(target).Start()
		return
	}
	if e = os.Rename(next, target); e != nil {
		_ = os.Rename(backup, target)
		showError("Обновление отменено: " + e.Error())
		_ = exec.Command(target).Start()
		return
	}
	c := exec.Command(target)
	c.Dir = filepath.Dir(target)
	if e = c.Start(); e != nil {
		_ = os.Remove(target)
		if er := os.Rename(backup, target); er != nil {
			showError(fmt.Sprintf("Не удалось восстановить игру. Резервная копия: %s. %v", backup, er))
			return
		}
		showError("Не удалось запустить обновление. Восстановлена предыдущая версия.")
		_ = exec.Command(target).Start()
		return
	}
	_ = c.Process.Release()
	// Update Windows' installed-app version (failure here does not affect the game).
	r := exec.Command("reg.exe", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\QuietWorld`, "/v", "DisplayVersion", "/t", "REG_SZ", "/d", version, "/f")
	r.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	_ = r.Run()
}
