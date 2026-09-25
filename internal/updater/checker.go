// Package updater：GitHub Release 更新检查（多端点降级 + 本地缓存）。
package updater

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"MeowField_AutoGomokuGo/internal/storage"
)

const (
	repo       = "Tsundeer/MeowField_AutoGomoku"
	apiURL     = "https://api.github.com/repos/" + repo + "/releases/latest"
	atomURL    = "https://github.com/" + repo + "/releases.atom"
	pageURL    = "https://github.com/" + repo + "/releases/latest"
	releaseURL = "https://github.com/" + repo + "/releases"
	userAgent  = "MeowField-AutoGomoku"
	cacheTTL   = time.Hour
)

var (
	tagRe    = regexp.MustCompile(`releases/tag/([^"?<>\s]+)`)
	cacheMu  sync.Mutex
	cacheMem map[string]any
)

func parseVersion(v string) []int {
	m := regexp.MustCompile(`(\d+(?:\.\d+)*)`).FindStringSubmatch(v)
	if m == nil {
		return []int{0}
	}
	var out []int
	for _, p := range strings.Split(m[1], ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func newer(a, b []int) bool {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func cachePath() string { return filepath.Join(storage.AppDataDir(), "update_cache.json") }

func cacheLoad() map[string]any {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheMem != nil {
		return cacheMem
	}
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	cacheMem = m
	return m
}

func cacheSave(m map[string]any) {
	cacheMu.Lock()
	cacheMem = m
	cacheMu.Unlock()
	body, _ := json.Marshal(m)
	_ = os.WriteFile(cachePath(), body, 0o644)
}

func httpGet(url string, headers map[string]string, timeout time.Duration) (int, http.Header, []byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header, body, nil
}

// Result 检查结果。
type Result struct {
	HasUpdate bool   `json:"has_update"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
	Notes     string `json:"notes"`
	Cached    bool   `json:"cached"`
	Error     string `json:"error,omitempty"`
}

// Check 查询最新版本。
// 端点降级：API(ETag 条件请求，304 不计匿名限额) -> atom -> HTML latest。
// 结果缓存 1 小时；全部失败时回退缓存。
func Check(current string, force bool) Result {
	res := Result{Latest: current, URL: releaseURL}

	cache := cacheLoad()
	fresh := false
	var etag string
	if cache != nil {
		if ts, _ := cache["ts"].(float64); ts > 0 &&
			time.Since(time.UnixMilli(int64(ts))) < cacheTTL {
			fresh = true
		}
		etag, _ = cache["etag"].(string)
	}
	if !force && fresh {
		if tag, _ := cache["tag"].(string); tag != "" {
			res.Latest = tag
			res.Cached = true
			if u, _ := cache["url"].(string); u != "" {
				res.URL = u
			}
			res.HasUpdate = newer(parseVersion(tag), parseVersion(current))
			return res
		}
	}

	headers := map[string]string{"Accept": "application/vnd.github+json"}
	if etag != "" {
		headers["If-None-Match"] = etag
	}
	code, hdrs, body, err := httpGet(apiURL, headers, 8*time.Second)
	switch {
	case err == nil && code == 304 && cache != nil:
		if tag, _ := cache["tag"].(string); tag != "" {
			res.Latest = tag
			res.HasUpdate = newer(parseVersion(tag), parseVersion(current))
			return res
		}
	case err == nil && code == 200:
		var data struct {
			TagName string `json:"tag_name"`
			HTMLURL string `json:"html_url"`
			Body    string `json:"body"`
		}
		if json.Unmarshal(body, &data) == nil && data.TagName != "" {
			res.Latest = data.TagName
			if data.HTMLURL != "" {
				res.URL = data.HTMLURL
			}
			res.Notes = data.Body
			if len(res.Notes) > 500 {
				res.Notes = res.Notes[:500]
			}
			res.HasUpdate = newer(parseVersion(data.TagName), parseVersion(current))
			cacheSave(map[string]any{"ts": time.Now().UnixMilli(),
				"tag": data.TagName, "url": res.URL, "etag": hdrs.Get("ETag")})
			return res
		}
	}

	// 2) atom
	if code2, _, body2, err2 := httpGet(atomURL, nil, 8*time.Second); err2 == nil && code2 == 200 {
		if m := tagRe.FindStringSubmatch(string(body2)); m != nil {
			tag := strings.ReplaceAll(m[1], "%20", " ")
			res.Latest = tag
			res.HasUpdate = newer(parseVersion(tag), parseVersion(current))
			cacheSave(map[string]any{"ts": time.Now().UnixMilli(), "tag": tag,
				"url": releaseURL})
			return res
		}
	}

	// 3) HTML
	if code3, _, body3, err3 := httpGet(pageURL, nil, 8*time.Second); err3 == nil && code3 == 200 {
		if m := tagRe.FindStringSubmatch(string(body3)); m != nil {
			tag := strings.ReplaceAll(m[1], "%20", " ")
			res.Latest = tag
			res.HasUpdate = newer(parseVersion(tag), parseVersion(current))
			return res
		}
	}

	res.Error = "无法连接更新服务，请手动访问发布页"
	if cache != nil {
		if tag, _ := cache["tag"].(string); tag != "" {
			res.Latest = tag
			res.Cached = true
			res.Error = "网络失败，以下为缓存结果"
			res.HasUpdate = newer(parseVersion(tag), parseVersion(current))
		}
	}
	return res
}
