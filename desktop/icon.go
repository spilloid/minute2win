package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
)

// Draw the product's stopwatch as a small native icon; no downloaded artwork.
func statusIcon(kind string) []byte {
	colors := map[string]color.NRGBA{
		"offline": {100, 110, 120, 255}, "idle": {35, 145, 80, 255},
		"armed": {190, 125, 0, 255}, "deploying": {11, 87, 208, 255}, "degraded": {195, 40, 40, 255},
	}
	im := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	bg := colors[kind]
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			if x >= 2 && x < 30 && y >= 2 && y < 30 {
				im.SetNRGBA(x, y, bg)
			}
			d := math.Hypot(float64(x)-15.5, float64(y)-17)
			if (d >= 8 && d <= 10) || (x >= 12 && x <= 19 && y >= 5 && y <= 6) || (x >= 15 && x <= 16 && y >= 10 && y <= 18) || (x >= 16 && x <= 21 && y >= 17 && y <= 19) {
				im.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	if runtime.GOOS != "windows" {
		return b.Bytes()
	}
	// ICO directory with a PNG payload, supported by the Windows native loader.
	var ico bytes.Buffer
	for _, value := range []uint16{0, 1, 1} {
		_ = binary.Write(&ico, binary.LittleEndian, value)
	}
	ico.Write([]byte{32, 32, 0, 0})
	for _, value := range []uint16{1, 32} {
		_ = binary.Write(&ico, binary.LittleEndian, value)
	}
	_ = binary.Write(&ico, binary.LittleEndian, uint32(b.Len()))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(b.Bytes())
	return ico.Bytes()
}
