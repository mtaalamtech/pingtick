package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

func main() {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.Transparent}, image.Point{}, draw.Src)
	blue := color.RGBA{0, 120, 215, 255}
	cx, cy, r := 128, 128, 110
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, blue)
			}
		}
	}
	white := color.RGBA{255, 255, 255, 255}
	// left slant
	for x := 70; x < 110; x++ {
		for y := 130; y < 170; y++ {
			if y > x+40 && y < x+80 {
				img.Set(x, y, white)
			}
		}
	}
	// right slant
	for x := 100; x < 200; x++ {
		for y := 70; y < 170; y++ {
			if y > -x+250 && y < -x+290 {
				img.Set(x, y, white)
			}
		}
	}
	f, _ := os.Create("winres/icon2.png")
	png.Encode(f, img)
	f.Close()
}
