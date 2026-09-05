package jsondom

import (
	"strings"
	"testing"
)

// 与 zcode 实际写入格式一致：统一两空格缩进、全展开。
// 注：DOM 保证键序与值逐字节还原，但内联写法（{"a":1}）会被统一展开为多行，
// 这与 zcode / probe 工具的输出风格一致，实际配置不受影响。
const sample = `{
  "_unknownTopLevel": {
    "keep": true
  },
  "provider": {
    "builtin:demo": {
      "name": "Demo",
      "models": {
        "M1": {
          "reasoning": {
            "enabled": true,
            "variants": [
              "low",
              "max"
            ],
            "defaultVariant": "max"
          },
          "zcode": {
            "priority": 99
          }
        }
      }
    }
  }
}
`

func TestRoundTripPreservesEverything(t *testing.T) {
	v, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalIndent(v, "  ", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != sample {
		t.Fatalf("round-trip 改变了文件内容:\n--- want ---\n%s\n--- got ---\n%s", sample, string(out))
	}
}

func TestEditPreservesUnknownFieldsAndOrder(t *testing.T) {
	v, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	// 修改 reasoning.variants
	m := v.Get("provider").Get("builtin:demo").Get("models").Get("M1")
	m.Get("reasoning").SetStrings("variants", []string{"high", "max"})
	// 追加新键到模型
	m.SetInt("limit", 100)

	out, err := MarshalIndent(v, "  ", true)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`"_unknownTopLevel"`, // 顶层未知字段保留
		`"zcode"`,            // 未编辑字段保留
		`"priority"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("丢失字段 %q", want)
		}
	}
	if !strings.Contains(s, `"high"`) || strings.Contains(s, `"low"`) {
		t.Fatalf("variants 未被正确替换")
	}
	// 键序：name 仍在 models 前面，zcode 仍在 M1 内最后
	if strings.Index(s, `"name"`) > strings.Index(s, `"models"`) {
		t.Fatalf("键序被重排")
	}
	if !strings.HasSuffix(s, "\n") {
		t.Fatalf("末尾换行丢失")
	}
}

func TestRenameKeepsPosition(t *testing.T) {
	v, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	p := v.Get("provider")
	if !p.RenameKey("builtin:demo", "renamed") {
		t.Fatal("RenameKey 失败")
	}
	out, _ := MarshalIndent(v, "  ", false)
	s := string(out)
	if !strings.Contains(s, `"renamed"`) || strings.Contains(s, "builtin:demo") {
		t.Fatal("重命名未生效")
	}
	iName := strings.Index(s, `"name"`)
	iRenamed := strings.Index(s, `"renamed"`)
	if iName < iRenamed {
		t.Fatal("重命名后键序改变")
	}
}

func TestDetectIndent(t *testing.T) {
	if got := DetectIndent([]byte(sample)); got != "  " {
		t.Fatalf("DetectIndent = %q, want 2 spaces", got)
	}
}
