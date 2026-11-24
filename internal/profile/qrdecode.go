package profile

// qrdecode.go — QR code support for subscription import/export (#102).
//
// v2rayN/Qv2ray/V2RayW all move nodes as QR images; NeuroRift now both
// renders share links as QR PNGs (frontend <img> via a data URL) and
// imports subscriptions by decoding QR images (a picked file, or the whole
// screen on Windows).
//
// Both sides use pure-Go libraries — no cgo, cross-compilation intact:
//   - encode: github.com/skip2/go-qrcode (MIT)
//   - decode: github.com/makiuchi-d/gozxing (ZXing core port, LGPL-3.0)

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg" // decode JPEG QR images
	_ "image/png"  // decode PNG QR images
	"os"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	qrcodeenc "github.com/skip2/go-qrcode"
)

// encodeQR renders text as a QR PNG (512px, medium error correction —
// enough for share links while staying decodable from screenshots).
func encodeQR(text string) ([]byte, error) {
	return qrcodeenc.Encode(text, qrcodeenc.Medium, 512)
}

// DecodeQR returns the text of the first QR code found in img.
func DecodeQR(img image.Image) (string, error) {
	if img == nil {
		return "", fmt.Errorf("no image")
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("qr: %w", err)
	}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
	if err != nil {
		return "", fmt.Errorf("no QR code in image: %w", err)
	}
	return res.GetText(), nil
}

// QRDataURL renders text as a QR code PNG packed into a data: URL for
// direct <img src> binding. Empty when text is empty or exceeds QR
// capacity (links are far below the ~2.9 KiB limit).
func QRDataURL(text string) string {
	if text == "" {
		return ""
	}
	png, err := encodeQR(text)
	if err != nil {
		return "" // payload too long or not encodable
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// DecodeQRImage reads the image at path and returns the text of the first
// QR code found in it.
func DecodeQRImage(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open image: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode image %s: %w", path, err)
	}
	return DecodeQR(img)
}

// IsImageFile reports whether data starts with a PNG or JPEG magic number.
func IsImageFile(data []byte) bool {
	return bytes.HasPrefix(data, []byte("\x89PNG")) ||
		bytes.HasPrefix(data, []byte("\xff\xd8\xff"))
}
