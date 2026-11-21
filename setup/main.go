package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

//go:embed QuietWorld.exe
var game []byte
var msgbox = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

func message(text string, flags uintptr) uintptr {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString("Тихий мир — Setup")
	r, _, _ := msgbox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags)
	return r
}
func hidden(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return c.Run()
}
func ps(s string) error {
	return hidden("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", s)
}
func q(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func main() {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		message("Не найден каталог LOCALAPPDATA.", 16)
		return
	}
	dir := filepath.Join(base, "Programs", "QuietWorld")
	exe := filepath.Join(dir, "QuietWorld.exe")
	un := filepath.Join(dir, "Uninstall.exe")
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\QuietWorld`
	if len(os.Args) > 1 && os.Args[1] == "--uninstall" {
		if message("Удалить игру? Сохранённый мир останется на компьютере.", 4|32) != 6 {
			return
		}
		if e := os.Remove(exe); e != nil && !os.IsNotExist(e) {
			message("Закройте игру и повторите удаление. При необходимости удалите папку вручную: "+dir, 48)
			return
		}
		_ = ps(`$d=[Environment]::GetFolderPath('Desktop');$s=[Environment]::GetFolderPath('Programs'); Remove-Item -LiteralPath (Join-Path $d 'Тихий мир.lnk'),(Join-Path $s 'Тихий мир.lnk') -Force -ErrorAction SilentlyContinue`)
		_ = hidden("reg.exe", "delete", key, "/f")
		message("Игра удалена. Сохранение оставлено.", 64)
		c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `Start-Sleep -Seconds 2; Remove-Item -LiteralPath `+q(un)+` -Force -ErrorAction SilentlyContinue; if (@(Get-ChildItem -LiteralPath `+q(dir)+` -Force).Count -eq 0) { Remove-Item -LiteralPath `+q(dir)+` -Force -ErrorAction SilentlyContinue }`)
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = c.Start()
		return
	}
	if message("Установить или обновить игру «Тихий мир 5.8»?\n\nАвтономная симуляция цивилизаций.\nПапка: "+dir+"\n\nОтдельное окно Windows. Требуется Microsoft Edge WebView2 Runtime (обычно уже установлен).", 4|32) != 6 {
		return
	}
	fail := func(e error) {
		message("Не удалось установить: "+e.Error()+"\nЕсли игра запущена, закройте её и повторите.", 16)
	}
	if e := os.MkdirAll(dir, 0755); e != nil {
		fail(e)
		return
	}
	tmp := exe + ".new"
	if e := os.WriteFile(tmp, game, 0755); e != nil {
		fail(e)
		return
	}
	if e := os.Rename(tmp, exe); e != nil {
		_ = os.Remove(tmp)
		fail(e)
		return
	}
	self, e := os.Executable()
	if e != nil {
		fail(e)
		return
	}
	b, e := os.ReadFile(self)
	if e != nil {
		fail(e)
		return
	}
	if !strings.EqualFold(self, un) {
		if e = os.WriteFile(un, b, 0755); e != nil {
			fail(e)
			return
		}
	}
	script := `$ErrorActionPreference='Stop'; $w=New-Object -ComObject WScript.Shell; foreach($p in @([Environment]::GetFolderPath('Desktop'),[Environment]::GetFolderPath('Programs'))){if($p){$l=$w.CreateShortcut((Join-Path $p 'Тихий мир.lnk'));$l.TargetPath=` + q(exe) + `;$l.WorkingDirectory=` + q(dir) + `;$l.Description='Автономная симуляция мира';$l.Save()}}`
	shortcutErr := ps(script)
	regOK := true
	for n, v := range map[string]string{"DisplayName": "Тихий мир / QuietWorld", "DisplayVersion": "7.0.0", "Publisher": "QuietWorld", "InstallLocation": dir, "DisplayIcon": exe, "UninstallString": fmt.Sprintf(`"%s" --uninstall`, un)} {
		if hidden("reg.exe", "add", key, "/v", n, "/t", "REG_SZ", "/d", v, "/f") != nil {
			regOK = false
		}
	}
	note := ""
	if shortcutErr != nil {
		note += "\nНе удалось создать ярлыки. Запуск: " + exe
	}
	if !regOK {
		note += "\nЗапись в списке приложений создана не полностью."
	}
	if message("Установка завершена."+note+"\n\nЗапустить игру?", 4|64) == 6 {
		if e := exec.Command(exe).Start(); e != nil {
			fail(e)
		}
	}
}
