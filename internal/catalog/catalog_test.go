package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleCatalog = `{
  "schemaVersion": "zcode.model-providers.v1",
  "providers": [
    {
      "id": "zai",
      "name": "Z.AI",
      "models": [
        {
          "id": "glm-5.3",
          "name": "GLM-5.3",
          "reasoning": {"defaultLevel": "max", "levels": {"low": {}, "high": {}, "max": {}}}
        },
        {
          "id": "glm-5.1",
          "name": "glm-5.1",
          "reasoning": {"defaultLevel": "enabled", "levels": {"enabled": {}, "off": {}}}
        },
        {
          "id": "glm-5.1-highspeed",
          "name": "glm-5.1-highspeed",
          "reasoning": {"defaultLevel": "enabled", "levels": {"enabled": {}, "off": {}}}
        }
      ]
    }
  ]
}`

// 新版目录：glm-5.3 档位升级为含 medium
const newerCatalog = `{
  "schemaVersion": "zcode.model-providers.v1",
  "providers": [
    {
      "id": "zai",
      "name": "Z.AI",
      "models": [
        {
          "id": "glm-5.3",
          "name": "GLM-5.3",
          "reasoning": {"defaultLevel": "medium", "levels": {"low": {}, "medium": {}, "high": {}, "max": {}}}
        }
      ]
    }
  ]
}`

func writeTemp(t *testing.T, name, content string) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadAndMatch(t *testing.T) {
	ix, err := LoadPaths([]string{writeTemp(t, "models_catalog_test.json", sampleCatalog), "不存在的路径"})
	if err != nil {
		t.Fatal(err)
	}
	if ix.Size() != 3 {
		t.Fatalf("Size = %d, want 3", ix.Size())
	}
	e, mt := ix.Match("glm-5.3")
	if e == nil || mt != MatchExact {
		t.Fatalf("精确匹配失败: %v type=%v", e, mt)
	}
	if e.Default != "max" || len(e.Variants) != 3 || e.Variants[0] != "low" || e.Variants[2] != "max" {
		t.Fatalf("档位错误: %+v", e)
	}

	// 大小写与斜杠前缀
	if e, mt := ix.Match("ZHIPU/GLM-5.3"); e == nil || mt != MatchExact {
		t.Fatal("斜杠前缀匹配失败")
	}
	// [] 前缀
	if e, _ := ix.Match("[按次]glm-5.1"); e == nil {
		t.Fatal("括号前缀匹配失败")
	}
	// 不存在
	if e, _ := ix.Match("nonexistent-model"); e != nil {
		t.Fatal("不应匹配")
	}
}

func TestSeriesMatch(t *testing.T) {
	ix, _ := LoadPaths([]string{writeTemp(t, "models_catalog_test.json", sampleCatalog)})

	// glm-5.3-flash 不在目录里，但 系列 glm@5.3 一致 → 系列匹配
	e, mt := ix.Match("glm-5.3-flash")
	if e == nil || mt != MatchSeries {
		t.Fatalf("系列匹配失败: %v type=%v", e, mt)
	}
	if e.Default != "max" {
		t.Fatalf("系列匹配档位错误: %+v", e)
	}

	// my-glm-5.6 不会匹配（版本不同，glm@5.6 无候选）
	if e, _ := ix.Match("my-glm-5.6"); e != nil {
		t.Fatal("不同版本不应系列匹配")
	}
}

func TestNewestCatalogFileWins(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "models_catalog_2026-05-01.json"), []byte(sampleCatalog), 0o644)
	os.WriteFile(filepath.Join(dir, "models_catalog_2026-06-03.json"), []byte(newerCatalog), 0o644)
	ix, err := LoadPaths([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := ix.Match("glm-5.3")
	if e == nil || e.Default != "medium" || len(e.Variants) != 4 {
		t.Fatalf("应加载最新目录文件: %+v", e)
	}
}

func TestFuzzyMatch(t *testing.T) {
	ix, _ := LoadPaths([]string{writeTemp(t, "models_catalog_test.json", sampleCatalog)})
	// "my-glm-5.1-preview"：系列 my-glm@5.1 无候选 → 模糊唯一包含 glm-5.1 → 模糊匹配
	e, mt := ix.Match("my-glm-5.1-preview")
	if e == nil || mt != MatchFuzzy {
		t.Fatalf("模糊匹配失败: %v type=%v", e, mt)
	}
	// 短 ID 不参与模糊匹配
	if e, _ := ix.Match("abc"); e != nil {
		t.Fatal("短 ID 不应模糊匹配")
	}
}

func TestSortLevels(t *testing.T) {
	ls := []string{"max", "off", "high", "none", "low", "enabled"}
	SortLevels(ls)
	want := []string{"none", "off", "low", "high", "enabled", "max"}
	for i := range want {
		if ls[i] != want[i] {
			t.Fatalf("SortLevels = %v, want %v", ls, want)
		}
	}
}
