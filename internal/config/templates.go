// 思考档位模板：用户可把常用档位组合（如 low-high-max，默认 max）存为模板并一键应用。
// 存放在配置同目录的 zre-templates.json，属于 ZRE 自己的数据，不影响 zcode。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Template struct {
	Name     string   `json:"name"`
	Variants []string `json:"variants"`
	Default  string   `json:"defaultVariant"`
}

var builtinTemplates = []Template{
	{Name: "标准三档（默认max）", Variants: []string{"low", "high", "max"}, Default: "max"},
	{Name: "均匀三档（默认high）", Variants: []string{"low", "medium", "high"}, Default: "high"},
	{Name: "开关型（默认enabled）", Variants: []string{"enabled", "off"}, Default: "enabled"},
	{Name: "深度型（默认max）", Variants: []string{"off", "high", "max"}, Default: "max"},
}

func (s *Store) templatesPath() string {
	return s.dir + string(os.PathSeparator) + "zre-templates.json"
}

func (s *Store) loadTemplates() {
	s.templates = nil
	data, err := os.ReadFile(s.templatesPath())
	if err != nil {
		// 首次使用：注入内置模板（目录存在时落盘，方便用户在文件里看到）
		s.templates = append([]Template{}, builtinTemplates...)
		if _, statErr := os.Stat(s.dir); statErr == nil {
			_ = s.persistTemplates()
		}
		return
	}
	var wrap struct {
		Templates []Template `json:"templates"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil || wrap.Templates == nil {
		s.templates = append([]Template{}, builtinTemplates...)
		return
	}
	s.templates = wrap.Templates
}

// Templates 返回模板列表（已拷贝）。
func (s *Store) Templates() []Template {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Template, len(s.templates))
	copy(out, s.templates)
	return out
}

func (s *Store) persistTemplates() error {
	wrap := struct {
		Templates []Template `json:"templates"`
	}{Templates: s.templates}
	data, err := json.MarshalIndent(wrap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(s.templatesPath(), data)
}

func (s *Store) SaveTemplate(t Template, allowOverwrite bool) (warnings []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return nil, fmt.Errorf("模板名称不能为空")
	}
	if len([]rune(t.Name)) > 60 {
		return nil, fmt.Errorf("模板名称过长")
	}
	seen := map[string]bool{}
	var variants []string
	for _, v := range t.Variants {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		variants = append(variants, v)
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("模板档位不能为空")
	}
	def := strings.TrimSpace(t.Default)
	if !seen[def] {
		def = variants[0]
		warnings = append(warnings, fmt.Sprintf("默认档位 %q 不在档位列表中，已改为 %q", t.Default, def))
	}
	t = Template{Name: t.Name, Variants: variants, Default: def}

	for i, old := range s.templates {
		if old.Name == t.Name {
			if !allowOverwrite {
				return nil, fmt.Errorf("模板 %q 已存在", t.Name)
			}
			s.templates[i] = t
			return warnings, s.persistTemplates()
		}
	}
	s.templates = append(s.templates, t)
	return warnings, s.persistTemplates()
}

func (s *Store) DeleteTemplate(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.templates {
		if t.Name == name {
			s.templates = append(s.templates[:i], s.templates[i+1:]...)
			return s.persistTemplates()
		}
	}
	return fmt.Errorf("模板 %q 不存在", name)
}
