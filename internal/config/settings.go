// ZRE 自身设置：持久化到配置同目录的 zre-settings.json。
// 与 zcode 的配置完全独立。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Settings struct {
	// 关闭所有网页后自动退出（心跳超时 15 秒；默认关闭）
	AutoQuitOnPageClose bool `json:"autoQuitOnPageClose"`
	// 自动嗅探的官方模型目录路径（空 = 使用内置探测）
	CatalogPaths []string `json:"catalogPaths"`
}

func (s *Store) settingsPath() string {
	return filepath.Join(s.dir, "zre-settings.json")
}

func (s *Store) loadSettings() {
	s.settings = Settings{CatalogPaths: []string{}}
	data, err := os.ReadFile(s.settingsPath())
	if err != nil {
		return
	}
	var parsed Settings
	if json.Unmarshal(data, &parsed) != nil {
		return
	}
	if parsed.CatalogPaths == nil {
		parsed.CatalogPaths = []string{}
	}
	s.settings = parsed
}

// Settings 返回当前设置的拷贝。
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.settings
	out.CatalogPaths = append([]string{}, s.settings.CatalogPaths...)
	return out
}

// SaveSettings 校验并持久化设置。
func (s *Store) SaveSettings(in Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var paths []string
	seen := map[string]bool{}
	for _, p := range in.CatalogPaths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if strings.HasPrefix(p, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[1:])
			}
		}
		paths = append(paths, p)
	}
	s.settings = Settings{
		AutoQuitOnPageClose: in.AutoQuitOnPageClose,
		CatalogPaths:        paths,
	}

	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.settingsPath(), append(data, '\n'))
}
