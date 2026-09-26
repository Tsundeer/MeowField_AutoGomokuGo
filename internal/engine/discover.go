package engine

import (
	"os"
	"path/filepath"
	"strings"
)

// EngineKind 支持的引擎类型。
type EngineKind string

const (
	KindRapfi      EngineKind = "rapfi"      // CPU NNUE（Gomocup 冠军）
	KindJax        EngineKind = "jax"        // MCTS + ONNX，支持 CUDA/TensorRT
	KindKatagomo   EngineKind = "katagomo"   // KataGo 系（CUDA），用户自备
	KindAlphagomoku EngineKind = "alphagomoku" // OpenCL，仅 15x15/20x20（不支持 13x13，集成但标记受限）
	KindSimple     EngineKind = "simple"     // 内置兜底
)

// Found 引擎探测结果。
type Found struct {
	Rapfi      []string // 候选 exe（指令集优先排序）
	Jax        string   // pbrain-Jax.exe 路径
	JaxDir     string
	Katagomo   string   // katagomo exe 路径
	Alphagomoku string
}

var enginesDirOverride string

func setEnginesDir(d string) { enginesDirOverride = d }

func hasDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func findExeIn(dirs []string, match func(string) bool) string {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(name), ".exe") &&
				match(strings.ToLower(name)) {
				return filepath.Join(dir, name)
			}
		}
	}
	return ""
}

// DetectEngines 扫描 engines/ 目录（含各引擎子目录），返回各引擎可用性。
// 兼容旧布局：rapfi exe 直接位于 engines/ 根。
func DetectEngines() Found {
	var f Found
	base := EnginesDir()
	dirs := []string{
		base,
		filepath.Join(base, "jax"),
		filepath.Join(base, "katagomo"),
		filepath.Join(base, "alphagomoku"),
	}
	// rapfi：根目录或 engines/rapfi/ 子目录
	rapfiDirs := append([]string{base, filepath.Join(base, "rapfi")}, dirs...)
	f.Rapfi = findRapfiExesIn(rapfiDirs)
	f.Jax = findExeIn([]string{filepath.Join(base, "jax"), base},
		func(n string) bool { return strings.Contains(n, "jax") })
	if f.Jax != "" {
		f.JaxDir = filepath.Dir(f.Jax)
	}
	f.Katagomo = findExeIn([]string{filepath.Join(base, "katagomo"), base},
		func(n string) bool {
			return strings.Contains(n, "katagomo") || strings.Contains(n, "katago")
		})
	f.Alphagomoku = findExeIn([]string{filepath.Join(base, "alphagomoku"), base},
		func(n string) bool { return strings.Contains(n, "alphagomoku") })
	return f
}

// findRapfiExesIn 旧逻辑的目录参数化版本（指令集优先排序）。
func findRapfiExesIn(dirs []string) []string {
	var found []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(name), ".exe") &&
				strings.Contains(strings.ToLower(name), "rapfi") {
				found = append(found, filepath.Join(dir, name))
			}
		}
	}
	pref := []string{"avx2", "sse", "avxvnni", "avx512vnni", "avx512"}
	var ordered []string
	seen := map[string]bool{}
	for _, key := range pref {
		for _, p := range found {
			if strings.Contains(strings.ToLower(filepath.Base(p)), key) && !seen[p] {
				ordered = append(ordered, p)
				seen[p] = true
			}
		}
	}
	for _, p := range found {
		if !seen[p] {
			ordered = append(ordered, p)
			seen[p] = true
		}
	}
	return ordered
}

// HasRapfi 是否存在 rapfi 引擎。
func HasRapfi() bool { return len(DetectEngines().Rapfi) > 0 }
