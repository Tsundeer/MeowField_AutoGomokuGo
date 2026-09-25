// Package vision：棋盘识别（OpenCV Python 版的纯 Go 移植）。
//
// 算法与 src/meowfield_gomoku/infrastructure/vision/detector.py 一一对应：
//  1. 米色面板：HSV 掩码最大连通域 -> 棋盘外框
//  2. 网格线：内缩 ROI 中"线色"掩码按行/列投影，阈值分段取峰；
//     边缘线被面板边框阴影遮暗时按等间距外推补全至 13 条
//  3. 落子分类：每个交叉点取中心小块中位 HSV（cv2 语义：H∈[0,180)）
package vision

import (
	"errors"
	"image"
	"math"
	"sort"

	"MeowField_AutoGomokuGo/internal/domain"
)

// 阈值常量（与 Python config.py 标定值一致）。
const (
	PanelH0, PanelH1     = 8, 35
	PanelS0, PanelS1     = 15, 100
	PanelV0, PanelV1     = 195, 256
	LineB0, LineB1       = 200, 216
	LineBRelax0          = 175
	LineBRelax1          = 220
	StoneDarkV           = 150
	StoneWhiteS          = 16
	StoneWhiteV0         = 155
	StoneWhiteV1         = 236
	EmptySMin            = 18
	EmptyVMin            = 228
	BoardN               = domain.BoardN
	insetRatio           = 0.05
	projPeakRatio        = 0.5
	supportMin           = 0.10
	spacingTolerance     = 0.12
)

// ErrBoardNotFound 识别失败。
var ErrBoardNotFound = errors.New("board not found")

// HSV 以 cv2 语义保存的像素值（H∈[0,180), S/V∈[0,255]）。
type HSV struct{ H, S, V int }

// Detection 一次识别结果。
type Detection struct {
	Board   [BoardN][BoardN]int8 // 0空 1黑 2白
	Panel   image.Rectangle      // 面板外框（截图内坐标）
	Points  [BoardN][BoardN]image.Point
	Spacing float64
	Unknown []string
}

// Stones 返回 (label, color) 列表。
func (d *Detection) Stones() []struct {
	Label string
	Color int8
} {
	out := []struct {
		Label string
		Color int8
	}{}
	for r := 0; r < BoardN; r++ {
		for c := 0; c < BoardN; c++ {
			if d.Board[r][c] != domain.Empty {
				out = append(out, struct {
					Label string
					Color int8
				}{domain.CoordLabel(r, c), d.Board[r][c]})
			}
		}
	}
	return out
}

// rgbToHSVcv2 复刻 cv2.cvtColor(BGR->HSV) 的 8bit 语义（入参为 B,G,R）：
// V=max, S=(max-min)*255/max, H 按 60° 扇区映射后除以 2（0..180）。
func rgbToHSVcv2(b, g, r uint8) HSV {
	B, G, R := float64(b), float64(g), float64(r)
	mx := math.Max(R, math.Max(G, B))
	mn := math.Min(R, math.Min(G, B))
	df := mx - mn
	v := mx
	s := 0.0
	if mx > 0 {
		s = df / mx * 255
	}
	h := 0.0
	if df != 0 {
		switch mx {
		case R:
			h = 60 * math.Mod((G-B)/df, 6)
		case G:
			h = 60 * ((B - R) / df + 2)
		default:
			h = 60 * ((R - G) / df + 4)
		}
		if h < 0 {
			h += 360
		}
	}
	return HSV{H: int(h / 2), S: int(s), V: int(v)}
}

// inPanel 面板底色判定。
func (h HSV) inPanel() bool {
	return h.H >= PanelH0 && h.H < PanelH1 && h.S >= PanelS0 && h.S < PanelS1 && h.V >= PanelV0 && h.V < PanelV1
}

