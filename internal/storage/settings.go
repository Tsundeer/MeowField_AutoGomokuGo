// Package storage：用户设置（%LocalAppData%，原子写入 + 备份）。
package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SchemaVersion 设置结构版本。
const SchemaVersion = 1

// Defaults 默认设置（镜像 Python 版）。
var Defaults = map[string]any{
	"schema_version": SchemaVersion,
	"our_color":      "auto",
	"engine":         "auto",
	"move_delay":     1.0,
	"engine_threads": 0,
	"think_limit":    20.0,
	"theme":          "dark",
	"click_offset_x": 0,
	"click_offset_y": 0,
	"mate_rush":      true,
}

// AppDataDir %LocalAppData%\MeowField_AutoGomoku。
func AppDataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = home
	}
	d := filepath.Join(base, "MeowField_AutoGomoku")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// LogsDir 日志目录。
func LogsDir() string {
	d := filepath.Join(AppDataDir(), "logs")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// DebugDir 调试截图目录。
func DebugDir() string {
	d := filepath.Join(AppDataDir(), "debug")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// SettingsPath 设置文件路径。
func SettingsPath() string { return filepath.Join(AppDataDir(), "settings.json") }

// LoadSettings 读取设置（缺省合并；兼容 v1.0 项目根 config.json）。
func LoadSettings() map[string]any {
	merged := map[string]any{}
	for k, v := range Defaults {
		merged[k] = v
	}
	for _, p := range []string{SettingsPath(),
		filepath.Join(".", "config.json")} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil || m == nil {
			continue
		}
		for k := range Defaults {
			if v, ok := m[k]; ok {
				merged[k] = v
			}
		}
		break
	}
	merged["schema_version"] = SchemaVersion
	return merged
}

// SaveSettings 原子写入（临时文件 -> replace，保留 .bak）。
func SaveSettings(settings map[string]any) error {
	data := map[string]any{}
	for k, v := range Defaults {
		data[k] = v
	}
	for k := range Defaults {
		if v, ok := settings[k]; ok {
			data[k] = v
		}
	}
	data["schema_version"] = SchemaVersion
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	path := SettingsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	bak := path + ".bak"
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(bak)
		_ = os.Rename(path, bak)
	}
	return os.Rename(tmp, path)
}
