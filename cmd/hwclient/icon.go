package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image/png"
	"runtime"
)

// The tray icons. Replace the files to rebrand; nothing else needs changing.
//   - icon.png: the regular icon (Windows tray, Linux). Square; 256 px works well.
//   - icon-template.png: macOS menu bar. Black on transparent: macOS uses
//     only its shape and recolors it for light and dark menu bars.
var (
	//go:embed icon.png
	regularIcon []byte
	//go:embed icon-template.png
	templateIcon []byte
)

// icoMaxSide is the largest side an ICO entry can describe; 0 in the entry means 256.
const icoMaxSide = 256

// trayIcon is the regular icon in the format the platform's tray wants:
// Windows only loads .ico, everything else takes the PNG as is.
func trayIcon() []byte {
	if runtime.GOOS != "windows" {
		return regularIcon
	}
	return pngToICO(regularIcon)
}

// pngToICO wraps a PNG in a one-entry ICO file. Windows Vista and later read
// PNG data inside ICO files, so no pixel conversion is needed.
func pngToICO(data []byte) []byte {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		panic("icon.png is not a PNG: " + err.Error()) // Embedded at build time.
	}
	side := func(pixels int) uint8 {
		if pixels >= icoMaxSide {
			return 0
		}
		return uint8(pixels)
	}
	const headerSize, entrySize = 6, 16
	var out bytes.Buffer
	// ICONDIR: reserved, type 1 (icon), one image.
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, 1})
	// ICONDIRENTRY: width, height, palette size, reserved, planes, bits per
	// pixel, data size, data offset.
	out.Write([]byte{side(config.Width), side(config.Height), 0, 0})
	binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
	binary.Write(&out, binary.LittleEndian, [2]uint32{uint32(len(data)), headerSize + entrySize})
	out.Write(data)
	return out.Bytes()
}
