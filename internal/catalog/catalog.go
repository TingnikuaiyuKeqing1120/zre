// Package catalog 加载 zcode 官方模型目录（models_catalog_*.json），
// 提供"配置里的模型 ID → 官方思考档位"的匹配能力，用于自动嗅探功能。
//
// 目录文件随 zcode 更新（文件名含日期）。同一目录存在多份时取文件名最新的
// 一份，因此 zcode 升级后新目录自动生效；运行中可用重新扫描刷新。
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entry 是目录中一个带思考档位的模型。
type Entry struct {
	ProviderID   string   `json:"providerId"`
	ProviderName string   `json:"providerName"`
	ModelID      string   `json:"modelId"`
	ModelName    string   `json:"modelName"`
	Variants     []string `json:"variants"` // 按官方惯例排序
	Default      string   `json:"default"`
	Context      int64    `json:"context,omitempty"` // 目录声明的上下文窗口
	Output       int64    `json:"output,omitempty"`  // 目录声明的最大输出
	InputMod     []string `json:"inputModalities,omitempty"`
	OutputMod    []string `json:"outputModalities,omitempty"`
}

type catalogFile struct {
	Providers []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Models []struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			ContextWindow   int64  `json:"contextWindow"`
			MaxOutputTokens int64  `json:"maxOutputTokens"`
			Modalities      *struct {
				Input  []string `json:"input"`
				Output []string `json:"output"`
			} `json:"modalities"`
			Reasoning *struct {
				DefaultLevel string                     `json:"defaultLevel"`
				Levels       map[string]json.RawMessage `json:"levels"`
			} `json:"reasoning"`
		} `json:"models"`
	} `json:"providers"`
}

// MatchType 描述一次匹配的可信程度。
type MatchType string

const (
	MatchExact  MatchType = "exact"  // 模型 ID 精确匹配（含大小写/[]前缀/短 ID 归一化）
	MatchSeries MatchType = "series" // 系列名+版本号匹配（如 glm@5.3）
	MatchFuzzy  MatchType = "fuzzy"  // 子串模糊匹配（建议人工核对）
)

type Index struct {
	byNorm   map[string]*Entry
	bySeries map[string][]*Entry
	count    int
}

var bracketRe = regexp.MustCompile(`^(?:\[[^\]]*\])+`)

// 档位排序：官方常见档位按强度排列，未知档位排在后面按字母序。
var levelRank = map[string]int{
	"none": 0, "off": 1, "minimal": 2, "low": 3, "medium": 4,
	"high": 5, "xhigh": 6, "enabled": 7, "max": 8,
}

func SortLevels(ls []string) {
	sort.SliceStable(ls, func(i, j int) bool {
		ri, rj := levelRank[ls[i]], levelRank[ls[j]]
		if ri != rj {
			return ri < rj
		}
		return ls[i] < ls[j]
	})
}

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// candidateKeys 为配置里的模型 ID 生成一系列候选匹配键：
// 原值、去掉 "[按次]" 类前缀、取 "/" 后的短 ID 等。
func candidateKeys(id string) []string {
	id = strings.TrimSpace(id)
	var keys []string
	add := func(s string) {
		s = normalize(s)
		if s == "" {
			return
		}
		for _, k := range keys {
			if k == s {
				return
			}
		}
		keys = append(keys, s)
	}
	add(id)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		add(id[i+1:])
	}
	stripped := strings.TrimSpace(bracketRe.ReplaceAllString(id, ""))
	if stripped != id {
		add(stripped)
		if i := strings.LastIndex(stripped, "/"); i >= 0 {
			add(stripped[i+1:])
		}
	}
	return keys
}

var versionRe = regexp.MustCompile(`(\d+(?:\.\d+)*)`)

// seriesKey 提取"系列名@版本号"（如 glm@5.3、deepseek@4、kimi-k@3）。
// 提取规则是确定性的，只要配置侧与目录侧解析结果一致即可用于匹配。
func seriesKey(id string) string {
	s := normalize(bracketRe.ReplaceAllString(strings.TrimSpace(id), ""))
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	loc := versionRe.FindStringIndex(s)
	if loc == nil {
		return ""
	}
	version := s[loc[0]:loc[1]]
	family := strings.Trim(s[:loc[0]], "-_ .v")
	if family == "" || version == "" {
		return ""
	}
	return family + "@" + version
}

