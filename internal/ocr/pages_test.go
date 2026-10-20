package ocr

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPageSpec(t *testing.T) {
	if s := pageSpec([]int{3, 1, 2, 5, 7, 8}); s != "1-3,5,7-8" {
		t.Errorf("spec: %s", s)
	}
}

func TestRotateImage(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	in := filepath.Join(dir, "a.png")
	f, _ := os.Create(in)
	_ = png.Encode(f, img)
	f.Close()
	out := filepath.Join(dir, "b.png")
	if err := RotateImage(in, out, 90); err != nil {
		t.Fatal(err)
	}
	g, _ := os.Open(out)
	r, _, err := image.Decode(g)
	g.Close()
	if err != nil || r.Bounds().Dx() != 2 || r.Bounds().Dy() != 4 {
		t.Fatalf("rotated bounds %v %v", r.Bounds(), err)
	}
	if c := color.RGBAModel.Convert(r.At(1, 0)).(color.RGBA); c.R != 255 {
		t.Errorf("top-left pixel should land top-right after 90°: %v", c)
	}
}