// Detect 主入口：img 为任意 RGBA/Gray 可解码图像。
func Detect(img image.Image) (*Detection, error) {
	b := img.Bounds()
	w, hh := b.Dx(), b.Dy()
	if w < BoardN*8 || hh < BoardN*8 {
		return nil, ErrBoardNotFound
	}
	// 返回 BGR 顺序（与 Python cv2.imread 语义一致，阈值均按 BGR 标定）
	px := func(x, y int) (uint8, uint8, uint8) {
		r, g, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return uint8(bb >> 8), uint8(g >> 8), uint8(r >> 8)
	}

	// 1) 面板：HSV 掩码最大连通域（4 邻接，含 bbox 与面积）
	type comp struct{ area, minx, miny, maxx, maxy int }
	mask := make([]bool, w*hh)
	for y := 0; y < hh; y++ {
		for x := 0; x < w; x++ {
			r, g, bb := px(x, y)
			if rgbToHSVcv2(r, g, bb).inPanel() {
				mask[y*w+x] = true
			}
		}
	}
	var best comp
	seen := make([]bool, w*hh)
	for i := range mask {
		if !mask[i] || seen[i] {
			continue
		}
		// BFS
		queue := []int{i}
		seen[i] = true
		cur := comp{}
		for len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			x, y := j%w, j/w
			cur.area++
			if cur.area == 1 || x < cur.minx {
				cur.minx = x
			}
			if cur.area == 1 || y < cur.miny {
				cur.miny = y
			}
			if x > cur.maxx {
				cur.maxx = x
			}
			if y > cur.maxy {
				cur.maxy = y
			}
			for _, nb := range []int{j - 1, j + 1, j - w, j + w} {
				if nb < 0 || nb >= w*hh || seen[nb] || !mask[nb] {
					continue
				}
				if nb%w == 0 && j%w == w-1 { // 行边界
					continue
				}
				if nb%w == w-1 && j%w == 0 {
					continue
				}
				seen[nb] = true
				queue = append(queue, nb)
			}
		}
		if cur.area > best.area {
			best = cur
		}
	}
	if best.area == 0 {
		return nil, errors.New("panel not found: " + ErrBoardNotFound.Error())
	}
	pw, ph := best.maxx-best.minx+1, best.maxy-best.miny+1
	ar := float64(pw) / float64(ph)
	if ar < 0.8 || ar > 1.25 || pw < 160 {
		return nil, errors.New("panel shape abnormal")
	}

	// 2) 网格线投影（内缩 ROI，B 通道线色掩码）
	inset := int(math.Min(float64(pw), float64(ph)) * insetRatio)
	x0, y0 := best.minx+inset, best.miny+inset
	x1, y1 := best.minx+pw-inset, best.miny+ph-inset
	if x1-x0 < BoardN*8 || y1-y0 < BoardN*8 {
		return nil, errors.New("panel too small")
	}
	colProj := make([]int, x1-x0)
	rowProj := make([]int, y1-y0)
	lineAt := func(x, y int) bool {
		bb, _, _ := px(x0+x, y0+y)
		return int(bb) >= LineB0 && int(bb) <= LineB1
	}
	for y := 0; y < y1-y0; y++ {
		for x := 0; x < x1-x0; x++ {
			if lineAt(x, y) {
				colProj[x]++
				rowProj[y]++
			}
		}
	}
	if colProj[maxIdx(colProj)] < (y1-y0)/4 || rowProj[maxIdx(rowProj)] < (x1-x0)/4 {
		return nil, errors.New("no grid structure")
	}

	// 放宽掩码支撑度（全面板）
	relaxCountCol := func(ax, ay, bx, by int) int { // 统计 [ax,bx) 列区间内 relax 像素数
		n := 0
		for y := ay; y < by; y++ {
			for x := ax; x < bx; x++ {
				bb, _, _ := px(best.minx+x, best.miny+y)
				v := int(bb)
				if v >= LineBRelax0 && v <= LineBRelax1 {
					n++
				}
			}
		}
		return n
	}
	supportV := func(xAbs float64) float64 {
		half := max(3, int(float64(pw)*0.006))
		a := int(xAbs) - half - best.minx
		b := int(xAbs) + half + 1 - best.minx
		if a < 0 {
			a = 0
		}
		if b > pw {
			b = pw
		}
		if b <= a {
			return -1
		}
		return float64(relaxCountCol(a, best.miny, b, best.miny+ph)) / float64(ph) / float64(b-a)
	}
	supportH := func(yAbs float64) float64 {
		half := max(3, int(float64(ph)*0.006))
		a := int(yAbs) - half - best.miny
		b := int(yAbs) + half + 1 - best.miny
		if a < 0 {
			a = 0
		}
		if b > ph {
			b = ph
		}
		if b <= a {
			return -1
		}
		// 行区间支撑：逐像素统计
		n := 0
		for y := a; y < b; y++ {
			for x := best.minx; x < best.minx+pw; x++ {
				bb, _, _ := px(x, best.miny+y)
				v := int(bb)
				if v >= LineBRelax0 && v <= LineBRelax1 {
					n++
				}
			}
		}
		return float64(n) / float64(pw) / float64(b-a)
	}

	// 投影峰是 ROI 局部坐标，加上 ROI 原点转为截图绝对坐标
	colRuns := runsAboveF(colProj)
	for i := range colRuns {
		colRuns[i] += float64(x0)
	}
	rowRuns := runsAboveF(rowProj)
	for i := range rowRuns {
		rowRuns[i] += float64(y0)
	}
	xs, err := buildLines(colRuns, supportV, float64(best.minx), float64(pw))
	if err != nil {
		return nil, err
	}
	ys, err := buildLines(rowRuns, supportH, float64(best.miny), float64(ph))
	if err != nil {
		return nil, err
	}
	sp := (xs[len(xs)-1] - xs[0]) / (BoardN - 1)
	spy := (ys[len(ys)-1] - ys[0]) / (BoardN - 1)
	if math.Abs(sp-spy) > 0.18*sp {
		return nil, errors.New("spacing mismatch")
	}

	det := &Detection{Panel: image.Rect(best.minx, best.miny, best.minx+pw, best.miny+ph),
		Spacing: sp}
	for i := 0; i < BoardN; i++ {
		for j := 0; j < BoardN; j++ {
			det.Points[i][j] = image.Point{X: int(xs[j]), Y: int(ys[i])}
		}
	}

	// 3) 落子分类（中位 HSV 采样块）
	radius := max(3, int(sp*0.16))
	for i := 0; i < BoardN; i++ {
		for j := 0; j < BoardN; j++ {
			cx, cy := det.Points[i][j].X, det.Points[i][j].Y
			h, ok := medianHSV(px, cx, cy, radius, w, hh)
			color, unknown := classify(h, ok)
			det.Board[i][j] = color
			if unknown {
				det.Unknown = append(det.Unknown, domain.CoordLabel(i, j))
			}
		}
	}
	return det, nil
}

