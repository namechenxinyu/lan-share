package qrcode

import (
	"bytes"
	"image/png"
	"testing"
)

func TestEncodePNG(t *testing.T) {
	b, err := EncodePNG("http://192.168.3.161:51888/s/abcdefghijklmnop", 6)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("not a PNG")
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != (37+8)*6 {
		t.Fatalf("unexpected QR image size: %v", img.Bounds())
	}
}

func TestTooLong(t *testing.T) {
	b := make([]byte, 107)
	if _, err := EncodePNG(string(b), 4); err == nil {
		t.Fatal("expected payload length error")
	}
}
