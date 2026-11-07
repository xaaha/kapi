package main

import (
	"encoding/json"
	"runtime"
)

var workDir = func() string {
	if runtime.GOOS == "windows" {
		return `C:\w`
	}
	return "/w"
}()

var workDirJSON = func() string {
	b, _ := json.Marshal(workDir)
	return string(b)
}()

var shortTempRoot = func() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "/tmp"
}()
