// Package engine：对弈引擎。Rapfi 子进程客户端（Gomocup 协议）与内置简易引擎。
package engine

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Move 一步着法。
type Move struct {
	R, C int
	Info map[string]any
}

// Engine 引擎统一接口。
type Engine interface {
	Name() string
	Start() error
	Stop()
	NewGame()
	BestMove(grid [][]int8, myColor int, timeLimitSec float64) (*Move, error)
}

// ---- 引擎定位 ----

var titleRe = regexp.MustCompile(`(?i)rapfi`)

// FindRapfiExes 定位 engines/ 下的 rapfi 可执行文件（按指令集优先排序）。
// 优先级与 Python 版相反处：avx2 实测最稳放最前。
func FindRapfiExes() []string {
	dir := EnginesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(name), ".exe") &&
			strings.Contains(strings.ToLower(name), "rapfi") {
			found = append(found, filepath.Join(dir, name))
		}
	}
	pref := []string{"avx2", "sse", "avxvnni", "avx512vnni", "avx512"}
	var ordered []string
	for _, key := range pref {
		for _, p := range found {
			if strings.Contains(strings.ToLower(filepath.Base(p)), key) {
				ordered = append(ordered, p)
			}
		}
	}
	for _, p := range found {
		dup := false
		for _, o := range ordered {
			if o == p {
				dup = true
				break
			}
		}
		if !dup {
			ordered = append(ordered, p)
		}
	}
	return ordered
}

var enginesDirOverride string

func setEnginesDir(d string) { enginesDirOverride = d }

// EnginesDir 引擎目录：优先 exe 同级 engines/（用户可替换），
// 否则开发环境仓库根 engines/。
func EnginesDir() string {
	if enginesDirOverride != "" {
		return enginesDirOverride
	}
	exe, err := os.Executable()
	if err == nil {
		cand := filepath.Join(filepath.Dir(exe), "engines")
		if st, err2 := os.Stat(cand); err2 == nil && st.IsDir() {
			return cand
		}
		if st, err2 := os.Stat(filepath.Join(filepath.Dir(exe), "_internal", "engines")); err2 == nil && st.IsDir() {
			return filepath.Join(filepath.Dir(exe), "_internal", "engines")
		}
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, "engines")
}

// HasRapfi 是否存在 rapfi 引擎。
func HasRapfi() bool { return len(FindRapfiExes()) > 0 }

var moveRe = regexp.MustCompile(`^\s*(\d{1,3})[ ,]+(\d{1,3})\s*$`)

// RapfiAI Rapfi 子进程引擎（Gomocup/piskvork 协议）。
type RapfiAI struct {
	TurnTimeMS int    // 默认每步思考预算（毫秒）
	MateRush   bool   // true=必胜快速落子(config 4)；false=完整证明(config 24)
	OnMate     func(mate string) // 检测到必胜线时回调（如 "+M29"）
	logf       func(string, ...any)
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      *bufio.Writer
	stdout     *bufio.Reader
	lineCh     chan string // 常驻 reader 输出（消除多 goroutine 竞争读）
	lastSpeed  time.Time
	lastMate   string // 本步已报告过的必胜线（去重）
}

