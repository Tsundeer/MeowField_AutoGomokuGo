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

// EnginesDir 引擎目录：优先 exe 同级 engines/（用户可替换），
// 否则开发环境仓库根 engines/。
func EnginesDir() string {
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
	logf       func(string, ...any)
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      *bufio.Writer
	stdout     *bufio.Reader
}

// NewRapfiAI 构造（启动延迟到首次 Start/BestMove）。
func NewRapfiAI(turnTimeMS int, logf func(string, ...any)) *RapfiAI {
	if turnTimeMS <= 0 {
		turnTimeMS = 20000
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &RapfiAI{TurnTimeMS: turnTimeMS, logf: logf}
}

func (a *RapfiAI) Name() string { return "rapfi" }

func (a *RapfiAI) send(line string) {
	a.stdin.WriteString(line + "\n")
	a.stdin.Flush()
}

// readline 读引擎输出直到出现着法行；跳过 MESSAGE/Depth/Speed 噪声（Speed 节流）。
func (a *RapfiAI) readline(timeout time.Duration) (string, error) {
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	var lastSpeed time.Time
	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return "", errors.New("引擎超时未返回")
		}
		type res struct {
			line string
			err  error
		}
		ch := make(chan res, 1)
		go func() {
			line, err := a.stdout.ReadString('\n')
			ch <- res{line, err}
		}()
		var line string
		var err error
		if timeout > 0 {
			remain := time.Until(deadline)
			if remain <= 0 {
				return "", errors.New("引擎超时未返回")
			}
			select {
			case r := <-ch:
				line, err = r.line, r.err
			case <-time.After(remain):
				return "", errors.New("引擎超时未返回")
			}
		} else {
			r := <-ch
			line, err = r.line, r.err
		}
		if err != nil {
			if a.cmd != nil && a.cmd.ProcessState != nil {
				return "", errors.New("引擎进程已退出")
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "?") {
			a.logf("[rapfi] 错误应答: %s", line)
			return "", fmt.Errorf("引擎报错: %s", line)
		}
		if m := moveRe.FindStringSubmatch(line); m != nil {
			return line, nil
		}
		switch {
		case strings.HasPrefix(line, "Speed"):
			if time.Since(lastSpeed) > 3*time.Second {
				lastSpeed = time.Now()
				a.logf("[rapfi] %s", line)
			}
		case strings.HasPrefix(line, "MESSAGE"):
			a.logf("[rapfi] %s", line)
		}
	}
}

func (a *RapfiAI) tryStart(exe string) error {
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
		line, err := a.stdout.ReadString('\n')
		if err != nil {
			if a.cmd != nil && a.cmd.ProcessState != nil {
				return errors.New("引擎进程已退出")
			}
			time.Sleep(10 * time.Millisecond)
			continue
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
		a.logf("[rapfi] %s", line)
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
