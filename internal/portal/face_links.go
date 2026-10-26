package portal

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
)

func faceToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func checkFaceToken(hash, secret string) bool {
	return len(secret) == 43 && subtle.ConstantTimeCompare([]byte(hash), []byte(digest(secret))) == 1
}
func cleanPortrait(data []byte) ([]byte, error) {
	config, _, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width < 32 || config.Height < 32 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, fmt.Errorf("请使用 32–8192 像素且不超过 1600 万像素的 JPEG/PNG 图片")
	}
	img, _, e := image.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	var out bytes.Buffer
	if e = jpeg.Encode(&out, img, &jpeg.Options{Quality: 92}); e != nil {
		return nil, e
	}
	if out.Len() > 6<<20 {
		return nil, fmt.Errorf("照片处理后超过 6 MB")
	}
	return out.Bytes(), nil
}
