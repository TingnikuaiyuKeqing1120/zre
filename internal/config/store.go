// Package config 负责 ~/.zcode/v2/config.json 的加载、编辑与安全保存。
// 所有编辑基于保序 JSON DOM，保存时不重排键序、不丢未知字段；
// 保存前自动备份到 backups/ 目录；检测到文件被外部修改时拒绝静默覆盖。
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zre/internal/jsondom"
	"zre/internal/snapshot"
)

var ErrConflict = errors.New("配置文件在本工具之外被修改过（可能 zcode 刚写入新配置），保存会覆盖这些外部更改")

const orderFileName = "model-provider-display-order.json"

type Store struct {
	mu sync.Mutex

	path    string
	dir     string
	raw     []byte
	root    *jsondom.Value
	indent  string
	finalNL bool
	exists  bool
	dirty   bool

	order      *jsondom.Value // model-provider-display-order.json 内容，可能为 nil
	orderRaw   []byte
	orderDirty bool

	templates []Template
	settings  Settings

	backupsDir string
	snaps      *snapshot.Manager

	LastBackupPath string
	LastSavedAt    time.Time
}

// ---- 视图（返回给前端的结构） ----

type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	HasReasoning  bool     `json:"hasReasoning"`
	Enabled       bool     `json:"reasoningEnabled"`
	Variants      []string `json:"variants"`
	Default       string   `json:"defaultVariant"`
	HasLimit      bool     `json:"hasLimit"`
	Context       int64    `json:"context"`
	Output        int64    `json:"output"`
	HasModalities bool     `json:"hasModalities"`
	InputMod      []string `json:"inputModalities"`
	OutputMod     []string `json:"outputModalities"`
	Priority      int64    `json:"priority"`
	Modified      bool     `json:"zcodeModified"`
}

type Provider struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Kind                 string   `json:"kind"`
	Source               string   `json:"source"`
	BaseURL              string   `json:"baseURL"`
	APIKey               string   `json:"apiKey"`
	APIKeyRequired       bool     `json:"apiKeyRequired"`
	EnabledSet           bool     `json:"enabledSet"`
	Enabled              bool     `json:"enabled"`
	SystemDisabledReason string   `json:"systemDisabledReason"`
	InOrder              bool     `json:"inOrder"`
	Models               []*Model `json:"models"`
}

type Stats struct {
	Providers        int `json:"providers"`
	DisabledProvider int `json:"disabledProviders"`
	Models           int `json:"models"`
	ReasoningModels  int `json:"reasoningModels"`
}

type State struct {
	ConfigPath     string      `json:"configPath"`
	OrderPath      string      `json:"orderPath"`
	FileExists     bool        `json:"fileExists"`
	Dirty          bool        `json:"dirty"`
	Providers      []*Provider `json:"providers"`
	Stats          Stats       `json:"stats"`
	LastBackupPath string      `json:"lastBackupPath"`
	LastSavedAt    *time.Time  `json:"lastSavedAt"`
}

// ---- 打开与加载 ----

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s := &Store{
		path:       abs,
		dir:        filepath.Dir(abs),
		indent:     "  ",
		finalNL:    true,
		backupsDir: filepath.Join(filepath.Dir(abs), "backups"),
	}
	s.snaps = snapshot.NewManager(filepath.Join(s.dir, "snapshots"))
	if err := s.load(); err != nil {
		return nil, err
	}
	s.loadTemplates()
	s.loadSettings()
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.raw = nil
			s.exists = false
			s.root = jsondom.NewObject()
			s.root.Set("provider", jsondom.NewObject())
			s.dirty = false
			s.loadOrder()
			return nil
		}
		return fmt.Errorf("读取配置失败: %w", err)
	}
	root, err := jsondom.Parse(data)
	if err != nil {
		return fmt.Errorf("配置文件不是合法 JSON: %w", err)
	}
	if root.Get("provider") == nil || root.Get("provider").Kind != jsondom.KindObject {
		root.Set("provider", jsondom.NewObject())
	}
	s.raw = data
	s.root = root
	s.exists = true
	s.dirty = false
	s.indent = jsondom.DetectIndent(data)
	s.finalNL = jsondom.HasFinalNewline(data)
	s.loadOrder()
	return nil
}

func (s *Store) loadOrder() {
	s.order, s.orderRaw, s.orderDirty = nil, nil, false
	data, err := os.ReadFile(s.OrderPath())
	if err != nil {
		return
	}
	root, err := jsondom.Parse(data)
	if err != nil || root.Kind != jsondom.KindObject {
		return
	}
	s.order = root
	s.orderRaw = data
}

