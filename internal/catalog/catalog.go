// Package catalog 加载 zcode 官方模型目录并回答
// "某个模型 ID 应该有什么思考档位 / 上下文 / 模态"。
//
// 自 zcode v3.12.3 起目录改为规则引擎格式（resources/config/provider/*.json，
// schemaVersion 1，modelConfigRules.modelRules 基于正则链式覆盖），
// 旧版 models_catalog*.json（zcode.model-providers.v1）不再兼容。
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

// Entry 是一次解析后的完整模型配置。
type Entry struct {
	ProviderName string   `json:"providerName,omitempty"` // 新格式无 per-model 来源，留作兼容字段
	ModelID      string   `json:"modelId,omitempty"`
	Variants     []string `json:"variants"`
	Default      string   `json:"default"`
	Context      int64    `json:"context,omitempty"`
	Output       int64    `json:"output,omitempty"`
	InputMod     []string `json:"inputModalities,omitempty"`
	OutputMod    []string `json:"outputModalities,omitempty"`
}

// MatchType 描述一次匹配的可信程度。
type MatchType string

const (
	MatchExact  MatchType = "exact"  // 命中官方正则规则
	MatchSeries MatchType = "series" // 系列名+版本号匹配（保留给旧调用方，新格式下不产生）
	MatchFuzzy  MatchType = "fuzzy"  // 子串模糊匹配（建议人工核对）
)

type Index struct {
	rules []compiledRule
	base  *Entry // ".*" 兜底规则的属性（模态等）
}

type compiledRule struct {
	re       *regexp.Regexp
	variants []string // nil = 本规则不涉及档位
	ctx      int64
	out      int64
	inMod    []string
	outMod   []string
}

// ---- 新格式（zcode ≥ v3.12.3） ----

type ruleFile struct {
	SchemaVersion int `json:"schemaVersion"`
	Config        struct {
		ModelConfigRules struct {
			ModelRules []struct {
				ModelMatch string `json:"modelMatch"`
				Config     struct {
					Properties struct {
						ContextWindow int64 `json:"contextWindow"`
						InputFormat   *struct {
							SupportsText  bool `json:"supportsText"`
							SupportsImage bool `json:"supportsImage"`
							SupportsVideo bool `json:"supportsVideo"`
							SupportsAudio bool `json:"supportsAudio"`
							SupportsPdf   bool `json:"supportsPdf"`
						} `json:"inputFormat"`
						OutputFormat *struct {
							SupportsText bool `json:"supportsText"`
						} `json:"outputFormat"`
					} `json:"properties"`
					OptionSpecs struct {
						MaxOutputTokens *struct {
							Max int64 `json:"max"`
						} `json:"maxOutputTokens"`
						ReasoningLevel *struct {
							Values []string `json:"values"`
						} `json:"reasoningLevel"`
					} `json:"optionSpecs"`
				} `json:"config"`
			} `json:"modelRules"`
		} `json:"modelConfigRules"`
	} `json:"config"`
}

// LoadPaths 从文件/目录构建规则索引；目录下加载所有 *.json 规则文件
// （如 resources/config/provider/zcode-builtin.json）。
func LoadPaths(paths []string) (*Index, error) {
	ix := &Index{}
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
			for _, de := range des {
				if !de.IsDir() && strings.HasSuffix(strings.ToLower(de.Name()), ".json") {
					if err := ix.loadFile(filepath.Join(p, de.Name())); err != nil {
						return nil, fmt.Errorf("解析 %s 失败: %w", filepath.Join(p, de.Name()), err)
					}
				}
			}
		} else {
			if err := ix.loadFile(p); err != nil {
				return nil, fmt.Errorf("解析 %s 失败: %w", p, err)
			}
		}
	}
	return ix, nil
}

func (ix *Index) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rf ruleFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return fmt.Errorf("不是 zcode 规则目录格式（v3.12.3+ 的 resources/config/provider/*.json）: %w", err)
	}
	for _, r := range rf.Config.ModelConfigRules.ModelRules {
		re, err := regexp.Compile("^(?:" + r.ModelMatch + ")$")
		if err != nil {
			continue // 跳过无法编译的规则（理论上不该出现）
		}
		cr := compiledRule{re: re}
		if rl := r.Config.OptionSpecs.ReasoningLevel; rl != nil && len(rl.Values) > 0 {
			cr.variants = append([]string{}, rl.Values...)
		}
		cr.ctx = r.Config.Properties.ContextWindow
		if mo := r.Config.OptionSpecs.MaxOutputTokens; mo != nil && mo.Max > 0 {
			cr.out = mo.Max
		}
		if inf := r.Config.Properties.InputFormat; inf != nil {
			cr.inMod = modalities(inf.SupportsText, inf.SupportsImage, inf.SupportsVideo, inf.SupportsAudio)
		}
		if outf := r.Config.Properties.OutputFormat; outf != nil && outf.SupportsText {
			cr.outMod = []string{"text"}
		}
		if r.ModelMatch == ".*" && ix.base == nil {
			ix.base = &Entry{InputMod: cr.inMod, OutputMod: cr.outMod}
		}
		ix.rules = append(ix.rules, cr)
	}
	return nil
}

func modalities(text, image, video, audio bool) []string {
	var out []string
	if text {
		out = append(out, "text")
	}
	if image {
		out = append(out, "image")
	}
	if video {
		out = append(out, "video")
	}
	if audio {
		out = append(out, "audio")
	}
	return out
}

func (ix *Index) Size() int { return len(ix.rules) }

// 档位排序：官方常见档位按强度排列，未知档位排在后面按字母序。
var levelRank = map[string]int{
	"none": 0, "off": 1, "disabled": 2, "minimal": 3, "low": 4,
	"medium": 5, "high": 6, "xhigh": 7, "enabled": 8, "max": 9,
}