// LoadPaths 从若干文件或目录构建索引；目录下只加载文件名最新的
// models_catalog*.json（旧文件自动让位给 zcode 更新后的新目录文件）。
func LoadPaths(paths []string) (*Index, error) {
	ix := &Index{byNorm: map[string]*Entry{}, bySeries: map[string][]*Entry{}}
	var files []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue // 候选路径不存在时静默跳过
		}
		if fi.IsDir() {
			des, err := os.ReadDir(p)
			if err != nil {
				return nil, fmt.Errorf("读取目录 %s 失败: %w", p, err)
			}
			var names []string
			for _, de := range des {
				name := de.Name()
				if !de.IsDir() && strings.HasPrefix(name, "models_catalog") && strings.HasSuffix(name, ".json") {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				sort.Sort(sort.Reverse(sort.StringSlice(names))) // 文件名含日期，字典序最大即最新
				files = append(files, filepath.Join(p, names[0]))
			}
		} else {
			files = append(files, p)
		}
	}
	for _, f := range files {
		if err := ix.loadFile(f); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", f, err)
		}
	}
	return ix, nil
}

func (ix *Index) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cf catalogFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return err
	}
	for _, prov := range cf.Providers {
		for _, m := range prov.Models {
			if m.Reasoning == nil || len(m.Reasoning.Levels) == 0 {
				continue
			}
			variants := make([]string, 0, len(m.Reasoning.Levels))
			for lv := range m.Reasoning.Levels {
				variants = append(variants, lv)
			}
			SortLevels(variants)
			def := strings.TrimSpace(m.Reasoning.DefaultLevel)
			valid := false
			for _, v := range variants {
				if v == def {
					valid = true
					break
				}
			}
			if !valid && len(variants) > 0 {
				def = variants[0]
			}
			e := &Entry{
				ProviderID:   prov.ID,
				ProviderName: prov.Name,
				ModelID:      m.ID,
				ModelName:    m.Name,
				Variants:     variants,
				Default:      def,
				Context:      m.ContextWindow,
				Output:       m.MaxOutputTokens,
			}
			if m.Modalities != nil {
				e.InputMod = m.Modalities.Input
				e.OutputMod = m.Modalities.Output
			}
			key := normalize(m.ID)
			if _, exists := ix.byNorm[key]; !exists {
				ix.byNorm[key] = e
				ix.count++
			}
			if sk := seriesKey(m.ID); sk != "" {
				ix.bySeries[sk] = append(ix.bySeries[sk], e)
			}
		}
	}
	return nil
}

func (ix *Index) Size() int { return ix.count }

// Match 匹配配置中的模型 ID，按可信度依次尝试：
//  1. 精确匹配：原 ID / 小写 / 去 [] 前缀 / 取 "/" 后短 ID
//  2. 系列匹配：系列名+版本号（如 glm@5.3），要求候选目录条目的档位组合完全一致
//  3. 模糊匹配：双方互相包含且候选唯一（要求 ID 长度 ≥ 4 防误配）
func (ix *Index) Match(modelID string) (*Entry, MatchType) {
	if ix == nil {
		return nil, ""
	}
	for _, k := range candidateKeys(modelID) {
		if e, ok := ix.byNorm[k]; ok {
			return e, MatchExact
		}
	}
	if sk := seriesKey(modelID); sk != "" {
		if cands := ix.bySeries[sk]; len(cands) > 0 && sameLevels(cands) {
			return cands[0], MatchSeries
		}
	}
	cfg := normalize(modelID)
	if len(cfg) >= 4 {
		var found *Entry
		n := 0
		for _, e := range ix.byNorm {
			en := normalize(e.ModelID)
			if len(en) < 4 {
				continue
			}
			if strings.Contains(cfg, en) || strings.Contains(en, cfg) {
				found = e
				n++
				if n > 1 {
					return nil, ""
				}
			}
		}
		if n == 1 {
			return found, MatchFuzzy
		}
	}
	return nil, ""
}

// MatchLabel 返回匹配条目与人类可读的匹配方式描述。
func (ix *Index) MatchLabel(modelID string) (*Entry, MatchType, string) {
	e, mt := ix.Match(modelID)
	var label string
	switch mt {
	case MatchExact:
		label = "精确"
	case MatchSeries:
		label = "系列 " + seriesKey(modelID)
	case MatchFuzzy:
		label = "模糊"
	}
	return e, mt, label
}

func sameLevels(entries []*Entry) bool {
	for _, e := range entries[1:] {
		if e.Default != entries[0].Default || strings.Join(e.Variants, ",") != strings.Join(entries[0].Variants, ",") {
			return false
		}
	}
	return true
}
