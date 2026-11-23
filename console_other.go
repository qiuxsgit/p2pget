//go:build !windows

package main

func configureConsole() {}

func enableProgressANSI(fd uintptr) bool { return true }
