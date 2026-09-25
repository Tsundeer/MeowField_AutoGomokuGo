package engine

import (
	"errors"
	"math"
	"sort"
	"time"
)

// 内置纯 Go 五子棋引擎：增量窗口计分 + α-β 迭代加深。
// 与 Python 版 engine.py 同一套算法（窗口权重、强制连五/必堵规则一致）。

const (
	five     = 10_000_000
	winScore = five * 10
	size     = 13
	pad      = size + 4
)

var winWeights = [6]int{0, 4, 32, 512, 8192, five}

var dirs = [4][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}}

// SimpleAI 内置引擎。
type SimpleAI struct {
	b           []int8
	lineScores  [][2]int
	total       [2]int
	stones      int
	nb          []int
	lines       [][]int
	cellLines   [][]int
	nbcache     []int
	bestPos     int
	bestDepth   int
	branch      int
}

// NewSimpleAI 构造。
func NewSimpleAI() *SimpleAI {
	a := &SimpleAI{}
	a.reset()
	return a
}

func (a *SimpleAI) Name() string { return "simple" }
func (a *SimpleAI) Start() error { return nil }
func (a *SimpleAI) Stop()        {}

func (a *SimpleAI) reset() {
	a.b = make([]int8, pad*pad)
	for i := range a.b {
		r, c := i/pad, i%pad
		if r < 2 || r >= pad-2 || c < 2 || c >= pad-2 {
			a.b[i] = 3
		}
	}
	a.total = [2]int{}
	a.stones = 0
	a.nb = make([]int, pad*pad)
	a.buildLines()
}

func (a *SimpleAI) buildLines() {
	idx := func(r, c int) int { return (r+2)*pad + (c + 2) }
	var ls [][]int
	for r := 0; r < size; r++ {
		l := make([]int, size)
		for c := range l {
			l[c] = idx(r, c)
		}
		ls = append(ls, l)
	}
	for c := 0; c < size; c++ {
		l := make([]int, size)
		for r := range l {
			l[r] = idx(r, c)
		}
		ls = append(ls, l)
	}
	for s := -(size - 1); s < size; s++ {
		var l []int
		for r := 0; r < size; r++ {
			if c := r - s; c >= 0 && c < size {
				l = append(l, idx(r, c))
			}
		}
		if len(l) >= 5 {
			ls = append(ls, l)
		}
	}
	for s := 0; s < 2*size-1; s++ {
		var l []int
		for r := 0; r < size; r++ {
			if c := s - r; c >= 0 && c < size {
				l = append(l, idx(r, c))
			}
		}
		if len(l) >= 5 {
			ls = append(ls, l)
		}
	}
	a.lines = ls
	a.cellLines = make([][]int, pad*pad)
	for li, line := range ls {
		for _, pos := range line {
			a.cellLines[pos] = append(a.cellLines[pos], li)
		}
	}
	a.lineScores = make([][2]int, len(ls))
}

// NewGame 重置棋盘。
func (a *SimpleAI) NewGame() { a.reset() }

func (a *SimpleAI) lineScore(line []int) (int, int) {
	s1, s2 := 0, 0
	b := a.b
	for i := 0; i+5 <= len(line); i++ {
		c1, c2 := 0, 0
		for _, p := range line[i : i+5] {
			switch b[p] {
			case 1:
				c1++
			case 2:
				c2++
			}
		}
		if c1 > 0 && c2 == 0 {
			s1 += winWeights[c1]
		} else if c2 > 0 && c1 == 0 {
			s2 += winWeights[c2]
		}
	}
	return s1, s2
}

func (a *SimpleAI) place(pos int, color int8) {
	a.b[pos] = color
	a.stones++
	for _, off := range a.neighbors() {
		a.nb[pos+off]++
	}
	for _, li := range a.cellLines[pos] {
		old := a.lineScores[li]
		a.total[0] -= old[0]
		a.total[1] -= old[1]
		ns1, ns2 := a.lineScore(a.lines[li])
		a.lineScores[li] = [2]int{ns1, ns2}
		a.total[0] += ns1
		a.total[1] += ns2
	}
}

func (a *SimpleAI) unplace(pos int) {
	color := a.b[pos]
	a.b[pos] = 0
	a.stones--
	for _, off := range a.neighbors() {
		a.nb[pos+off]--
	}
	for _, li := range a.cellLines[pos] {
		old := a.lineScores[li]
		a.total[0] -= old[0]
		a.total[1] -= old[1]
		ns1, ns2 := a.lineScore(a.lines[li])
		a.lineScores[li] = [2]int{ns1, ns2}
		a.total[0] += ns1
		a.total[1] += ns2
	}
	_ = color
}

func (a *SimpleAI) neighbors() []int {
	if a.nbcache != nil {
		return a.nbcache
	}
	var nb []int
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			if dr != 0 || dc != 0 {
				nb = append(nb, dr*pad+dc)
			}
		}
	}
	a.nbcache = nb
	return nb
}

func (a *SimpleAI) makesFive(pos int, color int8) bool {
	P := pad
	a.b[pos] = color
	ok := false
	r0, c0 := pos/P, pos%P
	for _, d := range dirs {
		cnt := 1
		for rr, cc := r0+d[0], c0+d[1]; a.b[rr*P+cc] == color; rr, cc = rr+d[0], cc+d[1] {
			cnt++
		}
		for rr, cc := r0-d[0], c0-d[1]; a.b[rr*P+cc] == color; rr, cc = rr-d[0], cc-d[1] {
			cnt++
		}
		if cnt >= 5 {
			ok = true
			break
		}
	}
	a.b[pos] = 0
	return ok
}

