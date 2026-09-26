package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPatchEngineMate(t *testing.T) {
	// 用临时引擎目录验证 config.toml 补丁
	dir := t.TempDir()
	toml := "num_iteration_after_mate = 24\r\ndefault_thread_num = 1\r\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDir := EnginesDir()
	setEnginesDir(dir)
	defer setEnginesDir(oldDir)

	if err := PatchEngineMate(true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if !strings.Contains(string(data), "num_iteration_after_mate = 4") {
		t.Fatalf("mate patch failed: %q", string(data))
	}
	if err := PatchEngineThreads(8); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "config.toml"))
	if !strings.Contains(string(data), "default_thread_num = 8") {
		t.Fatalf("threads patch failed: %q", string(data))
	}
}

func TestRapfiStopFallback(t *testing.T) {

	ai := NewRapfiAI(500, true, nil) // 极短预算验证 STOP 交出着法
	if err := ai.Start(); err != nil {
		t.Skip("引擎启动失败:", err)
	}
	defer ai.Stop()
	g := make([][]int8, 13)
	for r := range g {
		g[r] = make([]int8, 13)
	}
	g[7][7] = 1 // 黑 H8，我方白应招（无必胜、无威胁的散开局）
	t0 := time.Now()
	mv, err := ai.BestMove(g, 2, 0.5)
	dt := time.Since(t0)
	if err != nil {
		t.Fatalf("BestMove: %v", err)
	}
	t.Logf("0.5s 预算实际耗时 %.2fs -> (%d,%d)", dt.Seconds(), mv.R, mv.C)
	if dt > 15*time.Second {
		t.Fatalf("预算 0.5s 却耗时 %.2fs（STOP 兜底未生效）", dt.Seconds())
	}
}
