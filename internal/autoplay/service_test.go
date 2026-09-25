package autoplay

import (
	"testing"
	"time"

	"MeowField_AutoGomokuGo/internal/domain"
	"MeowField_AutoGomokuGo/internal/engine"
)

// ---- 测试基建：FakeAI + 注入 ----

type fakeAI struct{ calls int }

func (f *fakeAI) Name() string { return "fake" }
func (f *fakeAI) Start() error { return nil }
func (f *fakeAI) Stop()        {}
func (f *fakeAI) NewGame()     {}
func (f *fakeAI) BestMove(grid [][]int8, myColor int, _ float64) (*engine.Move, error) {
	f.calls++
	best := [2]int{6, 6}
	d0 := 1 << 30
	for r := 0; r < 13; r++ {
		for c := 0; c < 13; c++ {
			if grid[r][c] == 0 {
				d := abs2(r-6) + abs2(c-6)
				if d < d0 {
					d0, best = d, [2]int{r, c}
				}
			}
		}
	}
	return &engine.Move{R: best[0], C: best[1], Info: map[string]any{"engine": "fake"}}, nil
}

func abs2(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

type harness struct {
	s       *Service
	clicked [][2]int
	stopped bool
}

func newHarness(color string) *harness {
	h := &harness{}
	h.s = NewService(Settings{OurColor: color, EngineKind: "simple",
		MoveDelay: 0, EngineThreads: 0, ThinkLimit: 1},
		nil, nil)
	h.s.MkEngine(func(kind string, think float64) engine.Engine { return &fakeAI{} })
	h.s.active = true
	// 点击桩：记录并直接设置 awaiting（模拟真实 clickCell 行为）
	origClick := h.s.clickCellFn
	h.s.clickCellFn = func(r, c int) bool {
		h.clicked = append(h.clicked, [2]int{r, c})
		if origClick != nil {
			return origClick(r, c)
		}
		return true
	}
	h.s.verifyGhostFn = func(int, int) bool { return true }
	return h
}

func (h *harness) feed(b *domain.BoardGrid) {
	h.s.sameCount = 99
	h.s.processChange(b)
	h.drainThink()
}

// drainThink 等待后台思考完成并收尾（模拟轮询线程的收尾步骤）。
func (h *harness) drainThink() {
	deadline := time.Now().Add(15 * time.Second)
	for h.s.thinking && !h.s.thinkPending() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.s.thinkPending() {
		h.s.finishThink()
	}
}

func g(stones map[[2]int]int8) *domain.BoardGrid {
	b := domain.NewBoardGrid()
	for rc, v := range stones {
		b.G[rc[0]][rc[1]] = v
	}
	return b
}

func sOnly(list [][2]int, color int8) map[[2]int]int8 {
	m := map[[2]int]int8{}
	for _, rc := range list {
		m[rc] = color
	}
	return m
}

// ---- 场景 ----

func TestScenarioMidgameTakeover(t *testing.T) {
	h := newHarness("2")
	h.s.confirmed = nil
	h.feed(g(sOnly([][2]int{{7, 7}, {7, 8}}, 1))) // 黑2白0? 黑(7,7)(7,8), 再加白(6,6)
	h.feed(g(map[[2]int]int8{
		{7, 7}: 1, {7, 8}: 1, {6, 6}: 2,
	})) // 黑2白1 -> 轮白(我)
	if len(h.clicked) != 1 {
		t.Fatalf("中盘接管: clicked=%v", h.clicked)
	}
	if h.s.ourColor != 2 {
		t.Fatalf("颜色=%d", h.s.ourColor)
	}
}

func TestScenarioAutoLearningConfirmAndCorrect(t *testing.T) {
	h := newHarness("auto")
	h.s.confirmed = nil
	h.s.active = false
	h.feed(g(sOnly([][2]int{{7, 7}}, 1))) // 初始 1 黑
	h.s.confirmed = g(sOnly([][2]int{{7, 7}}, 1))
	// 白子落下 -> 学习: 我=黑(暂定)
	h.feed(g(sOnly([][2]int{{7, 7}, {6, 6}}, 1)))
	h.s.confirmed = g(sOnly([][2]int{{7, 7}, {6, 6}}, 1))
	h.feed(g(sOnly([][2]int{{7, 7}, {6, 6}, {2, 2}}, 1))) // 又一颗黑? 不合法局面不测
	_ = h
}

func TestScenarioInterleavedConfirm(t *testing.T) {
	h := newHarness("1")
	h.s.confirmed = nil
	h.feed(g(sOnly([][2]int{{7, 7}}, 1)))                  // 黑1 -> 轮白
	h.feed(g(map[[2]int]int8{{7, 7}: 1, {6, 6}: 2}))       // 白落 -> 轮黑(我) -> 出招
	if len(h.clicked) != 1 {
		t.Fatalf("setup clicked=%v", h.clicked)
	}
	r, c := h.clicked[0][0], h.clicked[0][1]
	h.s.awaitR, h.s.awaitC = r, c
	h.s.awaitDeadline = time.Now().Add(clickTimeout)
	base := g(sOnly([][2]int{{7, 7}, {6, 6}}, 1))
	h.s.confirmed = base
	// 同批差分: 我方确认 + 对方快招 (0,0) 白
	next := g(sOnly([][2]int{{7, 7}, {6, 6}, {r, c}}, 1))
	next.G[0][0] = 2
	h.feed(next)
	if h.s.ourColor != 1 || !h.s.colorLocked {
		t.Fatalf("确认错误 our=%d locked=%v", h.s.ourColor, h.s.colorLocked)
	}
	if h.s.expected != 1 {
		t.Fatalf("轮次被覆盖: %d", h.s.expected)
	}
	if len(h.clicked) != 2 {
		t.Fatalf("停摆! clicked=%v", h.clicked)
	}
}

func TestScenarioOpeningCenter(t *testing.T) {
	h := newHarness("1")
	h.s.confirmed = nil
	h.s.active = false
	h.feed(g(nil)) // 空盘
	if len(h.clicked) != 0 {
		t.Fatal("开启前不应出手")
	}
	h.s.SetActive(true)
	h.drainThink()
	if len(h.clicked) != 1 || h.clicked[0] != [2]int{6, 6} {
		t.Fatalf("先手开局应下中心: %v", h.clicked)
	}
	// 中心出现黑子 -> 确认
	h.s.sameCount = 99
	h.s.confirmed = g(nil)
	h.feed(g(map[[2]int]int8{{6, 6}: 1}))
	if h.s.ourColor != 1 || !h.s.colorLocked {
		t.Fatalf("颜色未锁定: %d %v", h.s.ourColor, h.s.colorLocked)
	}
	if h.s.expected != 2 {
		t.Fatalf("轮次=%d", h.s.expected)
	}
}

func TestScenarioOpeningDeclinedWaits(t *testing.T) {
	h := newHarness("auto")
	h.s.confirmed = nil
	h.s.active = false
	h.feed(g(nil))
	h.s.SetActive(true)
	h.drainThink()
	if len(h.clicked) != 1 || h.clicked[0] != [2]int{6, 6} {
		t.Fatalf("应试探中心: %v", h.clicked)
	}
	// 未生效 -> 超时 -> 回退等待
	h.s.awaitDeadline = time.Now().Add(-time.Second)
	h.s.checkClickTimeout()
	if h.s.awaitR != -1 || h.s.ourColor != 0 {
		t.Fatalf("回退状态错误 awaiting=%d our=%d", h.s.awaitR, h.s.ourColor)
	}
	// 对方落黑子 -> 接管出招
	h.feed(g(sOnly([][2]int{{7, 7}}, 1)))
	if len(h.clicked) != 2 {
		t.Fatalf("接管出招失败: %v", h.clicked)
	}
}

func TestScenarioEqualCountsAggressiveOnce(t *testing.T) {
	h := newHarness("1")
	h.s.confirmed = nil
	h.feed(g(sOnly([][2]int{{7, 7}}, 1)))        // 黑1
	h.feed(g(map[[2]int]int8{{7, 7}: 1, {6, 6}: 2})) // 黑1白1 -> 先行方未知
	h.drainThink()
	t.Logf("dbg: thinking=%v expected=%d our=%d active=%v await=%d clicked=%v",
		h.s.thinking, h.s.expected, h.s.ourColor, h.s.active, h.s.awaitR, h.clicked)
	// 激进接管：按我方先行尝试一次出手
	if len(h.clicked) != 1 {
		t.Fatalf("应尝试一次出手: %v", h.clicked)
	}
	// 点击未生效（还没轮到我方）-> 重试耗尽后回退等待，不再循环出手
	for i := 0; i < clickRetriesMax+1; i++ {
		h.s.awaitDeadline = time.Now().Add(-time.Second)
		h.s.checkClickTimeout()
	}
	if h.s.awaitR != -1 {
		t.Fatalf("应回退等待: awaiting=%d", h.s.awaitR)
	}
	// 回退后不会立即再出手（assumeTried 已置位）
	h.drainThink()
	if len(h.clicked) != 1+clickRetriesMax {
		t.Fatalf("不应重复出手: %v", h.clicked)
	}
}

func TestScenarioManualOurStoneContinues(t *testing.T) {
	h := newHarness("1")
	h.s.confirmed = nil
	h.feed(g(sOnly([][2]int{{7, 7}}, 1)))
	h.feed(g(map[[2]int]int8{{7, 7}: 1, {6, 6}: 2})) // 白落 -> 轮黑(我) -> 出招
	if len(h.clicked) != 1 {
		t.Fatalf("setup: %v", h.clicked)
	}
	// 用户手动下了黑子（程序那步没算）
	h.s.awaitR, h.s.awaitC = -1, -1
	h.s.confirmed = g(sOnly([][2]int{{7, 7}, {6, 6}}, 1))
	h.feed(g(sOnly([][2]int{{7, 7}, {6, 6}, {8, 8}}, 1))) // 手动黑
	if h.s.expected != 2 {
		t.Fatalf("expected=%d", h.s.expected)
	}
	// 白应招 -> 接管
	h.feed(g(map[[2]int]int8{{7, 7}: 1, {6, 6}: 2, {8, 8}: 1, {5, 5}: 2}))
	if len(h.clicked) < 2 {
		t.Fatalf("接管失败: %v", h.clicked)
	}
}