func (s *Store) ConfigPath() string           { return s.path }
func (s *Store) OrderPath() string            { return filepath.Join(s.dir, orderFileName) }
func (s *Store) Snapshots() *snapshot.Manager { return s.snaps }

// ---- 状态构建 ----

func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

func (s *Store) stateLocked() State {
	st := State{
		ConfigPath:     s.path,
		OrderPath:      s.OrderPath(),
		FileExists:     s.exists,
		Dirty:          s.dirty || s.orderDirty,
		LastBackupPath: s.LastBackupPath,
	}
	if !s.LastSavedAt.IsZero() {
		t := s.LastSavedAt
		st.LastSavedAt = &t
	}

	// 提供商侧栏顺序 = 显示顺序文件中的现存项 + 未收录项（按配置文件顺序追加）
	var orderIDs []string
	if s.order != nil {
		orderIDs = s.order.GetStrings("providerIds")
	}
	seen := map[string]bool{}
	var sideOrder []string
	for _, id := range orderIDs {
		if !seen[id] && s.providerObj(id) != nil {
			seen[id] = true
			sideOrder = append(sideOrder, id)
		}
	}
	if pv := s.root.Get("provider"); pv != nil {
		for _, id := range pv.Keys {
			if !seen[id] {
				seen[id] = true
				sideOrder = append(sideOrder, id)
			}
		}
	}

	for _, id := range sideOrder {
		p := s.buildProvider(id)
		if p == nil {
			continue
		}
		st.Providers = append(st.Providers, p)
		st.Stats.Providers++
		if p.EnabledSet && !p.Enabled {
			st.Stats.DisabledProvider++
		}
		for _, m := range p.Models {
			st.Stats.Models++
			if m.HasReasoning && m.Enabled {
				st.Stats.ReasoningModels++
			}
		}
	}
	if st.Providers == nil {
		st.Providers = []*Provider{}
	}
	return st
}

func (s *Store) providerObj(id string) *jsondom.Value {
	pv := s.root.Get("provider")
	if pv == nil {
		return nil
	}
	return pv.Get(id)
}

func (s *Store) buildProvider(id string) *Provider {
	obj := s.providerObj(id)
	if obj == nil {
		return nil
	}
	p := &Provider{
		ID:                   id,
		Name:                 obj.GetString("name", ""),
		Kind:                 obj.GetString("kind", ""),
		Source:               obj.GetString("source", ""),
		SystemDisabledReason: obj.GetString("systemDisabledReason", ""),
	}
	if opts := obj.Get("options"); opts != nil && opts.Kind == jsondom.KindObject {
		p.BaseURL = opts.GetString("baseURL", "")
		p.APIKey = opts.GetString("apiKey", "")
		p.APIKeyRequired = opts.GetBool("apiKeyRequired", false)
	}
	if en := obj.Get("enabled"); en != nil {
		p.EnabledSet = true
		p.Enabled = en.Kind == jsondom.KindBool && en.Bool
	}
	if s.order != nil {
		for _, oid := range s.order.GetStrings("providerIds") {
			if oid == id {
				p.InOrder = true
				break
			}
		}
	}
	if mv := obj.Get("models"); mv != nil && mv.Kind == jsondom.KindObject {
		for _, mid := range mv.Keys {
			if m := buildModel(mid, mv.Get(mid)); m != nil {
				p.Models = append(p.Models, m)
			}
		}
	}
	if p.Models == nil {
		p.Models = []*Model{}
	}
	return p
}

func buildModel(id string, obj *jsondom.Value) *Model {
	if obj == nil || obj.Kind != jsondom.KindObject {
		return nil
	}
	m := &Model{ID: id, Name: obj.GetString("name", "")}
	if rv := obj.Get("reasoning"); rv != nil && rv.Kind == jsondom.KindObject {
		m.HasReasoning = true
		m.Enabled = rv.GetBool("enabled", false)
		m.Variants = rv.GetStrings("variants")
		m.Default = rv.GetString("defaultVariant", "")
	}
	if lv := obj.Get("limit"); lv != nil && lv.Kind == jsondom.KindObject {
		m.HasLimit = true
		m.Context = lv.GetInt("context", 0)
		m.Output = lv.GetInt("output", 0)
	}
	if m.Variants == nil {
		m.Variants = []string{}
	}
	if m.InputMod == nil {
		m.InputMod = []string{}
	}
	if m.OutputMod == nil {
		m.OutputMod = []string{}
	}
	if mv := obj.Get("modalities"); mv != nil && mv.Kind == jsondom.KindObject {
		m.HasModalities = true
		m.InputMod = mv.GetStrings("input")
		m.OutputMod = mv.GetStrings("output")
	}
	if zv := obj.Get("zcode"); zv != nil && zv.Kind == jsondom.KindObject {
		m.Priority = zv.GetInt("priority", 0)
		m.Modified = zv.GetBool("modified", false)
	}
	return m
}

