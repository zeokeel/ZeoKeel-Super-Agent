// Command genicon draws the ZeoKeel mark at every size the desktop platforms need
// and packs it into .ico (Windows) and .icns (macOS).
//
// It draws with the standard library instead of rasterizing the SVG: adding an SVG
// engine to a zero-cgo build to produce six icons is a bad trade, and a geometric
// mark is something the build can own outright.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// palette: the same blue as the panel's accent, on a near-white plate.
var (
	plate   = color.NRGBA{0xF5, 0xF7, 0xFB, 0xFF}
	ink     = color.NRGBA{0x2F, 0x6B, 0xED, 0xFF}
	inkDeep = color.NRGBA{0x1E, 0x3F, 0x8A, 0xFF}
)

func main() {
	out := "packaging/icons"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		die(err)
	}
	sizes := []int{16, 32, 48, 64, 128, 256, 512, 1024}
	pngs := map[int][]byte{}
	for _, s := range sizes {
		img := draw(s)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			die(err)
		}
		pngs[s] = buf.Bytes()
		name := filepath.Join(out, fmt.Sprintf("zeokeel-%d.png", s))
		if err := os.WriteFile(name, buf.Bytes(), 0o644); err != nil {
			die(err)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "zeokeel.ico"),
		ico(pngs, []int{16, 32, 48, 64, 128, 256}), 0o644); err != nil {
		die(err)
	}
	if err := os.WriteFile(filepath.Join(out, "zeokeel.icns"),
		icns(pngs), 0o644); err != nil {
		die(err)
	}
	fmt.Println("icons:", out)
}

// draw renders one square icon: a rounded plate, a "Z" of two strokes, and a small
// dot for the agent's "listening" state.
func draw(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	f := float64(size)
	radius := f * 0.22
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if !inRounded(px, py, f, radius) {
				continue
			}
			img.SetNRGBA(x, y, plate)
		}
	}
	// The Z: top bar, diagonal, bottom bar. Coordinates are fractions of the box,
	// so every size keeps the same shape.
	thick := f * 0.11
	left, right := f*0.26, f*0.74
	top, bottom := f*0.29, f*0.71
	bar(img, left, top, right, top+thick, ink)
	bar(img, left, bottom-thick, right, bottom, ink)
	// Diagonal as a series of discs: no anti-aliasing library needed.
	steps := int(f * 2)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		// A Z reads top-right to bottom-left; going the other way draws an S.
		cx := right - (right-left)*t
		cy := top + thick + (bottom-top-2*thick)*t
		disc(img, cx, cy, thick*0.55, ink)
	}
	// A small dot in the lower-right corner says "this thing watches for work";
	// it sits clear of the Z so the letter stays readable at 16px.
	disc(img, f*0.78, f*0.78, f*0.07, inkDeep)
	return img
}

func inRounded(x, y, size, radius float64) bool {
	if x < radius && y < radius {
		return dist(x-radius, y-radius) <= radius
	}
	if x > size-radius && y < radius {
		return dist(x-(size-radius), y-radius) <= radius
	}
	if x < radius && y > size-radius {
		return dist(x-radius, y-(size-radius)) <= radius
	}
	if x > size-radius && y > size-radius {
		return dist(x-(size-radius), y-(size-radius)) <= radius
	}
	return true
}

// dist 用 stdlib math.Hypot，而不是手写 30 次牛顿迭代的 sqrt：
// 这是 (a) stdlib 已有的能力，手写版本还慢且多 20 行（audit F8）。
func dist(x, y float64) float64 { return math.Hypot(x, y) }

func bar(img *image.NRGBA, x0, y0, x1, y1 float64, c color.NRGBA) {
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			if image.Pt(x, y).In(img.Rect) {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

func disc(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			if !image.Pt(x, y).In(img.Rect) {
				continue
			}
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dist(dx, dy) <= r {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

// ico writes a Windows icon with PNG-compressed entries (Vista and later).
func ico(pngs map[int][]byte, sizes []int) []byte {
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // type: icon
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(sizes)))
	offset := 6 + 16*len(sizes)
	for _, s := range sizes {
		data := pngs[s]
		b := byte(s)
		if s >= 256 {
			b = 0 // 256 is encoded as 0
		}
		out.Write([]byte{b, b, 0, 0})
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
		_ = binary.Write(&out, binary.LittleEndian, uint16(32)) // bpp
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, s := range sizes {
		out.Write(pngs[s])
	}
	return out.Bytes()
}

// icns writes an Apple icon container. Modern macOS accepts PNG payloads in the
// ic07/ic08/ic09/ic10 slots, which is all we need.
func icns(pngs map[int][]byte) []byte {
	type entry struct {
		kind string
		size int
	}
	entries := []entry{
		{"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
		{"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
	}
	var body bytes.Buffer
	for _, e := range entries {
		data, ok := pngs[e.size]
		if !ok {
			continue
		}
		body.WriteString(e.kind)
		_ = binary.Write(&body, binary.BigEndian, uint32(len(data)+8))
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(body.Len()+8))
	out.Write(body.Bytes())
	return out.Bytes()
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "genicon:", err)
	os.Exit(1)
}
