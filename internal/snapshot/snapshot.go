// Package snapshot 管理命名快照（~/.zcode/v2/snapshots/*.json）。
package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Info struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type Manager struct {
	Dir string
}

func NewManager(dir string) *Manager { return &Manager{Dir: dir} }

var (
	nameInvalidRe = regexp.MustCompile(`[^\p{L}\p{N}\-_\.]+`)
	nameDotsRe    = regexp.MustCompile(`^\.+$`)
)

// Sanitize 把任意输入清洗成安全的文件名主干。
func Sanitize(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = nameInvalidRe.ReplaceAllString(name, "_")
	name = strings.Trim(name, ".")
	name = nameDotsRe.ReplaceAllString(name, "")
	runes := []rune(name)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	name = string(runes)
	if name == "" {
		return "", fmt.Errorf("快照名称不能为空")
	}
	return name, nil
}

func (m *Manager) path(name string) string { return filepath.Join(m.Dir, name+".json") }

// Save 把 data 写为命名快照。若同名快照已存在且 !allowOverwrite 则报错。
func (m *Manager) Save(name string, data []byte, allowOverwrite bool) (string, error) {
	clean, err := Sanitize(name)
	if err != nil {
		return "", err
	}
	if !allowOverwrite {
		if _, err := os.Stat(m.path(clean)); err == nil {
			return clean, fmt.Errorf("快照 %q 已存在", clean)
		}
	}
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return clean, err
	}
	if err := os.WriteFile(m.path(clean), data, 0o644); err != nil {
		return clean, err
	}
	return clean, nil
}

func (m *Manager) List() ([]Info, error) {
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Info{}, nil
		}
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{
			Name:    strings.TrimSuffix(e.Name(), ".json"),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	if out == nil {
		out = []Info{}
	}
	return out, nil
}

func (m *Manager) Read(name string) ([]byte, error) {
	clean, err := Sanitize(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(m.path(clean))
	if err != nil {
		return nil, fmt.Errorf("读取快照 %q 失败: %w", clean, err)
	}
	return data, nil
}

func (m *Manager) Delete(name string) error {
	clean, err := Sanitize(name)
	if err != nil {
		return err
	}
	if err := os.Remove(m.path(clean)); err != nil {
		return fmt.Errorf("删除快照 %q 失败: %w", clean, err)
	}
	return nil
}
