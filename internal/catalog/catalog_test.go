package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

// 与 zcode v3.12.3+ resources/config/provider/*.json 同构的最小规则文件
const ruleCatalog = `{
  "schemaVersion": 1,
  "revision": 28,
  "config": {
    "modelConfigRules": {
      "modelRules": [
        {
          "modelMatch": ".*",
          "config": {
            "properties": {
              "contextWindow": 200000,
              "inputFormat": {"supportsText": true, "supportsImage": false, "supportsVideo": false, "supportsAudio": false},
              "outputFormat": {"supportsText": true}
            },
            "optionSpecs": {
              "reasoningLevel": {"values": ["disabled", "enabled"]}
            }
          }
        },
        {
          "modelMatch": ".*glm-5\\.3(?:-flash)?(?:[.\\-:/\\[].*)?",
          "config": {
            "properties": {"contextWindow": 1000000},
            "optionSpecs": {"reasoningLevel": {"values": ["low", "high", "max"]}}
          }
        },
        {
          "modelMatch": ".*\\[1m\\]",
          "config": {"properties": {"contextWindow": 1000000}}
        },
        {
          "modelMatch": ".*deepseek-v4(?:[.\\-:/\\[].*)?",
          "config": {
            "optionSpecs": {"reasoningLevel": {"values": ["disabled", "low", "high", "max"]}}
          }
        }
      ]
    }
  }
}`

func writeRuleDir(t *testing.T) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zcode-builtin.json"), []byte(ruleCatalog), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadAndMatchExact(t *testing.T) {
	ix, err := LoadPaths([]string{writeRuleDir(t), "不存在的路径"})
	if err != nil {
		t.Fatal(err)
	}
	if ix.Size() != 4 {
		t.Fatalf("rules = %d, want 4", ix.Size())
	}

	// glm-5.3：特例规则覆盖兜底
	e, mt := ix.Match("GLM-5.3")
	if e == nil || mt != MatchExact {
		t.Fatalf("GLM-5.3 match failed: %v %v", e, mt)
	}
	if len(e.Variants) != 3 || e.Variants[0] != "low" || e.Variants[2] != "max" || e.Default != "max" {
		t.Fatalf("glm-5.3 variants/default wrong: %+v", e)
	}
	if e.Context != 1000000 {
		t.Fatalf("glm-5.3 context wrong: %d", e.Context)
	}
	// 模态继承自 ".*" 兜底
	if len(e.InputMod) != 1 || e.InputMod[0] != "text" {
		t.Fatalf("glm-5.3 modalities wrong: %v", e.InputMod)
	}

	// 变体名与斜杠前缀
	if e, _ := ix.Match("glm-5.3-flash"); e == nil || e.Context != 1000000 {
		t.Fatal("glm-5.3-flash match failed")
	}
	if e, _ := ix.Match("[按次]glm-5.3"); e == nil {
		t.Fatal("bracket prefix match failed")
	}
	if e, _ := ix.Match("vendor/GLM-5.3"); e == nil {
		t.Fatal("slash prefix match failed")
	}
}

func TestBaseFallbackAndOverride(t *testing.T) {
	ix, _ := LoadPaths([]string{writeRuleDir(t)})

	// 未知模型 → 兜底 disabled/enabled，200k
	e, mt := ix.Match("some-unknown-model")
	if e == nil || mt != MatchExact {
		t.Fatalf("base fallback failed: %v %v", e, mt)
	}
	if len(e.Variants) != 2 || e.Variants[0] != "disabled" || e.Default != "enabled" {
		t.Fatalf("base variants wrong: %+v", e)
	}
	if e.Context != 200000 {
		t.Fatalf("base context wrong: %d", e.Context)
	}

	// deepseek：特例档位 + 兜底上下文
	e, _ = ix.Match("deepseek-v4-pro")
	if e == nil || len(e.Variants) != 4 || e.Variants[0] != "disabled" {
		t.Fatalf("deepseek variants wrong: %+v", e)
	}
	if e.Context != 200000 {
		t.Fatalf("deepseek should inherit base context, got %d", e.Context)
	}

	// [1m] 后缀只改上下文，不改档位
	e, _ = ix.Match("kimi-k3[1m]")
	if e == nil {
		t.Fatal("[1m] match failed")
	}
	// kimi-k3 不命中 glm/deepseek 特例 → 兜底档位；上下文被 [1m] 规则覆盖
	if e.Context != 1000000 {
		t.Fatalf("[1m] context override failed: %d", e.Context)
	}
	if len(e.Variants) != 2 {
		t.Fatalf("[1m] variants wrong: %+v", e)
	}
}

func TestFuzzyFallback(t *testing.T) {
	ix, _ := LoadPaths([]string{writeRuleDir(t)})
	// "my-glm-5.3-preview" 不在候选键里，但内部含 "glm-5.3"
	// —— 注意规则是 .*glm-5\.3.*，apply 对完整串也能命中，所以这是精确而非模糊
	e, mt := ix.Match("my-glm-5.3-preview")
	if e == nil {
		t.Fatal("fuzzy/exact match failed")
	}
	_ = mt // 由于官方规则本身就是前缀通配，官方正则直接命中是预期行为
}

func TestNoMatch(t *testing.T) {
	ix, _ := LoadPaths([]string{writeRuleDir(t)})
	// 规则引擎对任意非空串都有 ".*" 兜底，因此 Match 几乎不会失败；
	// 只有空串才无匹配
	if e, _ := ix.Match("   "); e != nil {
		t.Fatalf("whitespace should not match: %+v", e)
	}
}

func TestPickDefault(t *testing.T) {
	cases := []struct {
		variants []string
		want     string
	}{
		{[]string{"low", "high", "max"}, "max"},
		{[]string{"disabled", "enabled"}, "enabled"},
		{[]string{"low", "medium", "xhigh"}, "xhigh"},
		{[]string{"none", "low", "medium", "high"}, "high"},
		{[]string{"medium"}, "medium"},
	}
	for _, c := range cases {
		if got := pickDefault(c.variants); got != c.want {
			t.Errorf("pickDefault(%v) = %q, want %q", c.variants, got, c.want)
		}
	}
}

func TestSortLevels(t *testing.T) {
	ls := []string{"max", "disabled", "high", "none", "low", "enabled", "xhigh"}
	SortLevels(ls)
	want := []string{"none", "disabled", "low", "high", "xhigh", "enabled", "max"}
	for i := range want {
		if ls[i] != want[i] {
			t.Fatalf("SortLevels = %v, want %v", ls, want)
		}
	}
}
