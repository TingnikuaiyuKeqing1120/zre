// Package jsondom 提供保序的 JSON DOM，用于对配置文件做"外科手术式"编辑：
// 解析时保留对象键的原始顺序与数字字面量，序列化时按原缩进格式还原，
// 从而保证 ZRE 保存后文件里未被编辑的部分保持原样（未知字段、键序均不丢）。
package jsondom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

type Kind int

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Value 是一个 JSON 节点。对象的键序通过 Keys 保序。
type Value struct {
	Kind Kind
	Bool bool
	Num  json.Number
	Str  string
	Arr  []*Value

	Keys []string
	vals map[string]*Value
}

func NewObject() *Value { return &Value{Kind: KindObject, vals: map[string]*Value{}} }
func NewArray() *Value  { return &Value{Kind: KindArray} }
func NewNull() *Value   { return &Value{Kind: KindNull} }
func NewString(s string) *Value {
	return &Value{Kind: KindString, Str: s}
}
func NewBool(b bool) *Value { return &Value{Kind: KindBool, Bool: b} }
func NewInt(n int64) *Value {
	return &Value{Kind: KindNumber, Num: json.Number(strconv.FormatInt(n, 10))}
}

// ---- 对象操作 ----

// Get 返回键对应的节点；键不存在或 v 不是对象时返回 nil。
func (v *Value) Get(key string) *Value {
	if v == nil || v.Kind != KindObject {
		return nil
	}
	return v.vals[key]
}

func (v *Value) Has(key string) bool { return v.Get(key) != nil }

// Set 写入键值。键已存在时原位替换（保持键序），否则追加到末尾。
func (v *Value) Set(key string, val *Value) {
	if v.Kind != KindObject {
		panic("jsondom: Set on non-object")
	}
	if _, ok := v.vals[key]; !ok {
		v.Keys = append(v.Keys, key)
	}
	v.vals[key] = val
}

func (v *Value) Delete(key string) bool {
	if v == nil || v.Kind != KindObject {
		return false
	}
	if _, ok := v.vals[key]; !ok {
		return false
	}
	delete(v.vals, key)
	for i, k := range v.Keys {
		if k == key {
			v.Keys = append(v.Keys[:i], v.Keys[i+1:]...)
			break
		}
	}
	return true
}

// RenameKey 原位重命名键（保持键序与值）。
func (v *Value) RenameKey(old, neu string) bool {
	if v == nil || v.Kind != KindObject || old == neu {
		return false
	}
	val, ok := v.vals[old]
	if !ok || v.vals[neu] != nil {
		return false
	}
	delete(v.vals, old)
	v.vals[neu] = val
	for i, k := range v.Keys {
		if k == old {
			v.Keys[i] = neu
			break
		}
	}
	return true
}

// ---- 数组操作 ----

func (v *Value) Append(item *Value) {
	if v.Kind != KindArray {
		panic("jsondom: Append on non-array")
	}
	v.Arr = append(v.Arr, item)
}

// ---- 宽容的类型化读取（nil 安全） ----

func (v *Value) GetString(key, def string) string {
	n := v.Get(key)
	if n == nil || n.Kind != KindString {
		return def
	}
	return n.Str
}

func (v *Value) GetBool(key string, def bool) bool {
	n := v.Get(key)
	if n == nil || n.Kind != KindBool {
		return def
	}
	return n.Bool
}

func (v *Value) GetInt(key string, def int64) int64 {
	n := v.Get(key)
	if n == nil || n.Kind != KindNumber {
		return def
	}
	if i, err := n.Num.Int64(); err == nil {
		return i
	}
	if f, err := n.Num.Float64(); err == nil {
		return int64(f)
	}
	return def
}

// GetStrings 读取字符串数组；键缺失或类型不符时返回 nil。
func (v *Value) GetStrings(key string) []string {
	n := v.Get(key)
	if n == nil || n.Kind != KindArray {
		return nil
	}
	out := make([]string, 0, len(n.Arr))
	for _, item := range n.Arr {
		if item.Kind == KindString {
			out = append(out, item.Str)
		}
	}
	return out
}

// ---- 类型化写入 ----

func (v *Value) SetString(key, s string) { v.Set(key, NewString(s)) }
func (v *Value) SetBool(key string, b bool) {
	v.Set(key, NewBool(b))
}
func (v *Value) SetInt(key string, n int64) { v.Set(key, NewInt(n)) }

func (v *Value) SetStrings(key string, ss []string) {
	arr := NewArray()
	for _, s := range ss {
		arr.Append(NewString(s))
	}
	v.Set(key, arr)
}

// ---- 解析 ----

func Parse(data []byte) (*Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	root, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("jsondom: 多余的尾部数据")
	} else if err != io.EOF {
		return nil, err
	}
	return root, nil
}

func parseValue(d *json.Decoder) (*Value, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	return valueFromToken(d, tok)
}

func valueFromToken(d *json.Decoder, tok json.Token) (*Value, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := NewObject()
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("jsondom: 期望对象键，得到 %T", kt)
				}
				val, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				obj.Set(key, val)
			}
			if _, err := d.Token(); err != nil { // 消费 '}'
				return nil, err
			}
			return obj, nil
		case '[':
			arr := NewArray()
			for d.More() {
				val, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				arr.Append(val)
			}
			if _, err := d.Token(); err != nil { // 消费 ']'
				return nil, err
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("jsondom: 意外的分隔符 %q", t)
		}
	case string:
		return &Value{Kind: KindString, Str: t}, nil
	case json.Number:
		return &Value{Kind: KindNumber, Num: t}, nil
	case bool:
		return &Value{Kind: KindBool, Bool: t}, nil
	case nil:
		return &Value{Kind: KindNull}, nil
	}
	return nil, fmt.Errorf("jsondom: 意外的 token 类型 %T", tok)
}

// ---- 序列化 ----

// MarshalIndent 以指定缩进序列化（先紧凑编码再统一缩进，保证键序与数字字面量不变）。
func MarshalIndent(v *Value, indent string, finalNewline bool) ([]byte, error) {
	var compact bytes.Buffer
	if err := writeCompact(&compact, v); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", indent); err != nil {
		return nil, err
	}
	if finalNewline {
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

func writeCompact(b *bytes.Buffer, v *Value) error {
	switch v.Kind {
	case KindNull:
		b.WriteString("null")
	case KindBool:
		if v.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case KindNumber:
		if !json.Valid([]byte(v.Num)) {
			return fmt.Errorf("jsondom: 非法数字字面量 %q", string(v.Num))
		}
		b.WriteString(string(v.Num))
	case KindString:
		s, err := encodeString(v.Str)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case KindArray:
		b.WriteByte('[')
		for i, item := range v.Arr {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCompact(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case KindObject:
		b.WriteByte('{')
		for i, k := range v.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			ks, err := encodeString(k)
			if err != nil {
				return err
			}
			b.WriteString(ks)
			b.WriteByte(':')
			item := v.vals[k]
			if item == nil {
				return fmt.Errorf("jsondom: 键 %q 缺少值", k)
			}
			if err := writeCompact(b, item); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	}
	return nil
}

func encodeString(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// ---- 格式探测 ----

var indentRe = regexp.MustCompile(`\n([ \t]+)[^ \t\n\r]`)

// DetectIndent 从原始文件内容猜测缩进字符串，默认两个空格。
func DetectIndent(data []byte) string {
	if m := indentRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return "  "
}

func HasFinalNewline(data []byte) bool {
	return len(data) > 0 && data[len(data)-1] == '\n'
}