// ---- 请求结构 ----

type ProviderInput struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

type ProviderUpdate struct {
	Name    *string `json:"name"`
	Kind    *string `json:"kind"`
	BaseURL *string `json:"baseURL"`
	APIKey  *string `json:"apiKey"`
	Enabled *string `json:"enabled"` // "unset" | "true" | "false"
}

type ReasoningInput struct {
	Enabled        bool     `json:"enabled"`
	Variants       []string `json:"variants"`
	DefaultVariant string   `json:"defaultVariant"`
}

type LimitInput struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

type ModalitiesInput struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type ModelInput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ModelUpdate struct {
	Name             *string          `json:"name"` // 空串表示移除显示名
	Reasoning        *ReasoningInput  `json:"reasoning"`
	RemoveReasoning  bool             `json:"removeReasoning"`
	Limit            *LimitInput      `json:"limit"` // 双 0 表示移除
	RemoveLimit      bool             `json:"removeLimit"`
	Modalities       *ModalitiesInput `json:"modalities"` // 双空表示移除
	RemoveModalities bool             `json:"removeModalities"`
}

type Result struct {
	State    State
	Warnings []string
}

func ok(s *Store, warnings ...string) Result {
	return Result{State: s.stateLocked(), Warnings: warnings}
}

// ---- 校验工具 ----

func cleanID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("ID 不能为空")
	}
	if len(id) > 200 {
		return "", fmt.Errorf("ID 过长")
	}
	for _, r := range id {
		if r < 0x20 {
			return "", fmt.Errorf("ID 不能包含控制字符")
		}
	}
	return id, nil
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// ---- 提供商操作 ----

func (s *Store) AddProvider(in ProviderInput) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = uuid()
	}
	id, err := cleanID(id)
	if err != nil {
		return Result{}, fmt.Errorf("提供商 ID 无效: %w", err)
	}
	if strings.HasPrefix(id, "builtin:") {
		return Result{}, fmt.Errorf(`"builtin:" 前缀保留给内置提供商，请换一个 ID`)
	}
	if s.providerObj(id) != nil {
		return Result{}, fmt.Errorf("提供商 %q 已存在", id)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Result{}, fmt.Errorf("名称不能为空")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "openai-compatible"
	}

	obj := jsondom.NewObject()
	obj.SetString("name", name)
	obj.SetString("kind", kind)
	opts := jsondom.NewObject()
	opts.SetString("apiKey", in.APIKey)
	opts.SetString("baseURL", strings.TrimSpace(in.BaseURL))
	obj.Set("options", opts)
	obj.SetString("source", "custom")
	obj.Set("models", jsondom.NewObject())

	s.root.Get("provider").Set(id, obj)
	s.dirty = true
	s.orderAppend(id)
	return ok(s), nil
}

func (s *Store) orderAppend(id string) {
	if s.order == nil {
		return
	}
	for _, oid := range s.order.GetStrings("providerIds") {
		if oid == id {
			return
		}
	}
	arr := s.order.Get("providerIds")
	if arr == nil || arr.Kind != jsondom.KindArray {
		s.order.SetStrings("providerIds", []string{id})
	} else {
		arr.Append(jsondom.NewString(id))
	}
	s.orderDirty = true
}

func (s *Store) orderRemove(id string) {
	if s.order == nil {
		return
	}
	ids := s.order.GetStrings("providerIds")
	var kept []string
	found := false
	for _, oid := range ids {
		if oid == id {
			found = true
			continue
		}
		kept = append(kept, oid)
	}
	if found {
		s.order.SetStrings("providerIds", kept)
		s.orderDirty = true
	}
}

