// Package domain：棋盘领域模型与纯逻辑（无 I/O、无 UI 依赖）。
package domain

import "fmt"

const (
	// BoardN 13x13 棋盘。
	BoardN = 13
	// 颜色约定：0=空 1=黑 2=白（与 Python 版一致）。
	Empty = 0
	Black = 1
	White = 2
)

// CoordLabel (行, 列) 0 基 -> 游戏坐标，如 (6,6) -> G7。
func CoordLabel(r, c int) string {
	return fmt.Sprintf("%c%d", 'A'+c, r+1)
}

// BoardGrid 13×13 棋盘状态。
type BoardGrid struct {
	N int
	G [BoardN][BoardN]int8
}

// NewBoardGrid 返回空棋盘。
func NewBoardGrid() *BoardGrid { return &BoardGrid{N: BoardN} }

// FromSlices 由 [13][13]int8 构造。
func FromSlices(g [BoardN][BoardN]int8) *BoardGrid {
	b := NewBoardGrid()
	for r := range b.G {
		for c := range b.G[r] {
			b.G[r][c] = g[r][c]
		}
	}
	return b
}

// ToSlices 导出数组。
func (b *BoardGrid) ToSlices() [BoardN][BoardN]int8 { return b.G }

// At 读取。
func (b *BoardGrid) At(r, c int) int8 { return b.G[r][c] }

// FromSlice 由 13x13 切片构造。
func FromSlice(g [][]int8) *BoardGrid {
	b := NewBoardGrid()
	for r := 0; r < BoardN && r < len(g); r++ {
		for c := 0; c < BoardN && c < len(g[r]); c++ {
			b.G[r][c] = g[r][c]
		}
	}
	return b
}

// Clone 深拷贝。
func (b *BoardGrid) Clone() *BoardGrid {
	c := *b
	return &c
}

// StoneCount 总子数。
func (b *BoardGrid) StoneCount() int {
	n := 0
	for r := range b.G {
		for c := range b.G[r] {
			if b.G[r][c] != Empty {
				n++
			}
		}
	}
	return n
}

// ColorCount 某色子数。
func (b *BoardGrid) ColorCount(color int8) int {
	n := 0
	for r := range b.G {
		for c := range b.G[r] {
			if b.G[r][c] == color {
				n++
			}
		}
	}
	return n
}

// IsEmpty 空盘。
func (b *BoardGrid) IsEmpty() bool { return b.StoneCount() == 0 }

// Equal 与另一棋盘逐点相等。
func (b *BoardGrid) Equal(o *BoardGrid) bool {
	if o == nil || b.N != o.N {
		return false
	}
	for r := range b.G {
		for c := range b.G[r] {
			if b.G[r][c] != o.G[r][c] {
				return false
			}
		}
	}
	return true
}

// MoverByCounts 轮到哪方行棋：子少的一方。
// 双方子数相等（轮到先行方）且先行方无法由当前局面推出时返回 0。
func (b *BoardGrid) MoverByCounts() int8 {
	bk := b.ColorCount(Black)
	w := b.ColorCount(White)
	if bk == w {
		return 0
	}
	if bk < w {
		return Black
	}
	return White
}

// CoordLabelAt 包级别便捷函数。
func CoordLabelAt(r, c int) string { return CoordLabel(r, c) }
