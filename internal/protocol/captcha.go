package protocol

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
)

type grayImage struct {
	w, h int
	pix  []float64
}

func decodeGray(data []byte, cropAlpha bool) (grayImage, error) {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return grayImage{}, err
	}
	bounds := decoded.Bounds()
	minX, minY, maxX, maxY := bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y
	if cropAlpha {
		found := false
		ax0, ay0, ax1, ay1 := maxX, maxY, minX, minY
		for y := minY; y < maxY; y++ {
			for x := minX; x < maxX; x++ {
				_, _, _, alpha := decoded.At(x, y).RGBA()
				if alpha == 0 {
					continue
				}
				found = true
				if x < ax0 {
					ax0 = x
				}
				if y < ay0 {
					ay0 = y
				}
				if x+1 > ax1 {
					ax1 = x + 1
				}
				if y+1 > ay1 {
					ay1 = y + 1
				}
			}
		}
		if found {
			minX, minY, maxX, maxY = ax0, ay0, ax1, ay1
		}
	}
	w, h := maxX-minX, maxY-minY
	if w < 2 || h < 2 {
		return grayImage{}, errors.New("captcha image dimensions are invalid")
	}
	output := grayImage{w: w, h: h, pix: make([]float64, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := decoded.At(minX+x, minY+y).RGBA()
			output.pix[y*w+x] = 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
		}
	}
	return output, nil
}

// SolveSliderNotch uses normalized grayscale cross-correlation over the full
// challenge image. It is a native Go implementation and has no Python/OpenCV
// runtime dependency.
func SolveSliderNotch(background, piece []byte) (float64, float64, error) {
	bg, err := decodeGray(background, false)
	if err != nil {
		return 0, 0, err
	}
	template, err := decodeGray(piece, true)
	if err != nil {
		return 0, 0, err
	}
	if template.w > bg.w || template.h > bg.h {
		return 0, 0, errors.New("captcha piece is larger than background")
	}

	count := float64(template.w * template.h)
	templateMean := 0.0
	for _, value := range template.pix {
		templateMean += value
	}
	templateMean /= count
	templateVariance := 0.0
	centered := make([]float64, len(template.pix))
	for i, value := range template.pix {
		centered[i] = value - templateMean
		templateVariance += centered[i] * centered[i]
	}
	if templateVariance < 1e-9 {
		return 0, 0, errors.New("captcha piece has no usable contrast")
	}

	stride := bg.w + 1
	integral := make([]float64, (bg.w+1)*(bg.h+1))
	integralSq := make([]float64, len(integral))
	for y := 0; y < bg.h; y++ {
		row, rowSq := 0.0, 0.0
		for x := 0; x < bg.w; x++ {
			value := bg.pix[y*bg.w+x]
			row += value
			rowSq += value * value
			index := (y+1)*stride + x + 1
			integral[index] = integral[index-stride] + row
			integralSq[index] = integralSq[index-stride] + rowSq
		}
	}
	rectSum := func(values []float64, x, y, w, h int) float64 {
		x1, y1 := x+w, y+h
		return values[y1*stride+x1] - values[y*stride+x1] - values[y1*stride+x] + values[y*stride+x]
	}

	bestX, bestScore := 0, -2.0
	for y := 0; y <= bg.h-template.h; y++ {
		for x := 0; x <= bg.w-template.w; x++ {
			sum := rectSum(integral, x, y, template.w, template.h)
			sumSq := rectSum(integralSq, x, y, template.w, template.h)
			mean := sum / count
			variance := sumSq - count*mean*mean
			if variance < 1e-9 {
				continue
			}
			dot := 0.0
			for ty := 0; ty < template.h; ty++ {
				bgOffset := (y+ty)*bg.w + x
				tOffset := ty * template.w
				for tx := 0; tx < template.w; tx++ {
					dot += centered[tOffset+tx] * (bg.pix[bgOffset+tx] - mean)
				}
			}
			score := dot / math.Sqrt(templateVariance*variance)
			if score > bestScore {
				bestX, bestScore = x, score
			}
		}
	}
	if bestScore < -1 {
		return 0, bestScore, errors.New("captcha solver produced no candidate")
	}
	return float64(bestX), bestScore, nil
}
