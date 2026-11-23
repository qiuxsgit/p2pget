//go:build windows

package progress

import "golang.org/x/sys/windows"

func ConfigureConsole() {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	kernel.NewProc("SetConsoleOutputCP").Call(65001)
	kernel.NewProc("SetConsoleCP").Call(65001)
}

func enableProgressANSI(fd uintptr) bool {
	var mode uint32
	handle := windows.Handle(fd)
	if windows.GetConsoleMode(handle, &mode) != nil {
		return false
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
