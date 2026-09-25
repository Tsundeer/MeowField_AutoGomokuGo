package vision

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"

	"MeowField_AutoGomokuGo/internal/domain"

	"golang.org/x/image/draw"
)

func loadSample(t *testing.T) image.Image {
	t.Helper()
	f, err := os.Open("../../testdata_board_1080p.png")
	if err != nil {
		t.Fatalf("open sample: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return img
}

func stonesMap(t *testing.T, d *Detection) map[string]int8 {
	t.Helper()
	m := map[string]int8{}
	for _, s := range d.Stones() {
		m[s.Label] = s.Color
	}
	return m
}

func TestDetectGoldenSample(t *testing.T) {
	d, err := Detect(loadSample(t))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	got := stonesMap(t, d)
	if len(got) != 1 || got["G7"] != 2 {
		t.Fatalf("stones = %v, want {G7:2}", got)
	}
	if len(d.Unknown) != 0 {
		t.Fatalf("unknown = %v", d.Unknown)
	}
}

func TestDetectMultiResolution(t *testing.T) {
	src := loadSample(t)
	base := src.Bounds().Dy()
	for _, h := range []int{720, 900, 1440} {
		scale := float64(h) / float64(base)
		w := int(math.Round(float64(src.Bounds().Dx()) * scale))
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
		d, err := Detect(dst)
		if err != nil {
			t.Fatalf("%dp detect: %v", h, err)
		}
		got := stonesMap(t, d)
		if len(got) != 1 || got["G7"] != 2 {
			t.Fatalf("%dp stones = %v", h, got)
		}
		if len(d.Unknown) != 0 {
			t.Fatalf("%dp unknown = %v", h, d.Unknown)
		}
	}
}

// drawStone 复刻 Python 测试的合成棋子（带描边/高光/阴影）。
func drawStone(dst *image.RGBA, cx, cy int, sp float64, color int8) {
	radius := int(sp * 0.42)
	sx, sy := cx+int(sp*0.13), cy+int(sp*0.13)
	// 阴影（椭圆近似：圆 + 略压扁由采样时 y 范围控制，这里简化为圆）
	fillCircle(dst, sx, sy, int(float64(radius)*0.75), uint32(150), uint32(160), uint32(170))
	if color == 2 {
		fillCircle(dst, cx, cy, radius, 222, 224, 228)
		ringCircle(dst, cx, cy, radius, 140, 140, 145)
		fillCircle(dst, cx-radius/3, cy-radius/3, radius/2, 245, 246, 248)
	} else {
		fillCircle(dst, cx, cy, radius, 52, 48, 45)
		ringCircle(dst, cx, cy, radius, 25, 22, 20)
		fillCircle(dst, cx-radius/3, cy-radius/3, radius/2, 110, 105, 100)
	}
}

func fillCircle(dst *image.RGBA, cx, cy, r int, rr, gg, bb uint32) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				mr, mg, mb := mixed(dst, x, y, rr, gg, bb)
					dst.SetRGBA(x, y, color.RGBA{uint8(mr), uint8(mg), uint8(mb), 255})
			}
		}
	}
}

func ringCircle(dst *image.RGBA, cx, cy, r int, rr, gg, bb uint32) {
	inner := r - 2
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			d2 := (x-cx)*(x-cx) + (y-cy)*(y-cy)
			if d2 <= r*r && d2 >= inner*inner {
				mr, mg, mb := mixed(dst, x, y, rr, gg, bb)
					dst.SetRGBA(x, y, color.RGBA{uint8(mr), uint8(mg), uint8(mb), 255})
			}
		}
	}
}

func mixed(dst *image.RGBA, x, y int, rr, gg, bb uint32) (r, g, b uint32) {
	or, og, ob, oa := dst.At(x, y).RGBA()
	a := oa / 257
	if a == 0 {
		a = 1
	}
	return or/257*(255-a)/255 + rr*a/255, og/257*(255-a)/255 + gg*a/255, ob/257*(255-a)/255 + bb*a/255
}

func TestDetectSyntheticStones(t *testing.T) {
	src := loadSample(t)
	d0, err := Detect(src)
	if err != nil {
		t.Fatalf("base detect: %v", err)
	}
	sp := d0.Spacing
	want := map[string]int8{"G7": 2}
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.Set(x, y, src.At(x, y))
		}
	}
	// 16 颗伪随机棋子（与 Python 测试相同布局：rng seed 7 的前 16 个不重复格）
	cells := pseudoShuffle(BoardN, 7)[:16]
	for i, idx := range cells {
		r, c := idx/BoardN, idx%BoardN
		color := int8(1)
		if i%2 == 1 {
			color = 2
		}
		p := d0.Points[r][c]
		drawStone(img, p.X, p.Y, sp, color)
		want[CoordLabel(r, c)] = color
	}
	d, err := Detect(img)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	got := stonesMap(t, d)
	if len(got) != len(want) {
		t.Fatalf("stones count = %d want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("stone %s = %d want %d (all: %v)", k, got[k], v, got)
		}
	}
	if len(d.Unknown) != 0 {
		t.Fatalf("unknown = %v", d.Unknown)
	}
}

// pseudoShuffle 与 Python 测试 rng.shuffle(cells[:16]) 等价的确定性洗牌。
func pseudoShuffle(n, seed int) []int {
	cells := make([]int, n*n)
	for i := range cells {
		cells[i] = i
	}
	// 简易 LCG 洗牌（不必与 numpy 完全一致，只需确定性分布）
	s := uint32(seed*2654435761 + 1)
	for i := len(cells) - 1; i > 0; i-- {
		s = s*1664525 + 1013904223
		j := int(s>>8) % (i + 1)
		cells[i], cells[j] = cells[j], cells[i]
	}
	return cells
}

func CoordLabel(r, c int) string {
	return domain.CoordLabel(r, c)
}