// SortLevels 按强度排列档位。
func SortLevels(ls []string) {
	sort.SliceStable(ls, func(i, j int) bool {
		ri, rj := levelRank[ls[i]], levelRank[ls[j]]
		if ri != rj {
			return ri < rj
		}
		return ls[i] < ls[j]
	})
}

// Match 按官方规则解析模型 ID：zcode 的模型正则通常按小写书写
// （如 .*glm-5\.3.*），对大小写不敏感。这里对 ID 的各候选形态
// （原值 / 去 [] 前缀 / 取 "/" 后短 ID / 小写）分别求值，
// 取信息量最大的结果（特例档位 > 兜底档位）。
func (ix *Index) Match(modelID string) (*Entry, MatchType) {
	if ix == nil || len(ix.rules) == 0 {
		return nil, ""
	}
	var best *Entry
	for _, cand := range candidateKeys(modelID) {
		e := ix.apply(cand)
		if e == nil {
			continue
		}
		if better(e, best) {
			best = e
		}
	}
	if best != nil {
		return best, MatchExact
	}
	// 规则未直接命中：回退为唯一子串模糊匹配（与旧版行为一致，便于核对）
	if cfg := strings.ToLower(strings.TrimSpace(modelID)); len(cfg) >= 4 {
		if e := ix.applySubstring(cfg); e != nil {
			return e, MatchFuzzy
		}
	}
	return nil, ""
}

// better 判断 e 是否比当前最优结果信息量更大：
// 特例档位集合（非 disabled/enabled 兜底）优先；其次更大的上下文声明。
func better(e, best *Entry) bool {
	if best == nil {
		return true
	}
	eSpec, bSpec := isSpecific(e), isSpecific(best)
	if eSpec != bSpec {
		return eSpec
	}
	return e.Context > best.Context
}

// isSpecific 判断档位是否为特例（非两档兜底）。
func isSpecific(e *Entry) bool {
	if e == nil || len(e.Variants) != 2 {
		return len(e.Variants) > 2
	}
	return !(e.Variants[0] == "disabled" && e.Variants[1] == "enabled")
}

func (ix *Index) apply(id string) *Entry {
	if id == "" {
		return nil
	}
	e := &Entry{Variants: []string{}}
	matched := false
	for _, r := range ix.rules {
		if !r.re.MatchString(id) {
			continue
		}
		matched = true
		if r.variants != nil {
			e.Variants = append([]string{}, r.variants...)
		}
		if r.ctx > 0 {
			e.Context = r.ctx
		}
		if r.out > 0 {
			e.Output = r.out
		}
		if r.inMod != nil {
			e.InputMod = r.inMod
		}
		if r.outMod != nil {
			e.OutputMod = r.outMod
		}
	}
	if !matched {
		return nil
	}
	SortLevels(e.Variants)
	e.Default = pickDefault(e.Variants)
	if e.InputMod == nil {
		e.InputMod = append([]string{}, ix.base.InputMod...)
	}
	if e.OutputMod == nil {
		e.OutputMod = append([]string{}, ix.base.OutputMod...)
	}
	return e
}

// applySubstring 模糊回退：正则把子串包进来匹配，要求唯一命中集合。
func (ix *Index) applySubstring(cfg string) *Entry {
	var hits []*Entry
	seen := map[string]bool{}
	for _, r := range ix.rules {
		pat := r.re.String()
		// 剥掉我们包的锚点后做子串匹配
		inner := strings.TrimSuffix(strings.TrimPrefix(pat, "^(?:"), ")$")
		re, err := regexp.Compile(inner)
		if err != nil {
			continue
		}
		if re.MatchString(cfg) {
			if e := ix.apply(cfg); e != nil {
				key := fmt.Sprintf("%v|%d|%s", e.Variants, e.Context, e.Default)
				if !seen[key] {
					seen[key] = true
					hits = append(hits, e)
				}
				if len(hits) > 1 {
					return nil // 歧义：多种不同配置都命中，宁缺毋滥
				}
			}
		}
	}
	if len(hits) == 1 {
		return hits[0]
	}
	return nil
}

// pickDefault 选默认档位：官方规则目录不带默认值，
// 采用 ZRE 惯例——xhigh > max > high > medium > enabled > 列表末位。
func pickDefault(variants []string) string {
	if len(variants) == 0 {
		return ""
	}
	for _, pref := range []string{"xhigh", "max", "high", "medium", "enabled"} {
		for _, v := range variants {
			if v == pref {
				return v
			}
		}
	}
	return variants[len(variants)-1]
}

// candidateKeys 为配置里的模型 ID 生成候选匹配串：
// 原值、去 "[按次]" 类前缀、取 "/" 后短 ID 及各自的小写变体。
func candidateKeys(id string) []string {
	var keys []string
	add := func(s string) {
		s = strings.TrimSpace(s)
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
	if strings.HasPrefix(id, "[") {
		if i := strings.Index(id, "]"); i >= 0 && i < len(id)-1 {
			stripped := id[i+1:]
			add(stripped)
			if j := strings.LastIndex(stripped, "/"); j >= 0 {
				add(stripped[j+1:])
			}
		}
	}
	for _, k := range append([]string{}, keys...) {
		add(strings.ToLower(k))
	}
	return keys
}

// MatchLabel 返回匹配条目与人类可读的匹配方式描述。
func (ix *Index) MatchLabel(modelID string) (*Entry, MatchType, string) {
	e, mt := ix.Match(modelID)
	var label string
	switch mt {
	case MatchExact:
		label = "精确"
	case MatchSeries:
		label = "系列"
	case MatchFuzzy:
		label = "模糊"
	}
	return e, mt, label
}