func (a *SimpleAI) candidates(color int8, cap int) []int {
	type cand struct {
		score, pos int
	}
	var cs []cand
	P := pad
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			pos := (r + 2) * P + (c + 2)
			if a.b[pos] == 0 && a.nb[pos] > 0 {
				cs = append(cs, cand{a.nb[pos]*16 - (abs(r-size/2) + abs(c-size/2)), pos})
			}
		}
	}
	if len(cs) == 0 && a.stones == 0 {
		return []int{a.idx(size/2, size/2)}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].score > cs[j].score })
	if len(cs) > cap {
		cs = cs[:cap]
	}
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.pos
	}
	return out
}

func (a *SimpleAI) idx(r, c int) int { return (r + 2) * pad + (c + 2) }

func (a *SimpleAI) evalFor(color int8) int {
	opp := int(color) - 1
	other := 1 - opp
	return a.total[opp] - a.total[other]
}

func (a *SimpleAI) search(color int8, depth, alpha, beta, ply int) (int, int) {
	opp := 3 - color
	for _, pos := range a.candidates(color, 20) {
		if a.makesFive(pos, color) {
			return winScore - ply, pos
		}
	}
	var threatCells []int
	for _, pos := range a.candidates(opp, 20) {
		if a.makesFive(pos, opp) {
			threatCells = append(threatCells, pos)
		}
	}
	moves := threatCells
	if len(moves) == 0 {
		if depth <= 0 {
			return a.evalFor(color), 0
		}
		moves = a.candidates(color, a.branch)
	}
	if len(moves) == 0 {
		return 0, 0
	}
	best, bestPos := math.MinInt64/2, 0
	for _, pos := range moves {
		a.place(pos, color)
		stillLost := false
		if len(threatCells) > 0 {
			for _, t := range threatCells {
				if t != pos && a.makesFive(t, opp) {
					stillLost = true
					break
				}
			}
		}
		score := 0
		if stillLost {
			score = -(winScore - ply - 1)
		} else {
			sc, _ := a.search(opp, depth-1, -beta, -alpha, ply+1)
			score = -sc
		}
		a.unplace(pos)
		if score > best {
			best, bestPos = score, pos
			if score > alpha {
				alpha = score
			}
			if alpha >= beta {
				break
			}
		}
	}
	return best, bestPos
}

// BestMove 计算着法。grid 13x13；timeLimitSec 为思考预算。
func (a *SimpleAI) BestMove(grid [][]int8, myColor int, timeLimitSec float64) (*Move, error) {
	a.reset()
	for r := range grid {
		for c := range grid[r] {
			if grid[r][c] != 0 {
				a.place(a.idx(r, c), grid[r][c])
			}
		}
	}
	if a.stones == 0 {
		m := size / 2
		return &Move{R: m, C: m, Info: map[string]any{"note": "开局天元"}}, nil
	}
	if a.stones >= size*size {
		return nil, errors.New("棋盘已满")
	}
	a.branch = 14
	if timeLimitSec <= 0 {
		timeLimitSec = 20
	}
	deadline := time.Now().Add(time.Duration(timeLimitSec * float64(time.Second)))
	color := int8(myColor)
	bestPos, bestDepth := 0, 0
	for depth := 2; depth <= 8; depth += 2 {
		a.bestPos = bestPos
		t0 := time.Now()
		score, pos, ok := a.searchRoot(color, depth, deadline)
		if !ok {
			break
		}
		if pos >= 0 {
			bestPos, bestDepth = pos, depth
			if math.Abs(float64(score)) >= float64(winScore-100) {
				break
			}
		}
		if time.Since(t0) > time.Duration(timeLimitSec*6/10*float64(time.Second)) {
			break
		}
	}
	if bestPos == 0 {
		return nil, errors.New("无着法")
	}
	P := pad
	return &Move{R: bestPos/P - 2, C: bestPos%P - 2,
		Info: map[string]any{"engine": "simple", "depth": bestDepth}}, nil
}

func (a *SimpleAI) searchRoot(color int8, depth int, deadline time.Time) (int, int, bool) {
	if time.Now().After(deadline) {
		return 0, 0, false
	}
	opp := 3 - color
	for _, pos := range a.candidates(color, 24) {
		if a.makesFive(pos, color) {
			return winScore, pos, true
		}
	}
	var threatCells []int
	for _, pos := range a.candidates(opp, 24) {
		if a.makesFive(pos, opp) {
			threatCells = append(threatCells, pos)
		}
	}
	moves := threatCells
	if len(moves) == 0 {
		moves = a.candidates(color, a.branch*2)
	}
	if a.bestPos != 0 {
		for i, m := range moves {
			if m == a.bestPos {
				moves = append(moves[:i], moves[i+1:]...)
				moves = append([]int{a.bestPos}, moves...)
				break
			}
		}
	}
	if len(moves) == 0 {
		return 0, 0, false
	}
	best, bestPos := math.MinInt64/2, 0
	alpha, beta := math.MinInt64/2, math.MaxInt64/2
	for _, pos := range moves {
		if time.Now().After(deadline) {
			return best, bestPos, bestPos != 0
		}
		a.place(pos, color)
		score := 0
		stillLost := false
		if len(threatCells) > 0 {
			for _, t := range threatCells {
				if t != pos && a.makesFive(t, opp) {
					stillLost = true
					break
				}
			}
		}
		if stillLost {
			score = -(winScore - 1)
		} else {
			sc, _ := a.search(opp, depth-1, -beta, -alpha, 1)
			score = -sc
		}
		a.unplace(pos)
		if score > best {
			best, bestPos = score, pos
			if score > alpha {
				alpha = score
			}
		}
	}
	return best, bestPos, true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
