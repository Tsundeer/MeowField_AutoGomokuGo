// Package autoplay：自动对弈状态机（后台 goroutine）。
//
// 核心规则（与 Python 版逐条对应）：
//  1. 轮次：子少的一方行棋；子数相等且历史缺失时先观察一次落子再判定
//  2. 我方颜色：点击后棋盘上出现的棋子颜色即我方颜色（铁证）；
//     首次试探点击未生效 -> 判定首次观测落子为你手动所下，纠正颜色
//  3. 接管：开启后随时接管中盘局面；空盘先手开局下中心 H7
//  4. 防幻影：落子前鼠标移到目标点复核悬停预览
//  5. 点击加固：前台校验、预热点击、超时重试
package autoplay

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"MeowField_AutoGomokuGo/internal/capture"
	"MeowField_AutoGomokuGo/internal/domain"
	"MeowField_AutoGomokuGo/internal/engine"
	"MeowField_AutoGomokuGo/internal/vision"
)

// CoordLabel 域坐标转游戏坐标。
func CoordLabel(r, c int) string { return domain.CoordLabel(r, c) }

const (
	pollInterval     = 550 * time.Millisecond
	stableReads      = 2
	clickTimeout     = 3 * time.Second
	clickRetriesMax  = 2
	clickHold        = 90 * time.Millisecond
	verifyHoverDelay = 350 * time.Millisecond
	giveupLimit      = 3
	centerR, centerC = 6, 6
	warmXOffsetRatio = 2 // 预热点击 x = origin + clientW/该值（顶部中央）
)

// Settings 用户可调参数。
type Settings struct {
	OurColor      string  // "auto" / "1" / "2"
	EngineKind    string  // auto / rapfi / simple
	MoveDelay     float64 // 秒
	EngineThreads int     // 0=全部核
	ThinkLimit    float64 // 秒；<=0 用引擎默认预算
	ClickOffsetX  int     // 手动点击偏移（像素）
	ClickOffsetY  int
}

// Service 自动对弈服务（后台 goroutine）。
type Service struct {
	mu       sync.Mutex
	settings Settings

	logf func(format string, args ...any)
	emit func(kind string, payload any)
	mkAI func(kind string, thinkSec float64) engine.Engine
	ai   engine.Engine

	stopCh chan struct{}
	doneCh chan struct{}

	active       bool
	confirmed    *domain.BoardGrid
	lastRead     *domain.BoardGrid
	sameCount    int
	expected     int8 // 0=未知
	ourColor     int8
	colorLocked  bool
	learning     bool
	openingTried bool
	awaitR, awaitC int
	awaitDeadline time.Time
	calibX, calibY int   // 点击偏移自动校准（像素）
	offsetX, offsetY int // 手动偏移（设置项 click_offset_x/y）
	requestShot   bool
	clickRetries int
	giveups      int
	lostCount    int

	hwnd             uintptr
	originX, originY int
	lastDet          *vision.Detection
	points           *[13][13][2]int

	thinking   bool
	busy       bool
	// 测试钩子（nil 时用默认实现）
	clickCellFn   func(r, c int) bool
	verifyGhostFn func(r, c int) bool
	_thinkClosed bool
	thinkOnce  sync.Once
	_thinkRes   *engine.Move
	_thinkErr   error
	_thinkBoard *domain.BoardGrid
	_thinkTook  time.Duration
	_thinkDoneCh chan struct{}
	lastThrot  time.Time
}