func (s *Store) UpdateProvider(id string, in ProviderUpdate) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	obj := s.providerObj(id)
	if obj == nil {
		return Result{}, fmt.Errorf("提供商 %q 不存在", id)
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return Result{}, fmt.Errorf("名称不能为空")
		}
		obj.SetString("name", name)
	}
	if in.Kind != nil {
		kind := strings.TrimSpace(*in.Kind)
		if kind == "" {
			return Result{}, fmt.Errorf("类型不能为空")
		}
		obj.SetString("kind", kind)
	}
	if in.BaseURL != nil || in.APIKey != nil {
		opts := obj.Get("options")
		if opts == nil || opts.Kind != jsondom.KindObject {
			opts = jsondom.NewObject()
			obj.Set("options", opts)
		}
		if in.BaseURL != nil {
			opts.SetString("baseURL", strings.TrimSpace(*in.BaseURL))
		}
		if in.APIKey != nil {
			opts.SetString("apiKey", *in.APIKey)
		}
	}
	if in.Enabled != nil {
		switch *in.Enabled {
		case "unset":
			obj.Delete("enabled")
		case "true":
			obj.SetBool("enabled", true)
		case "false":
			obj.SetBool("enabled", false)
		}
	}
	s.dirty = true
	return ok(s), nil
}

func (s *Store) DeleteProvider(id string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pv := s.root.Get("provider")
	if pv == nil || pv.Get(id) == nil {
		return Result{}, fmt.Errorf("提供商 %q 不存在", id)
	}
	pv.Delete(id)
	s.orderRemove(id)
	s.dirty = true
	return ok(s), nil
}

func (s *Store) RenameProvider(id, newID string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newID, err := cleanID(newID)
	if err != nil {
		return Result{}, fmt.Errorf("新 ID 无效: %w", err)
	}
	if newID == id {
		return ok(s), nil
	}
	if strings.HasPrefix(newID, "builtin:") {
		return Result{}, fmt.Errorf(`"builtin:" 前缀保留给内置提供商`)
	}
	pv := s.root.Get("provider")
	if pv == nil || pv.Get(id) == nil {
		return Result{}, fmt.Errorf("提供商 %q 不存在", id)
	}
	if pv.Get(newID) != nil {
		return Result{}, fmt.Errorf("提供商 %q 已存在", newID)
	}
	if !pv.RenameKey(id, newID) {
		return Result{}, fmt.Errorf("重命名失败")
	}
	// 同步显示顺序文件，保持原位置
	if s.order != nil {
		ids := s.order.GetStrings("providerIds")
		for i, oid := range ids {
			if oid == id {
				ids[i] = newID
				s.order.SetStrings("providerIds", ids)
				s.orderDirty = true
				break
			}
		}
	}
	s.dirty = true
	return ok(s), nil
}

func (s *Store) ReorderProviders(ids []string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.order == nil {
		s.order = jsondom.NewObject()
		s.order.Set("providerIds", jsondom.NewArray())
	}
	// 归一化：未传入的现有提供商追加到末尾
	current := map[string]bool{}
	if pv := s.root.Get("provider"); pv != nil {
		for _, id := range pv.Keys {
			current[id] = true
		}
	}
	seen := map[string]bool{}
	var final []string
	for _, id := range ids {
		if current[id] && !seen[id] {
			seen[id] = true
			final = append(final, id)
		}
	}
	if pv := s.root.Get("provider"); pv != nil {
		for _, id := range pv.Keys {
			if !seen[id] {
				final = append(final, id)
			}
		}
	}
	s.order.SetStrings("providerIds", final)
	s.orderDirty = true
	return ok(s), nil
}

// ---- 模型操作 ----

func (s *Store) modelContainer(providerID string, create bool) (*jsondom.Value, error) {
	obj := s.providerObj(providerID)
	if obj == nil {
		return nil, fmt.Errorf("提供商 %q 不存在", providerID)
	}
	mv := obj.Get("models")
	if mv == nil || mv.Kind != jsondom.KindObject {
		if !create {
			return nil, fmt.Errorf("提供商 %q 没有模型", providerID)
		}
		mv = jsondom.NewObject()
		obj.Set("models", mv)
	}
	return mv, nil
}

func (s *Store) AddModel(providerID string, in ModelInput) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := cleanID(in.ID)
	if err != nil {
		return Result{}, fmt.Errorf("模型 ID 无效: %w", err)
	}
	mv, err := s.modelContainer(providerID, true)
	if err != nil {
		return Result{}, err
	}
	if mv.Get(id) != nil {
		return Result{}, fmt.Errorf("模型 %q 已存在", id)
	}
	obj := jsondom.NewObject()
	if name := strings.TrimSpace(in.Name); name != "" {
		obj.SetString("name", name)
	}
	mv.Set(id, obj)
	s.dirty = true
	return ok(s), nil
}

