package qrcode

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
)

// EncodePNG creates a Version 5-L QR code (byte mode) using only the Go standard library.
// Version 5-L can hold up to 106 bytes in byte mode, which is sufficient for LAN Share URLs.
func EncodePNG(text string, scale int) ([]byte, error) {
	data := []byte(text)
	if len(data) > 106 {
		return nil, errors.New("QR payload too long (max 106 bytes)")
	}
	if scale < 2 {
		scale = 2
	}
	matrix, err := encodeV5L(data)
	if err != nil {
		return nil, err
	}
	const border = 4
	n := len(matrix)
	size := (n + border*2) * scale
	img := image.NewGray(image.Rect(0, 0, size, size))
	white := color.Gray{Y: 255}
	black := color.Gray{Y: 0}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetGray(x, y, white)
		}
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if !matrix[y][x] {
				continue
			}
			x0 := (x + border) * scale
			y0 := (y + border) * scale
			for yy := y0; yy < y0+scale; yy++ {
				for xx := x0; xx < x0+scale; xx++ {
					img.SetGray(xx, yy, black)
				}
			}
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func encodeV5L(data []byte) ([][]bool, error) {
	const (
		size          = 37
		dataCodewords = 108
		eccCodewords  = 26
	)
	bits := make([]bool, 0, dataCodewords*8)
	appendBits := func(val uint, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, ((val>>uint(i))&1) != 0)
		}
	}
	appendBits(0x4, 4) // byte mode
	appendBits(uint(len(data)), 8)
	for _, b := range data {
		appendBits(uint(b), 8)
	}
	capBits := dataCodewords * 8
	terminator := 4
	if capBits-len(bits) < terminator {
		terminator = capBits - len(bits)
	}
	appendBits(0, terminator)
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	code := make([]byte, 0, dataCodewords+eccCodewords)
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8; j++ {
			b <<= 1
			if bits[i+j] {
				b |= 1
			}
		}
		code = append(code, b)
	}
	pads := []byte{0xEC, 0x11}
	for i := 0; len(code) < dataCodewords; i++ {
		code = append(code, pads[i&1])
	}
	ecc := reedSolomon(code, eccCodewords)
	code = append(code, ecc...)

	m := make([][]bool, size)
	fn := make([][]bool, size)
	for y := 0; y < size; y++ {
		m[y] = make([]bool, size)
		fn[y] = make([]bool, size)
	}
	setFunc := func(x, y int, dark bool) {
		if x >= 0 && x < size && y >= 0 && y < size {
			m[y][x] = dark
			fn[y][x] = true
		}
	}
	drawFinder := func(cx, cy int) {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := cx+dx, cy+dy
				if x < 0 || x >= size || y < 0 || y >= size {
					continue
				}
				dist := abs(dx)
				if abs(dy) > dist {
					dist = abs(dy)
				}
				setFunc(x, y, dist != 2 && dist != 4)
			}
		}
	}
	drawAlignment := func(cx, cy int) {
		for dy := -2; dy <= 2; dy++ {
			for dx := -2; dx <= 2; dx++ {
				dist := abs(dx)
				if abs(dy) > dist {
					dist = abs(dy)
				}
				setFunc(cx+dx, cy+dy, dist != 1)
			}
		}
	}
	drawFinder(3, 3)
	drawFinder(size-4, 3)
	drawFinder(3, size-4)
	for i := 0; i < size; i++ {
		if !fn[6][i] {
			setFunc(i, 6, i%2 == 0)
		}
		if !fn[i][6] {
			setFunc(6, i, i%2 == 0)
		}
	}
	centers := []int{6, 30}
	for _, cy := range centers {
		for _, cx := range centers {
			if !fn[cy][cx] {
				drawAlignment(cx, cy)
			}
		}
	}

	// Fixed mask 0; this is valid QR and keeps the implementation compact.
	drawFormat := func(mask int) {
		data5 := (1 << 3) | mask // ECL L = 01
		rem := data5
		for i := 0; i < 10; i++ {
			rem = (rem << 1) ^ ((rem >> 9) * 0x537)
		}
		format := ((data5 << 10) | rem) ^ 0x5412
		bit := func(i int) bool { return ((format >> uint(i)) & 1) != 0 }
		for i := 0; i <= 5; i++ {
			setFunc(8, i, bit(i))
		}
		setFunc(8, 7, bit(6))
		setFunc(8, 8, bit(7))
		setFunc(7, 8, bit(8))
		for i := 9; i < 15; i++ {
			setFunc(14-i, 8, bit(i))
		}
		for i := 0; i < 8; i++ {
			setFunc(size-1-i, 8, bit(i))
		}
		for i := 8; i < 15; i++ {
			setFunc(8, size-15+i, bit(i))
		}
		setFunc(8, size-8, true)
	}
	drawFormat(0)

	bitIndex := 0
	for right := size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		upward := ((right + 1) & 2) == 0
		for vert := 0; vert < size; vert++ {
			y := vert
			if upward {
				y = size - 1 - vert
			}
			for j := 0; j < 2; j++ {
				x := right - j
				if fn[y][x] {
					continue
				}
				dark := false
				if bitIndex < len(code)*8 {
					dark = ((code[bitIndex>>3] >> uint(7-(bitIndex&7))) & 1) != 0
					bitIndex++
				}
				if (x+y)%2 == 0 { // mask pattern 0
					dark = !dark
				}
				m[y][x] = dark
			}
		}
	}
	return m, nil
}

func reedSolomon(data []byte, degree int) []byte {
	gen := []byte{1}
	for i := 0; i < degree; i++ {
		gen = polyMul(gen, []byte{1, gfPow(byte(i))})
	}
	msg := make([]byte, len(data)+degree)
	copy(msg, data)
	for i := 0; i < len(data); i++ {
		factor := msg[i]
		if factor == 0 {
			continue
		}
		for j := 0; j < len(gen); j++ {
			msg[i+j] ^= gfMul(gen[j], factor)
		}
	}
	return append([]byte(nil), msg[len(data):]...)
}

func polyMul(a, b []byte) []byte {
	out := make([]byte, len(a)+len(b)-1)
	for i, x := range a {
		for j, y := range b {
			out[i+j] ^= gfMul(x, y)
		}
	}
	return out
}

var gfExp [512]byte
var gfLog [256]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[byte(x)] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < len(gfExp); i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfPow(exp byte) byte { return gfExp[int(exp)] }
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
