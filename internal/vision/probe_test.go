package vision

import (
	"image/png"
	"os"
	"testing"

	"MeowField_AutoGomokuGo/internal/domain"
)

func TestProbeHSV(t *testing.T) {
	f, _ := os.Open("../../testdata_board_1080p.png")
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	b := img.Bounds()
	px := func(x, y int) (uint8, uint8, uint8) {
		r, g, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return uint8(bb >> 8), uint8(g >> 8), uint8(r >> 8)
	}
	d, err := Detect(img)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	t.Logf("panel=%v spacing=%.1f", d.Panel, d.Spacing)
	for _, pt := range []struct {
		label string
		r, c  int
	}{{"G7", 6, 6}, {"A1", 0, 0}, {"E2", 1, 4}, {"M13", 12, 12}} {
		p := d.Points[pt.r][pt.c]
		hsv, _ := medianHSV(px, p.X, p.Y, int(d.Spacing*0.16), b.Dx(), b.Dy())
		t.Logf("%s @(%d,%d) HSV=%+v board=%d (domain empty=%d)", pt.label, p.X, p.Y, hsv, d.Board[pt.r][pt.c], domain.Empty)
	}
}