// NewRapfiAI 构造（启动延迟到首次 Start/BestMove）。
func NewRapfiAI(turnTimeMS int, mateRush bool, logf func(string, ...any)) *RapfiAI {
	if turnTimeMS <= 0 {
		turnTimeMS = 20000
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &RapfiAI{TurnTimeMS: turnTimeMS, MateRush: mateRush, logf: logf}
}

var mateRe = regexp.MustCompile(`Eval ([+-]M\d+)`)

// extractMate 从 Depth 行提取必胜线（如 "+M29"/"-M20"）。
func extractMate(line string) string {
	m := mateRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

func (a *RapfiAI) Name() string { return "rapfi" }

func (a *RapfiAI) send(line string) {
	a.stdin.WriteString(line + "\n")
	a.stdin.Flush()
}

// readline 读引擎输出直到出现着法行；跳过 MESSAGE/Depth/Speed 噪声（Speed 节流）。
// readline 读引擎输出直到出现着法行。
// 超时策略：先发 STOP 让引擎中断搜索并交出当前最优着法（协议标准做法），
// 再给 10s 宽限读取；仍无结果才报错。
// MESSAGE Depth 明细不进日志；Speed 节流；其余 MESSAGE 原样。
func (a *RapfiAI) readline(timeout time.Duration) (string, error) {
	if a.lineCh == nil { // 兜底（未启动 reader）
		return "", errors.New("引擎输出流未启动")
	}
	softDeadline := time.Now().Add(timeout)
	stopped := false
	for {
		remain := time.Until(softDeadline)
		if remain <= 0 && stopped {
			return "", errors.New("引擎超时未返回着法")
		}
		select {
		case line, ok := <-a.lineCh:
			if !ok {
				return "", errors.New("引擎进程已退出")
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "?") {
				a.logf("错误应答: %s", line)
				return "", fmt.Errorf("引擎报错: %s", line)
			}
			if m := moveRe.FindStringSubmatch(line); m != nil {
				return line, nil
			}
			switch {
			case strings.HasPrefix(line, "Speed"):
				if time.Since(a.lastSpeed) > 3*time.Second {
					a.lastSpeed = time.Now()
					a.logf("%s", line)
				}
			case strings.HasPrefix(line, "MESSAGE Depth"):
				// 搜索明细不逐行写日志；但检测到必胜线要显式报告
				if mate := extractMate(line); mate != "" && mate != a.lastMate {
					a.lastMate = mate
					if a.OnMate != nil {
						a.OnMate(mate)
					}
				}
			case strings.HasPrefix(line, "MESSAGE"):
				a.logf("%s", line)
			}
		case <-time.After(maxDuration(50*time.Millisecond, remain)):
			if !stopped {
				stopped = true
				a.logf("思考超时，发送 STOP 请求引擎交出当前最优着法…")
				a.mu.Unlock()
				a.send("STOP")
				a.mu.Lock()
				softDeadline = time.Now().Add(10 * time.Second)
			}
		}
	}
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func (a *RapfiAI) tryStart(exe string) error {
	if err := PatchEngineMate(a.MateRush); err != nil {
		a.logf("config.toml 调整失败（不影响启动）: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // 与 Python 版一致（合并）
	a.cmd, a.stdin, a.stdout = cmd, bufio.NewWriter(stdin), bufio.NewReader(stdout)
	if err := cmd.Start(); err != nil {
		return err
	}
	// 常驻 reader：所有输出经 channel 串行分发，杜绝读竞争
	a.lineCh = make(chan string, 64)
	go func() {
		for {
			line, err := a.stdout.ReadString('\n')
			if err != nil {
				close(a.lineCh)
				return
			}
			select {
			case a.lineCh <- line:
			default: // 满则丢弃最旧行，保持不阻塞
				select {
				case <-a.lineCh:
				default:
				}
				select {
				case a.lineCh <- line:
				default:
				}
			}
		}
	}()
	a.send(fmt.Sprintf("START 13"))
	if err := a.expectOK(10 * time.Second); err != nil {
		return err
	}
	// 不限时基线 + 每步预算（运行时可经 best_move 调整）
	a.send("INFO timeout_match 0")
	a.send(fmt.Sprintf("INFO timeout_turn %d", a.TurnTimeMS))
	return nil
}

func (a *RapfiAI) expectOK(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case line, ok := <-a.lineCh:
			if !ok {
				return errors.New("引擎进程已退出")
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			low := strings.ToLower(line)
			if strings.HasPrefix(low, "ok") {
				return nil
			}
			if strings.HasPrefix(low, "?") {
				return fmt.Errorf("引擎报错: %s", line)
			}
			a.logf("%s", line)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("引擎初始化无应答")
}

// Start 启动引擎（按候选列表依次尝试，直到一个能应答）。
func (a *RapfiAI) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.startLocked()
}

func (a *RapfiAI) startLocked() error {
	candidates := FindRapfiExes()
	if len(candidates) == 0 {
		return errors.New("未找到 rapfi 引擎，请将引擎解压到 engines/ 目录")
	}
	var lastErr error
	for _, exe := range candidates {
		err := a.tryStart(exe)
		if err == nil {
			a.logf("[rapfi] 引擎已启动: %s", filepath.Base(exe))
			return nil
		}
		lastErr = err
		a.logf("[rapfi] %s 启动失败: %v", filepath.Base(exe), err)
		a.kill()
	}
	return fmt.Errorf("所有 rapfi 引擎均无法启动: %w", lastErr)
}

func (a *RapfiAI) kill() {
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
	a.cmd = nil
}

// Stop 结束引擎进程。
func (a *RapfiAI) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc_alive() {
		a.send("END")
	}
	time.Sleep(200 * time.Millisecond)
	a.kill()
}

func (a *RapfiAI) proc_alive() bool {
	return a.cmd != nil && a.cmd.Process != nil
}

// NewGame 重开一局（重置引擎内部状态）。
func (a *RapfiAI) NewGame() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.proc_alive() {
		_ = a.startLocked()
		return
	}
	a.send(fmt.Sprintf("START 13"))
	_ = a.expectOK(15 * time.Second)
}

// BestMove 请求引擎计算着法。grid 为 13x13（0空1黑2白）。
func (a *RapfiAI) BestMove(grid [][]int8, myColor int, timeLimitSec float64) (*Move, error) {
	n := len(grid)
	total := 0
	for _, row := range grid {
		for _, v := range row {
			total += int(v)
		}
	}
	if total == 0 {
		m := n / 2
		return &Move{R: m, C: m, Info: map[string]any{"note": "开局天元"}}, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastMate = ""
	if !a.proc_alive() {
		if err := a.startLocked(); err != nil {
			return nil, err
		}
	}
	budget := a.TurnTimeMS
	if timeLimitSec > 0 {
		budget = int(timeLimitSec * 1000)
	}
	// 每步必下发预算：引擎的 timeout_turn 可随时调整，无需重启
	a.send(fmt.Sprintf("INFO timeout_turn %d", budget))
	a.send("INFO rule 0") // 自由规则（无禁手）
	a.send("BOARD")
	stones := 0
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			v := int(grid[r][c])
			if v != 0 {
				t := 1 // 我方
				if v != myColor {
					t = 2
				}
				a.send(fmt.Sprintf("%d,%d,%d", c, r, t))
				stones++
			}
		}
	}
	a.send("DONE")
	timeout := time.Duration(budget*3+30000) * time.Millisecond
	line, err := a.readline(timeout)
	if err != nil {
		return nil, err
	}
	m := moveRe.FindStringSubmatch(line)
	if m == nil {
		return nil, fmt.Errorf("rapfi 输出无法解析: %q", line)
	}
	x, _ := strconv.Atoi(m[1])
	y, _ := strconv.Atoi(m[2])
	if x < 0 || x >= n || y < 0 || y >= n {
		return nil, fmt.Errorf("rapfi 返回非法坐标: (%d,%d)", x, y)
	}
	return &Move{R: y, C: x, Info: map[string]any{"engine": "rapfi"}}, nil
}
