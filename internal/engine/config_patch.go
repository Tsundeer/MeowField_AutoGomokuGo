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

// PatchEngineMate 必胜证明收尾层数开关：
// fast=true（推荐）-> 4 层，找到必胜快速出招；
// fast=false -> 24 层，完整证明更强但耗时可能远超思考上限。
func PatchEngineMate(fast bool) error {
	v := 24
	if fast {
		v = 4
	}
	return patchConfigLine(`(?m)^num_iteration_after_mate\s*=\s*\d+`,
		"num_iteration_after_mate = "+itoa(v))
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