func (s *Store) DeleteModel(providerID, id string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mv, err := s.modelContainer(providerID, false)
	if err != nil {
		return Result{}, err
	}
	if mv.Get(id) == nil {
		return Result{}, fmt.Errorf("模型 %q 不存在", id)
	}
	mv.Delete(id)
	s.dirty = true
	return ok(s), nil
}

// ReorderModels 重排提供商内模型的顺序（models 对象键序即 zcode 显示顺序）。
func (s *Store) ReorderModels(providerID string, ids []string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	obj := s.providerObj(providerID)
	if obj == nil {
		return Result{}, fmt.Errorf("提供商 %q 不存在", providerID)
	}
	mv := obj.Get("models")
	if mv == nil || mv.Kind != jsondom.KindObject || len(mv.Keys) == 0 {
		return Result{}, fmt.Errorf("提供商 %q 没有可排序的模型", providerID)
	}
	existing := map[string]bool{}
	for _, k := range mv.Keys {
		existing[k] = true
	}
	seen := map[string]bool{}
	var order []string
	for _, id := range ids {
		if existing[id] && !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	for _, k := range mv.Keys { // 未传入的模型按原顺序追加
		if !seen[k] {
			order = append(order, k)
		}
	}
	neu := jsondom.NewObject()
	for _, id := range order {
		neu.Set(id, mv.Get(id))
	}
	obj.Set("models", neu)
	s.dirty = true
	return ok(s), nil
}

func (s *Store) RenameModel(providerID, id, newID string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newID, err := cleanID(newID)
	if err != nil {
		return Result{}, fmt.Errorf("新 ID 无效: %w", err)
	}
	if newID == id {
		return ok(s), nil
	}
	mv, err := s.modelContainer(providerID, false)
	if err != nil {
		return Result{}, err
	}
	if mv.Get(id) == nil {
		return Result{}, fmt.Errorf("模型 %q 不存在", id)
	}
	if mv.Get(newID) != nil {
		return Result{}, fmt.Errorf("模型 %q 已存在", newID)
	}
	if !mv.RenameKey(id, newID) {
		return Result{}, fmt.Errorf("重命名失败")
	}
	s.dirty = true
	return ok(s), nil
}

func (s *Store) UpdateModel(providerID, id string, in ModelUpdate) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	mv, err := s.modelContainer(providerID, false)
	if err != nil {
		return Result{}, err
	}
	obj := mv.Get(id)
	if obj == nil {
		return Result{}, fmt.Errorf("模型 %q 不存在", id)
	}
	var warnings []string

	if in.Name != nil {
		if name := strings.TrimSpace(*in.Name); name != "" {
			obj.SetString("name", name)
		} else {
			obj.Delete("name")
		}
	}

	if in.RemoveReasoning {
		obj.Delete("reasoning")
	} else if in.Reasoning != nil {
		// 归一化：去空、去重（保序）
		var variants []string
		seen := map[string]bool{}
		for _, v := range in.Reasoning.Variants {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			variants = append(variants, v)
		}
		r := jsondom.NewObject()
		r.SetBool("enabled", in.Reasoning.Enabled)
		r.Set("variants", jsondom.NewArray())
		if len(variants) > 0 {
			r.SetStrings("variants", variants)
			def := strings.TrimSpace(in.Reasoning.DefaultVariant)
			valid := seen[def]
			if def != "" && !valid {
				warnings = append(warnings, fmt.Sprintf("默认档位 %q 不在档位列表中，已改为 %q", def, variants[0]))
			}
			if !valid {
				def = variants[0]
			}
			r.SetString("defaultVariant", def)
		}
		if in.Reasoning.Enabled && len(variants) == 0 {
			warnings = append(warnings, "已启用思考但档位列表为空，zcode 可能按默认行为处理")
		}
		obj.Set("reasoning", r)
	}

	if in.RemoveLimit {
		obj.Delete("limit")
	} else if in.Limit != nil {
		if in.Limit.Context <= 0 && in.Limit.Output <= 0 {
			obj.Delete("limit")
		} else {
			l := jsondom.NewObject()
			l.SetInt("context", max64(in.Limit.Context, 0))
			l.SetInt("output", max64(in.Limit.Output, 0))
			obj.Set("limit", l)
		}
	}

	if in.RemoveModalities {
		obj.Delete("modalities")
	} else if in.Modalities != nil {
		inMo := cleanList(in.Modalities.Input)
		outMo := cleanList(in.Modalities.Output)
		if len(inMo) == 0 && len(outMo) == 0 {
			obj.Delete("modalities")
		} else {
			mo := jsondom.NewObject()
			mo.SetStrings("input", inMo)
			mo.SetStrings("output", outMo)
			obj.Set("modalities", mo)
		}
	}

	s.dirty = true
	return ok(s, warnings...), nil
}

