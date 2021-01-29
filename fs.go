package main

// Copied from https://gitlab.com/unnamed-os/build/gonani/-/blob/a18f0ed98d938a7dda974030323e02d94b4831fe/fs/fs.go
// by Simao

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func DirectoryExists(path string) bool {
	stat, err := os.Stat(path)
	if err == nil {
		return stat.IsDir()
	}
	return false
}

func FileExists(path string) bool {
	stat, err := os.Stat(path)
	if err == nil {
		return !stat.IsDir()
	}
	return false
}

func ReplaceExtension(filename string, ext string) string {
	return fmt.Sprintf("%s.%s", strings.TrimSuffix(filename, filepath.Ext(filename)), ext)
}

func RemoveExtension(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}
