//go:build !windows

package progress

func ConfigureConsole() {}

func enableProgressANSI(fd uintptr) bool { return true }
