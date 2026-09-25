package engine

import (
	"testing"
)

func grid(stones map[[2]int]int8) [][]int8 {
	g := make([][]int8, 13)
	for r := range g {
		g[r] = make([]int8, 13)
	}
	for rc, v := range stones {
		g[rc[0]][rc[1]] = v
	}
	return g
}

func s(list [][2]int) map[[2]int]int8 {
	m := map[[2]int]int8{}
	for _, rc := range list {
		m[rc] = 1
	}
	return m
}

func TestEngineTactics(t *testing.T) {
	ai := NewSimpleAI()

	t.Run("连五进攻", func(t *testing.T) {
		g := grid(map[[2]int]int8{
			{6, 4}: 1, {6, 5}: 1, {6, 6}: 1, {6, 7}: 1,
			{5, 5}: 2, {5, 6}: 2, {5, 7}: 2,
		})
		mv, err := ai.BestMove(g, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if !(mv.R == 6 && (mv.C == 3 || mv.C == 8)) {
			t.Fatalf("got (%d,%d)", mv.R, mv.C)
		}
	})

	t.Run("堵对方四连", func(t *testing.T) {
		g := grid(map[[2]int]int8{
			{6, 4}: 2, {6, 5}: 2, {6, 6}: 2, {6, 7}: 2,
			{5, 5}: 1, {5, 6}: 1, {5, 7}: 1,
		})
		mv, err := ai.BestMove(g, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if !(mv.R == 6 && (mv.C == 3 || mv.C == 8)) {
			t.Fatalf("got (%d,%d)", mv.R, mv.C)
		}
	})

	t.Run("中盘合法", func(t *testing.T) {
		g := grid(map[[2]int]int8{
			{6, 6}: 1, {6, 7}: 2, {5, 5}: 1, {7, 7}: 2, {7, 6}: 1,
			{5, 7}: 2, {4, 8}: 1, {8, 6}: 2, {5, 6}: 1, {6, 5}: 2,
			{8, 5}: 1, {4, 5}: 2,
		})
		mv, err := ai.BestMove(g, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if mv.R < 0 || mv.R >= 13 || mv.C < 0 || mv.C >= 13 {
			t.Fatalf("非法坐标 (%d,%d)", mv.R, mv.C)
		}
	})

	t.Run("空盘下天元", func(t *testing.T) {
		mv, err := ai.BestMove(grid(nil), 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if mv.R != 6 || mv.C != 6 {
			t.Fatalf("got (%d,%d)", mv.R, mv.C)
		}
	})

	t.Run("自对弈20步", func(t *testing.T) {
		g := grid(nil)
		color := int8(1)
		ok := true
		for step := 0; step < 20; step++ {
			mv, err := ai.BestMove(g, int(color), 0.25)
			if err != nil || g[mv.R][mv.C] != 0 {
				ok = false
				break
			}
			g[mv.R][mv.C] = color
			color = 3 - color
		}
		if !ok {
			t.Fatal("自对弈中断")
		}
	})
}

// s2 辅助（黑白混合局面）
func s2(black, white [][2]int) map[[2]int]int8 {
	m := map[[2]int]int8{}
	for _, rc := range black {
		m[rc] = 1
	}
	for _, rc := range white {
		m[rc] = 2
	}
	return m
}

var _ = s2