// NewService 构造。
func NewService(settings Settings, logf func(string, ...any),
	emit func(kind string, payload any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if emit == nil {
		emit = func(string, any) {}
	}
	svc := &Service{
		settings:     settings,
		logf:         logf,
		emit:         emit,
		stopCh:       make(chan struct{}),
		awaitR:       -1,
		awaitC:       -1,
		_thinkDoneCh: make(chan struct{}),
	}
	switch settings.OurColor {
	case "1":
		svc.ourColor = domain.Black
	case "2":
		svc.ourColor = domain.White
	}
	return svc
}

// MkEngine 注入引擎工厂（测试用）；生产用默认。
func (s *Service) MkEngine(f func(kind string, think float64) engine.Engine) {
	s.mu.Lock()
	s.mkAI = f
	s.mu.Unlock()
}

func (s *Service) engineFor() (engine.Engine, error) {
	s.mu.Lock()
	if s.ai != nil {
		e := s.ai
		s.mu.Unlock()
		return e, nil
	}
	s.offsetX = s.settings.ClickOffsetX
	s.offsetY = s.settings.ClickOffsetY
	kind := s.settings.EngineKind
	think := s.settings.ThinkLimit
	mk := s.mkAI
	s.mu.Unlock()
	if mk != nil {
		e := mk(kind, think)
		s.mu.Lock()
		s.ai = e
		s.mu.Unlock()
		s.emit("engine", e.Name())
		return e, nil
	}
	if kind == "simple" {
		e := engine.NewSimpleAI()
		if err := e.Start(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.ai = e
		s.mu.Unlock()
		s.emit("engine", e.Name())
		return e, nil
	}
	if !engine.HasRapfi() {
		s.log("未找到 rapfi 引擎，回退内置简易引擎")
		e := engine.NewSimpleAI()
		if err := e.Start(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.ai = e
		s.mu.Unlock()
		s.emit("engine", e.Name())
		return e, nil
	}
	e := engine.NewRapfiAI(int(think*1000), func(f string, a ...any) { s.logf("[rapfi] "+f, a...) })
	if err := e.Start(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.ai = e
	s.mu.Unlock()
	s.emit("engine", e.Name())
	return e, nil
}

func (s *Service) log(format string, args ...any) { s.logf(format, args...) }

// Start 启动轮询 goroutine。
func (s *Service) Start() {
	go func() {
		defer s.log("识别线程已退出")
		for {
			select {
			case <-s.stopCh:
				return
			default:
			}
			start := time.Now()
			s.once()
			if d := pollInterval - time.Since(start); d > 0 {
				select {
				case <-s.stopCh:
					return
				case <-time.After(d):
				}
			}
		}
	}()
}

// Stop 停止并收尾。
func (s *Service) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
	}
	close(s.stopCh)
	s.mu.Lock()
	e := s.ai
	s.ai = nil
	s.mu.Unlock()
	if e != nil {
		e.Stop()
	}
}

// RequestTestShot 请求一次截图测试识别（轮询线程消费）。
func (s *Service) RequestTestShot() {
	s.mu.Lock()
	s.requestShot = true
	s.mu.Unlock()
}

// Awaiting 当前待确认落子格。
func (s *Service) Awaiting() (int, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.awaitR < 0 {
		return 0, 0, false
	}
	return s.awaitR, s.awaitC, true
}

// SetActive 开/关自动落子。
func (s *Service) SetActive(on bool) {
	s.mu.Lock()
	if s.active == on {
		s.mu.Unlock()
		return
	}
	s.active = on
	hasBoard := s.confirmed != nil
	s.mu.Unlock()
	s.emit("active", on)
	if on {
		s.log("自动落子已开启")
		s.logTurnStatus()
		if hasBoard {
			s.maybeAct()
		}
	} else {
		s.log("自动落子已暂停")
	}
}

// SetSettings 更新参数。
func (s *Service) SetSettings(st Settings) {
	s.mu.Lock()
	prev := s.settings
	s.settings = st
	colorChanged := st.OurColor != prev.OurColor
	engineChanged := st.EngineKind != prev.EngineKind
	threadsChanged := st.EngineThreads != prev.EngineThreads
	eng := s.ai
	s.mu.Unlock()

	if st.ClickOffsetX != prev.ClickOffsetX || st.ClickOffsetY != prev.ClickOffsetY {
		s.offsetX, s.offsetY = st.ClickOffsetX, st.ClickOffsetY
	}
	if engineChanged || threadsChanged {
		if eng != nil {
			eng.Stop()
		}
		s.mu.Lock()
		s.ai = nil
		s.mu.Unlock()
	}
	if colorChanged {
		s.mu.Lock()
		s.colorLocked = false
		switch st.OurColor {
		case "1":
			s.ourColor = domain.Black
		case "2":
			s.ourColor = domain.White
		default:
			s.ourColor = 0
		}
		s.mu.Unlock()
		s.recheckTurn()
	}
}



// ---- 轮询主循环 ----

func (s *Service) once() {
	if s.thinkPending() {
		s.finishThink()
	}
	if s.hwnd == 0 {
		w, err := capture.FindGameWindow()
		if err != nil {
			s.logThrottled("%v", err)
			time.Sleep(1500 * time.Millisecond)
			return
		}
		s.mu.Lock()
		s.hwnd = w.HWND
		s.mu.Unlock()
		s.logf("已找到游戏窗口: %s (%s)", w.Title, w.Process)
		s.emit("window", w.Title)
	}
	frame, ox, oy, err := capture.CaptureClient(s.hwnd)
	if err != nil {
		s.logThrottled("截屏失败: %v", err)
		return
	}
	s.mu.Lock()
	s.originX, s.originY = ox, oy
	s.mu.Unlock()

	withShot := false
	s.mu.Lock()
	if s.requestShot {
		s.requestShot = false
		withShot = true
	}
	s.mu.Unlock()
	_ = withShot

	det, derr := vision.Detect(frame)
	if derr != nil {
		s.mu.Lock()
		s.lostCount++
		lost := s.lostCount
		s.mu.Unlock()
		if lost == 3 {
			s.logf("未识别到棋盘（%v）。请进入五子棋对局界面，并避免窗口被完全遮挡。", derr)
		}
		return
	}
	s.mu.Lock()
	s.lostCount = 0
	s.lastDet = det
	last := s.lastRead
	same := s.sameCount
	s.mu.Unlock()
	s.storePoints(det)
	s.emit("board", det)

	board := domain.FromSlices(det.Board)
	if last != nil && board.Equal(last) {
		s.mu.Lock()
		s.sameCount = same + 1
		same = s.sameCount + 1
		s.mu.Unlock()
		if same < stableReads {
			return
		}
	} else {
		s.mu.Lock()
		s.lastRead = board
		s.sameCount = 1
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	confirmed := s.confirmed
	s.mu.Unlock()
	if confirmed == nil || !board.Equal(confirmed) {
		s.processChange(board)
	}
	s.checkClickTimeout()
}

func (s *Service) storePoints(det *vision.Detection) {
	var arr *[13][13][2]int
	for i := range det.Points {
		for j := range det.Points[i] {
			if arr == nil {
				arr = &[13][13][2]int{}
			}
			arr[i][j][0] = det.Points[i][j].X
			arr[i][j][1] = det.Points[i][j].Y
		}
	}
	s.mu.Lock()
	s.points = arr
	s.mu.Unlock()
}

// ---- 局面变化处理 ----

func (s *Service) processChange(stable *domain.BoardGrid) {
	s.mu.Lock()
	old := s.confirmed
	s.confirmed = stable
	s.mu.Unlock()

	if old == nil {
		s.logf("初始局面已锁定：%d 颗子", stable.StoneCount())
		s.mu.Lock()
		s.expected = stable.MoverByCounts()
		s.mu.Unlock()
		s.logTurnStatus()
		s.mu.Lock()
		act := s.active
		s.mu.Unlock()
		if act {
			s.maybeAct()
		}
		return
	}

	oldStones, newStones := old.StoneCount(), stable.StoneCount()
	var placements [][3]int
	removals := 0
	for r := 0; r < domain.BoardN; r++ {
		for c := 0; c < domain.BoardN; c++ {
			o, nw := old.At(r, c), stable.At(r, c)
			switch {
			case o == 0 && nw != 0:
				placements = append(placements, [3]int{r, c, int(nw)})
			case o != 0 && nw == 0:
				removals++
			}
		}
	}

	if newStones < oldStones || removals > 0 || len(placements) > 3 {
		s.logf("棋盘重置/大幅变化（%d -> %d 子），按新对局处理", oldStones, newStones)
		s.mu.Lock()
		s.awaitR, s.awaitC = -1, -1
		s.learning = false
		s.openingTried = false
		if s.settings.OurColor == "auto" {
			s.ourColor = 0
			s.colorLocked = false
		}
		s.expected = stable.MoverByCounts()
		eng := s.ai
		s.mu.Unlock()
		if eng != nil {
			eng.NewGame()
		}
		s.logTurnStatus()
		s.mu.Lock()
		act := s.active
		s.mu.Unlock()
		if act {
			s.maybeAct()
		}
		return
	}
	if len(placements) == 0 {
		s.log("棋盘出现非正常变化，已重新同步")
		return
	}
	if len(placements) == 1 &&
		s.filterPreview(placements[0][0], placements[0][1]) {
		return
	}
	// 时序修正：我方点击的落子必先于对方应招
	if s.awaitPending() {
		for i, p := range placements {
			if p[0] == s.awaitR && p[1] == s.awaitC {
				placements[0], placements[i] = placements[i], placements[0]
				break
			}
		}
	}
	for _, pl := range placements {
		s.handlePlacement(pl[0], pl[1], int8(pl[2]))
	}

	s.mu.Lock()
	act := s.active && s.expected != 0 && s.expected == s.ourColor
	s.mu.Unlock()
	if act {
		s.maybeAct()
	}
}

func (s *Service) awaitPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.awaitR >= 0
}

func (s *Service) handlePlacement(r, c int, color int8) {
	label := CoordLabel(r, c)
	s.mu.Lock()
	isAwait := s.awaitR == r && s.awaitC == c
	our := s.ourColor
	locked := s.colorLocked
	s.mu.Unlock()

	if isAwait {
		s.mu.Lock()
		det, pts := s.lastDet, s.points
		clickR, clickC := s.awaitR, s.awaitC
		s.mu.Unlock()
		if pts != nil && det != nil && (r != clickR || c != clickC) {
			dr, dc := r-clickR, c-clickC
			if abs(dr) <= 2 && abs(dc) <= 2 {
				// 游戏把我们的点击判到了 (r,c)：把点击向量向目标格修正
				px := func(rr, cc int) [2]int {
					return [2]int{pts[rr][cc][0], pts[rr][cc][1]}
				}
				a, b := px(clickR, clickC), px(r, c)
				s.mu.Lock()
				s.calibX += a[0] - b[0]
				s.calibY += a[1] - b[1]
				cx, cy := s.calibX, s.calibY
				s.mu.Unlock()
				s.logf("检测到点击落点偏移，已自动校准（offset %+d,%+d px）", cx, cy)
			}
		}
		s.mu.Lock()
		s.awaitR, s.awaitC = -1, -1
		s.clickRetries = 0
		if our != 0 && color != our && locked {
			s.logf("警告：点击后棋子颜色(%s)与记录不符，已纠正", colorName(color))
		}
		s.ourColor = color
		s.colorLocked = true
		s.learning = false
		s.expected = 3 - color
		s.mu.Unlock()
		s.emit("our_color", int(color))
		s.logf("我方落子 %s 已确认（我方执%s，已锁定）", label, colorName(color))
		return
	}
	if our != 0 {
		if color == our {
			s.logf("检测到我方手动落子 %s，继续接管后续着法", label)
		} else {
			s.logf("对方落子 %s", label)
		}
		s.mu.Lock()
		s.expected = 3 - color
		s.mu.Unlock()
		return
	}
	// 颜色未知（自动学习）
	s.mu.Lock()
	s.ourColor = 3 - color
	s.learning = true
	s.expected = s.ourColor
	tentative := s.ourColor
	s.mu.Unlock()
	s.emit("our_color", int(tentative))
	s.logf("观测到落子 %s（%s）：程序以%s接管应招；若点击未生效（说明这步是你手动下的），会自动纠正颜色并等待对方落子",
		label, colorName(color), colorName(tentative))
}

// filterPreview 新"落子"在鼠标交叉点 -> 移开复核。true=已忽略。
func (s *Service) filterPreview(r, c int) bool {
	s.mu.Lock()
	det := s.lastDet
	ox, oy := s.originX, s.originY
	awaiting := s.awaitR >= 0
	s.mu.Unlock()
	if det == nil || awaiting {
		return false
	}
	cx, cy, ok := capture.GetCursorPos()
	if !ok {
		return false
	}
	fx, fy := cx-ox, cy-oy
	bestR, bestC, bestD := 0, 0, 1<<30
	for i := 0; i < domain.BoardN; i++ {
		for j := 0; j < domain.BoardN; j++ {
			dx, dy := det.Points[i][j].X-fx, det.Points[i][j].Y-fy
			if d := dx*dx + dy*dy; d < bestD {
				bestD, bestR, bestC = d, i, j
			}
		}
	}
	if float64(bestD) > (det.Spacing*0.55)*(det.Spacing*0.55) ||
		bestR != r || bestC != c {
		return false
	}
	frame, nox, noy, err := capture.CaptureClient(s.hwnd)
	if err != nil {
		return false
	}
	det2, err := vision.Detect(frame)
	if err != nil {
		return false
	}
	s.mu.Lock()
	s.originX, s.originY = nox, noy
	s.lastDet = det2
	s.confirmed = domain.FromSlices(det2.Board)
	s.mu.Unlock()
	s.emit("board", det2)
	if det2.Board[r][c] == 0 {
		s.logf("忽略 %s 处的疑似悬停预览（请尽量让鼠标离开棋盘区域）", CoordLabel(r, c))
		return true
	}
	return false
}

// ---- 落子 ----

func (s *Service) maybeAct() {
	s.mu.Lock()
	if s.thinking || !s.active || s.awaitR >= 0 || s.confirmed == nil {
		s.mu.Unlock()
		return
	}
	// 先手开局：空盘第一步必为先行方 -> 中心 H7
	if s.confirmed.IsEmpty() && !s.openingTried {
		s.openingTried = true
		learning := s.ourColor == 0
		s.learning = learning
		s.mu.Unlock()
		s.logf("先手开局：落子中心 %s", CoordLabel(centerR, centerC))
		if s.clickCell(centerR, centerC) {
			s.mu.Lock()
			s.awaitR, s.awaitC = centerR, centerC
			s.awaitDeadline = time.Now().Add(clickTimeout)
			s.clickRetries = 0
			s.mu.Unlock()
		}
		return
	}
	if s.ourColor == 0 || s.expected == 0 || s.expected != s.ourColor {
		s.mu.Unlock()
		return
	}
	s.thinking = true
	s.thinkOnce = sync.Once{}
	s._thinkDoneCh = make(chan struct{})
	s._thinkClosed = false
	s._thinkRes = nil
	s._thinkErr = nil
	s._thinkBoard = s.confirmed.Clone()
	s.emit("status", "思考中…")
	grid := s._thinkBoard.ToSlices()
	color := int(s.ourColor)
	kind := s.settings.EngineKind
	limit := s.settings.ThinkLimit
	s.mu.Unlock()
	go s.thinkWorker(grid, color, kind, limit)
}

// 后台思考期间由轮询线程读这些字段（用锁保护）。
var (
	_ = sync.Mutex{}
)

func (s *Service) thinkWorker(grid [13][13]int8, color int, kind string, limit float64) {
	s.mu.Lock()
	d := time.Duration((rand.Float64()*0.6 + 0.7) * s.settings.MoveDelay * float64(time.Second))
	s.mu.Unlock()
	if d > 0 {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			select {
			case <-s.stopCh:
				s.mu.Lock()
				s.thinking = false
				s.mu.Unlock()
				s.signalThinkDone()
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	eng, err := s.engineFor()
	if err != nil {
		s.mu.Lock()
		s._thinkErr = err
		s.mu.Unlock()
		s.signalThinkDone()
		return
	}
	t0 := time.Now()
	mv, err := eng.BestMove(gridToSlice(grid), color, limit)
	s.mu.Lock()
	s._thinkRes = mv
	s._thinkErr = err
	s._thinkTook = time.Since(t0)
	s.mu.Unlock()
	s.signalThinkDone()
}

func gridToSlice(g [13][13]int8) [][]int8 {
	out := make([][]int8, 13)
	for r := range out {
		out[r] = make([]int8, 13)
		copy(out[r], g[r][:])
	}
	return out
}

func (s *Service) signalThinkDone() {
	s.mu.Lock()
	if s._thinkClosed {
		s.mu.Unlock()
		return
	}
	s._thinkClosed = true
	close(s._thinkDoneCh)
	s.mu.Unlock()
}

func (s *Service) thinkPending() bool {
	if !s.thinking {
		return false
	}
	select {
	case <-s._thinkDoneCh:
		return true
	default:
		return false
	}
}

func (s *Service) finishThink() {
	s.mu.Lock()
	s.thinking = false
	s.busy = false
	err := s._thinkErr
	res := s._thinkRes
	took := s._thinkTook
	board := s._thinkBoard
	confirmed := s.confirmed
	s.mu.Unlock()

	if err != nil {
		s.logf("引擎思考出错: %v", err)
		return
	}
	if res == nil {
		s.log("引擎无着法可下（棋盘已满？）")
		return
	}
	if !board.Equal(confirmed) {
		s.log("思考期间局面发生变化，重新计算…")
		s.maybeAct()
		return
	}
	label := CoordLabel(res.R, res.C)
	engName, _ := res.Info["engine"].(string)
	depth, hasDepth := res.Info["depth"].(int)
	extra := ""
	if hasDepth {
		extra = fmt.Sprintf(", 深度%d", depth)
	}
	s.logf("我方选择 %s（%s%s, 思考%dms）", label, engName, extra, took.Milliseconds())

	if !s.verifyNoGhost(res.R, res.C) {
		return
	}
	if !s.clickCell(res.R, res.C) {
		return
	}
	s.mu.Lock()
	s.awaitR, s.awaitC = res.R, res.C
	s.awaitDeadline = time.Now().Add(clickTimeout)
	s.clickRetries = 0
	s.mu.Unlock()
}

func (s *Service) clickCell(r, c int) bool {
	if s.clickCellFn != nil {
		return s.clickCellFn(r, c)
	}
	sx, sy, err := s.screenPoint(r, c, true)
	if err != nil {
		s.logf("计算点击坐标失败: %v", err)
		return false
	}
	capture.FocusWindow(s.hwnd)
	time.Sleep(100 * time.Millisecond)
	if !capture.IsForeground(s.hwnd) {
		capture.FocusWindow(s.hwnd)
		time.Sleep(100 * time.Millisecond)
		if !capture.IsForeground(s.hwnd) {
			_, _, cw, _, _ := capture.ClientRectScreen(s.hwnd)
			capture.ClickAt(s.originX+cw/warmXOffsetRatio, s.originY+8, clickHold)
			time.Sleep(150 * time.Millisecond)
			s.log("窗口未在前台，先预热点击一次")
		}
	}
	capture.ClickAt(sx, sy, clickHold)
	s.logf("已点击 %s，等待棋盘确认…", CoordLabel(r, c))
	return true
}

func (s *Service) screenPoint(r, c int, refresh bool) (int, int, error) {
	if refresh {
		frame, ox, oy, err := capture.CaptureClient(s.hwnd)
		if err != nil {
			return 0, 0, err
		}
		det, err := vision.Detect(frame)
		if err != nil {
			return 0, 0, err
		}
		s.mu.Lock()
		s.originX, s.originY = ox, oy
		s.mu.Unlock()
		s.storePoints(det)
	}
	s.mu.Lock()
	pts := s.points
	ox, oy := s.originX, s.originY
	cx, cy := s.calibX, s.calibY
	offX, offY := s.offsetX, s.offsetY
	s.mu.Unlock()
	if pts == nil {
		return 0, 0, errors.New("尚无交叉点坐标")
	}
	return ox + pts[r][c][0] + cx + offX, oy + pts[r][c][1] + cy + offY, nil
}

func (s *Service) verifyNoGhost(targetR, targetC int) bool {
	if s.verifyGhostFn != nil {
		return s.verifyGhostFn(targetR, targetC)
	}
	sx, sy, err := s.screenPoint(targetR, targetC, true)
	if err != nil {
		return true
	}
	capture.MoveMouse(sx, sy)
	time.Sleep(verifyHoverDelay)
	frame, nox, noy, err := capture.CaptureClient(s.hwnd)
	if err != nil {
		return true
	}
	det2, err := vision.Detect(frame)
	if err != nil {
		return true
	}
	s.mu.Lock()
	s.originX, s.originY = nox, noy
	s.lastDet = det2
	confirmed := s.confirmed
	s.mu.Unlock()
	s.emit("board", det2)
	if confirmed == nil {
		return true
	}
	var ghost []string
	for r := 0; r < domain.BoardN; r++ {
		for c := 0; c < domain.BoardN; c++ {
			if confirmed.At(r, c) != 0 && det2.Board[r][c] == 0 &&
				!(r == targetR && c == targetC) {
				ghost = append(ghost, CoordLabel(r, c))
			}
		}
	}
	if len(ghost) == 0 {
		return true
	}
	s.logf("%v 处的棋子在复核时消失，疑似鼠标悬停预览，忽略本次落子", ghost)
	s.mu.Lock()
	s.expected = 3 - s.ourColor
	s.mu.Unlock()
	warm := capture.WarmPoint(s.hwnd)
	capture.MoveMouse(warm.X, warm.Y)
	time.Sleep(verifyHoverDelay)
	if frame3, _, _, err := capture.CaptureClient(s.hwnd); err == nil {
		if det3, err := vision.Detect(frame3); err == nil {
			s.mu.Lock()
			s.confirmed = domain.FromSlices(det3.Board)
			s.mu.Unlock()
			s.emit("board", det3)
			return false
		}
	}
	s.mu.Lock()
	s.confirmed = domain.FromSlices(det2.Board)
	s.mu.Unlock()
	return false
}

func (s *Service) checkClickTimeout() {
	s.mu.Lock()
	if s.awaitR < 0 || time.Now().Before(s.awaitDeadline) {
		s.mu.Unlock()
		return
	}
	r, c := s.awaitR, s.awaitC
	confirmed := s.confirmed
	our := s.ourColor
	locked := s.colorLocked
	learning := s.learning
	retries := s.clickRetries
	s.mu.Unlock()
	if confirmed != nil && confirmed.At(r, c) != 0 {
		s.mu.Lock()
		s.awaitR, s.awaitC = -1, -1
		s.mu.Unlock()
		return
	}
	label := CoordLabel(r, c)

	if learning && !locked && our != 0 {
		observed := 3 - our
		s.mu.Lock()
		s.awaitR, s.awaitC = -1, -1
		s.learning = false
		s.clickRetries = 0
		s.ourColor = observed
		s.expected = 3 - observed
		s.mu.Unlock()
		s.emit("our_color", int(observed))
		s.logf("点击未生效：判定首次落子为你手动所下，我方颜色已锁定为%s，等待对方落子后自动接管",
			colorName(observed))
		return
	}
	if learning && our == 0 {
		s.mu.Lock()
		s.awaitR, s.awaitC = -1, -1
		s.learning = false
		s.clickRetries = 0
		s.mu.Unlock()
		s.log("开局试探未生效（还没轮到我方），等待对方落子后自动接管")
		return
	}
	if retries < clickRetriesMax {
		s.mu.Lock()
		s.clickRetries = retries + 1
		s.mu.Unlock()
		s.logf("%s 未确认（%d/%d），重试点击…", label, retries+1, clickRetriesMax)
		if s.clickCell(r, c) {
			s.mu.Lock()
			s.awaitDeadline = time.Now().Add(clickTimeout)
			s.mu.Unlock()
			return
		}
	}
	s.mu.Lock()
	s.awaitR, s.awaitC = -1, -1
	s.clickRetries = 0
	s.giveups++
	gu := s.giveups
	expected := confirmed.MoverByCounts()
	s.expected = expected
	s.mu.Unlock()
	if gu >= giveupLimit {
		s.mu.Lock()
		s.expected = 0
		s.mu.Unlock()
		s.log("连续多次落子未生效，已暂停自动出手。请检查游戏窗口是否被遮挡/是否弹窗，然后点\"停止\"再点\"开始自动对弈\"重新接管")
	} else {
		s.log("落子未生效，将按当前轮次自动重试")
	}
}

// ---- 辅助 ----

func (s *Service) logTurnStatus() {
	s.mu.Lock()
	confirmed := s.confirmed
	expected := s.expected
	our := s.ourColor
	s.mu.Unlock()
	if confirmed == nil {
		return
	}
	b, w := confirmed.ColorCount(domain.Black), confirmed.ColorCount(domain.White)
	if expected == 0 {
		expected = confirmed.MoverByCounts()
	}
	if expected == 0 {
		s.logf("当前局面：黑%d 白%d，轮到先行方（等待下一次落子后自动判定）", b, w)
		return
	}
	ours := ""
	if expected == our {
		ours = "（轮到我方）"
	}
	s.logf("当前局面：黑%d 白%d，当前轮到：%s%s", b, w, colorName(expected), ours)
}

func (s *Service) recheckTurn() {
	s.mu.Lock()
	confirmed := s.confirmed
	s.mu.Unlock()
	if confirmed == nil {
		return
	}
	s.mu.Lock()
	if s.expected == 0 {
		s.expected = confirmed.MoverByCounts()
	}
	s.mu.Unlock()
	s.logTurnStatus()
	s.mu.Lock()
	act := s.active && s.expected != 0 && s.expected == s.ourColor
	s.mu.Unlock()
	if act {
		s.maybeAct()
	}
}

func (s *Service) logThrottled(format string, args ...any) {
	s.mu.Lock()
	now := time.Now()
	if now.Sub(s.lastThrot) > 5*time.Second {
		s.lastThrot = now
		s.mu.Unlock()
		s.logf(format, args...)
		return
	}
	s.mu.Unlock()
}

func colorName(c int8) string {
	switch c {
	case 1:
		return "黑"
	case 2:
		return "白"
	}
	return "?"
}


func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