func classify(h HSV, ok bool) (int8, bool) {
	if !ok {
		return -1, true
	}
	switch {
	case h.V < StoneDarkV:
		return domain.Black, false
	case h.S <= StoneWhiteS && h.V >= StoneWhiteV0 && h.V <= StoneWhiteV1:
		return domain.White, false
	case h.S >= EmptySMin && h.V >= EmptyVMin:
		return domain.Empty, false
	default:
		return -1, true
	}
}

// medianHSV 中心块的中位 HSV（与 Python np.median(patch) 对应）。
func medianHSV(px func(x, y int) (uint8, uint8, uint8), cx, cy, r, w, hh int) (HSV, bool) {
	if cx-r < 0 || cy-r < 0 || cx+r >= w || cy+r >= hh {
		return HSV{}, false
	}
	var hs, ss, vs []int
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			rr, g, bb := px(x, y)
			h := rgbToHSVcv2(rr, g, bb)
			hs = append(hs, h.H)
			ss = append(ss, h.S)
			vs = append(vs, h.V)
		}
	}
	sort.Ints(hs)
	sort.Ints(ss)
	sort.Ints(vs)
	m := len(hs) / 2
	return HSV{H: hs[m], S: ss[m], V: vs[m]}, true
}

// runsAbove 投影阈值分段取峰（对应 Python _runs）。
func runsAboveF(proj []int) []float64 {
	mx := proj[maxIdx(proj)]
	th := float64(mx) * 0.5
	type run struct{ c float64; w int }
	var runs []run
	start := -1
	for i, v := range proj {
		on := float64(v) > th
		if on && start < 0 {
			start = i
		} else if !on && start >= 0 {
			runs = append(runs, run{float64(start+i-1) / 2, i - start})
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, run{float64(start+len(proj)-1) / 2, len(proj) - start})
	}
	if len(runs) == 0 {
		return nil
	}
	mergeDist := max(6, len(proj)/BoardN*2/5)
	merged := []run{runs[0]}
	for _, rr := range runs[1:] {
		last := &merged[len(merged)-1]
		if rr.c-last.c < float64(mergeDist) {
			tw := last.w + rr.w
			last.c = (last.c*float64(last.w) + rr.c*float64(rr.w)) / float64(tw)
			last.w = tw
		} else {
			merged = append(merged, rr)
		}
	}
	out := make([]float64, len(merged))
	for i, rr := range merged {
		out[i] = rr.c
	}
	return out
}

// buildLines 线集合 -> 外推补全 13 条（对应 Python _build_lines）。
func buildLines(cs []float64, support func(float64) float64,
	lo float64, size float64) ([]float64, error) {
	if len(cs) < 3 {
		return nil, errors.New("too few grid lines")
	}
	lines := make([]float64, len(cs))
	copy(lines, cs)
	// 中位间距
	diffs := make([]float64, len(lines)-1)
	for i := range diffs {
		diffs[i] = lines[i+1] - lines[i]
	}
	sp := median(diffs)
	guard := size * 0.004
	for len(lines) < BoardN {
		left, right := lines[0]-sp, lines[len(lines)-1]+sp
		sl, sr := -1.0, -1.0
		if left > lo+guard {
			sl = support(left)
		}
		if right < lo+size-guard {
			sr = support(right)
		}
		if sl < supportMin && sr < supportMin {
			return nil, errors.New("cannot extend lines")
		}
		if sl >= sr {
			lines = append([]float64{left}, lines...)
		} else {
			lines = append(lines, right)
		}
	}
	if len(lines) > BoardN {
		lines = lines[:BoardN]
	}
	for i := 0; i+1 < len(lines); i++ {
		if math.Abs(lines[i+1]-lines[i]-sp) > sp*spacingTolerance {
			return nil, errors.New("irregular spacing")
		}
	}
	return lines, nil
}

func maxIdx(a []int) int {
	m, mi := 0, 0
	for i, v := range a {
		if v > m {
			m, mi = v, i
		}
	}
	return mi
}

func median(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	c := append([]float64(nil), a...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
