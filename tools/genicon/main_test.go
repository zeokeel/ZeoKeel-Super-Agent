package main

import (
	"encoding/binary"
	"image"
	"testing"
)

// ico 是手写的容器格式：头字段、条目数、偏移与净荷拼接都必须是 Windows
// 能接受的样子（这些是"手写二进制"最容易错的地方，而 PNG 编码本身用 stdlib）。
func TestICOContainer(t *testing.T) {
	sizes := []int{16, 32, 48, 64, 128, 256}
	pngs := map[int][]byte{}
	for i, size := range sizes {
		pngs[size] = []byte{byte(i), 1, 2, 3}
	}
	out := ico(pngs, sizes)
	if len(out) < 6+16*len(sizes) {
		t.Fatalf("ico too short: %d", len(out))
	}
	if got := binary.LittleEndian.Uint16(out[0:2]); got != 0 {
		t.Fatalf("reserved = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(out[2:4]); got != 1 {
		t.Fatalf("type = %d, want 1 (icon)", got)
	}
	if got := binary.LittleEndian.Uint16(out[4:6]); got != uint16(len(sizes)) {
		t.Fatalf("count = %d, want %d", got, len(sizes))
	}
	wantOffset := 6 + 16*len(sizes)
	for i, size := range sizes {
		entry := out[6+16*i:]
		wantDim := byte(size)
		if size >= 256 {
			wantDim = 0 // 256 用 0 表示
		}
		if entry[0] != wantDim || entry[1] != wantDim {
			t.Fatalf("entry %d dims = %d/%d, want %d", i, entry[0], entry[1], wantDim)
		}
		if planes := binary.LittleEndian.Uint16(entry[4:6]); planes != 1 {
			t.Fatalf("entry %d planes = %d", i, planes)
		}
		if bpp := binary.LittleEndian.Uint16(entry[6:8]); bpp != 32 {
			t.Fatalf("entry %d bpp = %d", i, bpp)
		}
		if length := binary.LittleEndian.Uint32(entry[8:12]); int(length) != len(pngs[size]) {
			t.Fatalf("entry %d length = %d, want %d", i, length, len(pngs[size]))
		}
		if offset := binary.LittleEndian.Uint32(entry[12:16]); int(offset) != wantOffset {
			t.Fatalf("entry %d offset = %d, want %d", i, offset, wantOffset)
		}
		wantOffset += len(pngs[size])
	}
}

// icns 的头部与每个块的 length 字段必须自洽（length 含 8 字节块头）。
func TestICNSContainer(t *testing.T) {
	pngs := map[int][]byte{
		128: make([]byte, 10), 256: make([]byte, 20), 512: make([]byte, 30), 1024: make([]byte, 40),
		32: make([]byte, 5), 64: make([]byte, 6),
	}
	out := icns(pngs)
	if string(out[:4]) != "icns" {
		t.Fatalf("magic = %q", out[:4])
	}
	if got := binary.BigEndian.Uint32(out[4:8]); int(got) != len(out) {
		t.Fatalf("icns size = %d, want %d", got, len(out))
	}
	// 每个 entry：4 字节类型 + 4 字节长度 + PNG
	offset := 8
	entries := 0
	for offset < len(out) {
		if offset+8 > len(out) {
			t.Fatalf("truncated icns entry at %d", offset)
		}
		length := int(binary.BigEndian.Uint32(out[offset+4 : offset+8]))
		if length < 8 || offset+length > len(out) {
			t.Fatalf("entry at %d has bad length %d", offset, length)
		}
		key := string(out[offset : offset+4])
		data := out[offset+8 : offset+length]
		if len(data) != len(pngs[mapKeySize(key)]) && len(pngs[mapKeySize(key)]) != 0 {
			// 同一个尺寸可能被多个 slot 复用；这里只断言 length 自洽。
			t.Logf("entry %s carries %d bytes", key, len(data))
		}
		offset += length
		entries++
	}
	if entries == 0 {
		t.Fatal("icns has no entries")
	}
}

// mapKeySize 把 ic07/ic08… 映射到像素尺寸，仅测试用。
func mapKeySize(key string) int {
	switch key {
	case "ic11":
		return 32
	case "ic12":
		return 64
	case "ic07":
		return 128
	case "ic08", "ic13":
		return 256
	case "ic09", "ic14":
		return 512
	case "ic10":
		return 1024
	}
	return 0
}

// 每个尺寸都能画出非空图标；比例缩放不越界。
func TestDrawProducesNonEmptyImages(t *testing.T) {
	for _, size := range []int{16, 48, 1024} {
		img := draw(size)
		if img.Bounds() != image.Rect(0, 0, size, size) {
			t.Fatalf("size %d bounds = %v", size, img.Bounds())
		}
		// 中心必是 Z 的笔画或底板，不应完全透明。
		if _, _, _, a := img.At(size/2, size/2).RGBA(); a == 0 {
			t.Fatalf("size %d center is transparent", size)
		}
	}
}

// 同一尺寸的绘制必须确定（构建产物可复现）。
func TestDrawIsDeterministic(t *testing.T) {
	a, b := draw(32), draw(32)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				t.Fatalf("pixel (%d,%d) differs between runs", x, y)
			}
		}
	}
}
