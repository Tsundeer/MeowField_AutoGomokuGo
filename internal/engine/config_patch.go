package engine

import (
	"os"
	"path/filepath"
	"regexp"
)

// PatchEngineThreads 设置引擎线程数（0=全部逻辑核）。
func PatchEngineThreads(threads int) error {
	return patchConfigLine(`(?m)^default_thread_num\s*=\s*\d+`,
		"default_thread_num = "+itoa(threads))
}

// PatchEngineMate 找到必胜后快速收尾（默认 24 层迭代会无视时间限制，
// 导致深层必胜局面"到我方下棋却不动"）。
func PatchEngineMate() error {
	return patchConfigLine(`(?m)^num_iteration_after_mate\s*=\s*\d+`,
		"num_iteration_after_mate = 4")
}

func patchConfigLine(pattern, repl string) error {
	path := filepath.Join(EnginesDir(), "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(data)
	out, changed := reReplace(s, pattern, repl)
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

func reReplace(s, pattern, repl string) (string, bool) {
	re := regexp.MustCompile(pattern)
	if !re.MatchString(s) {
		return s, false
	}
	return re.ReplaceAllString(s, repl), true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
