package main

import (
	"image"
	"io"
	"runtime"

	_ "image/jpeg"
	_ "image/png"
)

func imageDecode(r io.Reader) (image.Image, string, error) {
	return image.Decode(r)
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}

func isDarwin() bool {
	return runtime.GOOS == "darwin"
}
