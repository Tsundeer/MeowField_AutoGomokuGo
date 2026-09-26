package engine

import (
	"os"
	"path/filepath"
	"strings"
)

// PatchJaxDevice 设置 JAX 的推理设备（configs/config.toml [eval] device）。
// cpu / cuda / tensorrt。cuda 需系统 CUDA 运行时，tensorrt 需 TensorRT 8.6。
func PatchJaxDevice(device string) error {
	f := DetectEngines()
	if f.JaxDir == "" {
		return os.ErrNotExist
	}
	path := filepath.Join(f.JaxDir, "configs", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	inEval := false
	found := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "[") {
			inEval = trimmed == "[eval]"
			continue
		}
		if inEval && strings.HasPrefix(trimmed, "device") {
			lines[i] = `device = "` + device + `"`
			found = true
			break
		}
	}
	if !found {
		return nil // 配置无 device 字段则不动
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
