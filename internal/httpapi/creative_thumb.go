package httpapi

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"strconv"
	"sync"
)

type thumbCacheEntry struct {
	mime string
	data []byte
	etag string
}

var (
	thumbMu    sync.RWMutex
	thumbCache = make(map[string]thumbCacheEntry, 256)
	thumbOrder = make([]string, 0, 256)
)

const maxThumbEntries = 512

func parseRequestedWidth(raw string) int {
	if raw == "" {
		return 0
	}
	w, err := strconv.Atoi(raw)
	if err != nil || w <= 0 {
		return 0
	}
	if w < 120 {
		return 120
	}
	if w > 1200 {
		return 1200
	}
	return w
}

// buildThumbnailJPEG scales src image down to maxWidth using bilinear interpolation
// and encodes as JPEG (q=82). Cached in memory keyed by sha256 + width.
func buildThumbnailJPEG(sha256Hex, origMime string, origData []byte, maxWidth int) (mime string, data []byte, etag string) {
	key := sha256Hex + ":w" + strconv.Itoa(maxWidth)
	thumbMu.RLock()
	if hit, ok := thumbCache[key]; ok {
		thumbMu.RUnlock()
		return hit.mime, hit.data, hit.etag
	}
	thumbMu.RUnlock()

	img, _, err := image.Decode(bytes.NewReader(origData))
	if err != nil {
		return origMime, origData, `"` + sha256Hex + `"`
	}
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return origMime, origData, `"` + sha256Hex + `"`
	}

	var target image.Image = img
	if srcW > maxWidth {
		dstW := maxWidth
		dstH := int(float64(srcH) * float64(dstW) / float64(srcW))
		if dstH < 1 {
			dstH = 1
		}
		target = resizeBilinear(img, dstW, dstH)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, target, &jpeg.Options{Quality: 82}); err != nil || buf.Len() == 0 {
		return origMime, origData, `"` + sha256Hex + `"`
	}
	outBytes := buf.Bytes()
	// If original was already smaller JPEG, keep original
	if origMime == "image/jpeg" && len(origData) <= len(outBytes) {
		outBytes = origData
	}
	entry := thumbCacheEntry{
		mime: "image/jpeg",
		data: outBytes,
		etag: `"` + sha256Hex + `-w` + strconv.Itoa(maxWidth) + `"`,
	}

	thumbMu.Lock()
	if _, exists := thumbCache[key]; !exists {
		if len(thumbOrder) >= maxThumbEntries {
			evict := thumbOrder[0]
			thumbOrder = thumbOrder[1:]
			delete(thumbCache, evict)
		}
		thumbOrder = append(thumbOrder, key)
	}
	thumbCache[key] = entry
	thumbMu.Unlock()

	return entry.mime, entry.data, entry.etag
}

func resizeBilinear(src image.Image, dstW, dstH int) *image.RGBA {
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	xRatio := float64(srcW) / float64(dstW)
	yRatio := float64(srcH) / float64(dstH)

	for y := 0; y < dstH; y++ {
		sy := (float64(y)+0.5)*yRatio - 0.5
		y0 := int(sy)
		if y0 < 0 {
			y0 = 0
		}
		y1 := y0 + 1
		if y1 >= srcH {
			y1 = srcH - 1
		}
		wy := sy - float64(y0)
		if wy < 0 {
			wy = 0
		}
		for x := 0; x < dstW; x++ {
			sx := (float64(x)+0.5)*xRatio - 0.5
			x0 := int(sx)
			if x0 < 0 {
				x0 = 0
			}
			x1 := x0 + 1
			if x1 >= srcW {
				x1 = srcW - 1
			}
			wx := sx - float64(x0)
			if wx < 0 {
				wx = 0
			}

			r00, g00, b00, a00 := src.At(sb.Min.X+x0, sb.Min.Y+y0).RGBA()
			r10, g10, b10, a10 := src.At(sb.Min.X+x1, sb.Min.Y+y0).RGBA()
			r01, g01, b01, a01 := src.At(sb.Min.X+x0, sb.Min.Y+y1).RGBA()
			r11, g11, b11, a11 := src.At(sb.Min.X+x1, sb.Min.Y+y1).RGBA()

			w00 := (1 - wx) * (1 - wy)
			w10 := wx * (1 - wy)
			w01 := (1 - wx) * wy
			w11 := wx * wy

			r := uint8((float64(r00)*w00 + float64(r10)*w10 + float64(r01)*w01 + float64(r11)*w11) / 257.0)
			g := uint8((float64(g00)*w00 + float64(g10)*w10 + float64(g01)*w01 + float64(g11)*w11) / 257.0)
			bl := uint8((float64(b00)*w00 + float64(b10)*w10 + float64(b01)*w01 + float64(b11)*w11) / 257.0)
			a := uint8((float64(a00)*w00 + float64(a10)*w10 + float64(a01)*w01 + float64(a11)*w11) / 257.0)
			dst.SetRGBA(x, y, color.RGBA{R: r, G: g, B: bl, A: a})
		}
	}
	return dst
}
