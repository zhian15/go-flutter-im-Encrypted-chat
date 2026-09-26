package captcha

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand"
)

// Generate 生成 4 位数字图形验证码，返回 code 与 base64 PNG
func Generate() (code string, b64 string, err error) {
	const w, h = 120, 40
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// 浅色背景
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 245, G: 247, B: 250, A: 255}}, image.Point{}, draw.Src)

	// 干扰线
	for i := 0; i < 4; i++ {
		drawLine(img, rand.Intn(w), rand.Intn(h), rand.Intn(w), rand.Intn(h), color.RGBA{R: 180, G: 190, B: 205, A: 160})
	}

	// 4 位数字
	digits := make([]byte, 4)
	for i := 0; i < 4; i++ {
		digits[i] = byte('0' + rand.Intn(10))
	}
	code = string(digits)

	// 简单绘制数字（用 3x5 点阵替代字体，避免引入字体文件）
	drawDigit(img, code, 10, 8)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", err
	}
	return code, base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// 3x5 点阵数字字模
var digitMaps = map[byte][5][3]int{
	'0': {{1, 1, 1}, {1, 0, 1}, {1, 0, 1}, {1, 0, 1}, {1, 1, 1}},
	'1': {{0, 1, 0}, {1, 1, 0}, {0, 1, 0}, {0, 1, 0}, {1, 1, 1}},
	'2': {{1, 1, 1}, {0, 0, 1}, {1, 1, 1}, {1, 0, 0}, {1, 1, 1}},
	'3': {{1, 1, 1}, {0, 0, 1}, {1, 1, 1}, {0, 0, 1}, {1, 1, 1}},
	'4': {{1, 0, 1}, {1, 0, 1}, {1, 1, 1}, {0, 0, 1}, {0, 0, 1}},
	'5': {{1, 1, 1}, {1, 0, 0}, {1, 1, 1}, {0, 0, 1}, {1, 1, 1}},
	'6': {{1, 1, 1}, {1, 0, 0}, {1, 1, 1}, {1, 0, 1}, {1, 1, 1}},
	'7': {{1, 1, 1}, {0, 0, 1}, {0, 1, 0}, {0, 1, 0}, {0, 1, 0}},
	'8': {{1, 1, 1}, {1, 0, 1}, {1, 1, 1}, {1, 0, 1}, {1, 1, 1}},
	'9': {{1, 1, 1}, {1, 0, 1}, {1, 1, 1}, {0, 0, 1}, {1, 1, 1}},
}

func drawDigit(img *image.RGBA, code string, x0, y0 int) {
	c := color.RGBA{R: 31, G: 111, B: 235, A: 255} // 主色 #1F6FEB
	for i, ch := range []byte(code) {
		m := digitMaps[ch]
		for r := 0; r < 5; r++ {
			for col := 0; col < 3; col++ {
				if m[r][col] == 1 {
					px, py := x0+i*26+col*5, y0+r*5
					for dx := 0; dx < 5; dx++ {
						for dy := 0; dy < 5; dy++ {
							img.Set(px+dx, py+dy, c)
						}
					}
				}
			}
		}
	}
}

func drawLine(img *image.RGBA, x1, y1, x2, y2 int, c color.Color) {
	steps := 40
	for i := 0; i <= steps; i++ {
		x := x1 + (x2-x1)*i/steps
		y := y1 + (y2-y1)*i/steps
		if x >= 0 && x < img.Bounds().Dx() && y >= 0 && y < img.Bounds().Dy() {
			img.Set(x, y, c)
		}
	}
}
