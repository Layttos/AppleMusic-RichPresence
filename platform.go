package main

import "runtime"

// platformName is logged at startup so a bug report says which player is in
// use without the reporter having to know.
func platformName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS (AppleScript)"
	case "windows":
		return "Windows (System Media Transport Controls)"
	default:
		return runtime.GOOS
	}
}
