package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestPNGToICOWrapsTheEmbeddedIcon(t *testing.T) {
	ico := pngToICO(regularIcon)
	var header [3]uint16
	binary.Read(bytes.NewReader(ico), binary.LittleEndian, &header)
	if header != [3]uint16{0, 1, 1} {
		t.Fatalf("ICONDIR = %v, want reserved 0, type 1, one image", header)
	}
	size := binary.LittleEndian.Uint32(ico[14:18])
	offset := binary.LittleEndian.Uint32(ico[18:22])
	if int(size) != len(regularIcon) || offset != 22 || !bytes.Equal(ico[offset:], regularIcon) {
		t.Errorf("entry size %d offset %d does not point at the PNG", size, offset)
	}
}