func cleanList(ss []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range ss {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ---- 原始内容 ----

// RawJSON 返回当前（含未保存修改）配置的格式化文本。
func (s *Store) RawJSON() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := jsondom.MarshalIndent(s.root, s.indent, s.finalNL)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ---- 保存 / 重载 ----

type SaveInfo struct {
	Saved      bool   `json:"saved"`
	NoChange   bool   `json:"noChange"`
	Conflict   bool   `json:"conflict"`
	BackupPath string `json:"backupPath"`
}

func (s *Store) Save(force bool) (SaveInfo, State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := jsondom.MarshalIndent(s.root, s.indent, s.finalNL)
	if err != nil {
		return SaveInfo{}, State{}, err
	}

	info := SaveInfo{}
	if !s.dirty && !s.orderDirty {
		info.NoChange = true
		return info, s.stateLocked(), nil
	}

	disk, derr := os.ReadFile(s.path)
	if s.exists && derr == nil && !bytes.Equal(disk, s.raw) && !force {
		return SaveInfo{Conflict: true}, s.stateLocked(), ErrConflict
	}

	// 备份磁盘上的当前版本
	if s.exists && derr == nil && len(disk) > 0 {
		bp, err := s.backup(disk)
		if err != nil {
			return SaveInfo{}, s.stateLocked(), fmt.Errorf("备份失败，已取消保存: %w", err)
		}
		info.BackupPath = bp
		s.LastBackupPath = bp
	}

	if s.dirty {
		if err := writeAtomic(s.path, current); err != nil {
			return SaveInfo{}, s.stateLocked(), fmt.Errorf("写入配置失败: %w", err)
		}
		s.raw = current
		s.exists = true
		s.dirty = false
		info.Saved = true
	}

	if s.orderDirty {
		s.order.SetInt("updatedAt", time.Now().UnixMilli())
		odata, err := jsondom.MarshalIndent(s.order, "  ", true)
		if err != nil {
			return SaveInfo{}, s.stateLocked(), fmt.Errorf("序列化显示顺序失败: %w", err)
		}
		if err := writeAtomic(s.OrderPath(), odata); err != nil {
			return SaveInfo{}, s.stateLocked(), fmt.Errorf("写入显示顺序文件失败: %w", err)
		}
		s.orderRaw = odata
		s.orderDirty = false
		info.Saved = true
	}

	s.LastSavedAt = time.Now()
	return info, s.stateLocked(), nil
}

func (s *Store) backup(data []byte) (string, error) {
	if err := os.MkdirAll(s.backupsDir, 0o755); err != nil {
		return "", err
	}
	name := "config_zre_" + time.Now().Format("20060102_150405") + ".json"
	p := filepath.Join(s.backupsDir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".zre-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// SnapshotSave / SnapshotRestore / SnapshotDelete 供服务层调用。

func (s *Store) SnapshotSave(name string, allowOverwrite bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			// 配置尚不存在时快照当前内存内容
			data, err = jsondom.MarshalIndent(s.root, s.indent, s.finalNL)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	return s.snaps.Save(name, data, allowOverwrite)
}

func (s *Store) SnapshotRestore(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.snaps.Read(name)
	if err != nil {
		return "", err
	}
	if _, err := jsondom.Parse(data); err != nil {
		return "", fmt.Errorf("快照内容不是合法 JSON: %w", err)
	}
	var backupPath string
	if s.exists {
		disk, derr := os.ReadFile(s.path)
		if derr == nil && len(disk) > 0 {
			if backupPath, err = s.backup(disk); err != nil {
				return "", fmt.Errorf("恢复前备份失败: %w", err)
			}
		}
	}
	if err := writeAtomic(s.path, data); err != nil {
		return backupPath, fmt.Errorf("恢复快照失败: %w", err)
	}
	if err := s.load(); err != nil {
		return backupPath, fmt.Errorf("快照已写入但重新加载失败: %w", err)
	}
	return backupPath, nil
}

func (s *Store) SnapshotDelete(name string) error {
	return s.snaps.Delete(name)
}

func (s *Store) SnapshotList() ([]snapshot.Info, error) {
	return s.snaps.List()
}
