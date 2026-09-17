// Package server 提供 ZRE 的本地 HTTP API 与内嵌 Web 界面。
// 仅监听 127.0.0.1。
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"zre/internal/catalog"
	"zre/internal/config"
	"zre/internal/snapshot"
)

//go:embed web
var webFS embed.FS

type Options struct {
	CatalogPaths   []string
	CatalogFromCLI bool   // --catalog 启动参数锁定目录来源
	Quit           func() // Web"退出"与自动退出回调
}

type Server struct {
	store *config.Store
	mux   *http.ServeMux

	catMu      sync.Mutex
	cat        *catalog.Index
	catPaths   []string
	catFromCLI bool

	quit func()

	hbMu       sync.Mutex
	lastHb     int64 // 最近一次页面心跳（unix 秒）
	hbEverSeen bool
}

func New(st *config.Store, o Options) *Server {
	sv := &Server{
		store:      st,
		mux:        http.NewServeMux(),
		cat:        nil,
		catPaths:   o.CatalogPaths,
		catFromCLI: o.CatalogFromCLI,
		quit:       o.Quit,
	}
	// 初始目录索引
	if ix, err := catalog.LoadPaths(sv.catPaths); err == nil {
		sv.cat = ix
	}
	sv.routes()
	go sv.autoQuitWatch()
	return sv
}

// autoQuitWatch 监视页面心跳：开启"关闭网页自动退出"后，超过 15 秒无任何
// 存活标签页的心跳即退出进程（多标签任一存活即续命）。
func (sv *Server) autoQuitWatch() {
	for {
		time.Sleep(3 * time.Second)
		if !sv.store.Settings().AutoQuitOnPageClose {
			continue
		}
		sv.hbMu.Lock()
		seen, last := sv.hbEverSeen, sv.lastHb
		sv.hbMu.Unlock()
		if !seen {
			continue // 页面至少心跳过一次才开始计时
		}
		if time.Now().Unix()-last > 15 {
			log.Println("所有页面已关闭超过 15 秒，自动退出（可在设置中关闭该行为）")
			if sv.quit != nil {
				sv.quit()
			}
			return
		}
	}
}

func (sv *Server) catalog() *catalog.Index {
	sv.catMu.Lock()
	defer sv.catMu.Unlock()
	return sv.cat
}

// RescanCatalog 重新加载官方模型目录（zcode 更新目录文件后可手动刷新）。
func (sv *Server) RescanCatalog() (int, error) {
	if len(sv.catPaths) == 0 {
		return 0, fmt.Errorf("未配置官方目录路径，请用 --catalog 指定 models_catalog*.json 的路径")
	}
	ix, err := catalog.LoadPaths(sv.catPaths)
	if err != nil {
		return 0, err
	}
	sv.catMu.Lock()
	sv.cat = ix
	n := ix.Size()
	sv.catMu.Unlock()
	return n, nil
}

func (sv *Server) Handler() http.Handler { return sv.mux }

func (sv *Server) routes() {
	sub, err := fs.Sub(webFS, "web")
	if err == nil {
		sv.mux.Handle("GET /", http.FileServerFS(sub))
	}
	sv.mux.HandleFunc("GET /api/state", sv.handleState)
	sv.mux.HandleFunc("GET /api/raw", sv.handleRaw)
	sv.mux.HandleFunc("POST /api/save", sv.handleSave)
	sv.mux.HandleFunc("POST /api/reload", sv.handleReload)

	sv.mux.HandleFunc("POST /api/provider/add", sv.mutate(func(body []byte) (config.Result, error) {
		var in config.ProviderInput
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.AddProvider(in)
	}))
	sv.mux.HandleFunc("POST /api/provider/update", sv.mutate(func(body []byte) (config.Result, error) {
		var in struct {
			ID string `json:"id"`
			config.ProviderUpdate
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.UpdateProvider(in.ID, in.ProviderUpdate)
	}))
	sv.mux.HandleFunc("POST /api/provider/delete", sv.mutateID(func(id string, _ []byte) (config.Result, error) {
		return sv.store.DeleteProvider(id)
	}))
	sv.mux.HandleFunc("POST /api/provider/rename-id", sv.mutateID(func(id string, body []byte) (config.Result, error) {
		var in struct {
			NewID string `json:"newId"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.RenameProvider(id, in.NewID)
	}))
	sv.mux.HandleFunc("POST /api/provider/fetch-models", sv.handleProviderFetchModels)
	sv.mux.HandleFunc("POST /api/provider/reorder", sv.mutate(func(body []byte) (config.Result, error) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.ReorderProviders(in.IDs)
	}))

	sv.mux.HandleFunc("POST /api/model/add", sv.mutate(func(body []byte) (config.Result, error) {
		var in struct {
			ProviderID string            `json:"providerId"`
			Model      config.ModelInput `json:"model"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.AddModel(in.ProviderID, in.Model)
	}))
	sv.mux.HandleFunc("POST /api/model/update", sv.mutate(func(body []byte) (config.Result, error) {
		var in struct {
			ProviderID string             `json:"providerId"`
			ID         string             `json:"id"`
			Update     config.ModelUpdate `json:"update"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.UpdateModel(in.ProviderID, in.ID, in.Update)
	}))
	sv.mux.HandleFunc("POST /api/model/delete", sv.mutate(func(body []byte) (config.Result, error) {
		var in struct {
			ProviderID string `json:"providerId"`
			ID         string `json:"id"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.DeleteModel(in.ProviderID, in.ID)
	}))
	sv.mux.HandleFunc("POST /api/model/rename-id", sv.mutate(func(body []byte) (config.Result, error) {
		var in struct {
			ProviderID string `json:"providerId"`
			ID         string `json:"id"`
			NewID      string `json:"newId"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.RenameModel(in.ProviderID, in.ID, in.NewID)
	}))

	sv.mux.HandleFunc("POST /api/snapshot/save", sv.handleSnapshotSave)
	sv.mux.HandleFunc("POST /api/snapshot/restore", sv.handleSnapshotRestore)
	sv.mux.HandleFunc("POST /api/snapshot/delete", sv.handleSnapshotDelete)

	sv.mux.HandleFunc("POST /api/model/reorder", sv.mutate(func(body []byte) (config.Result, error) {
		var in struct {
			ProviderID string   `json:"providerId"`
			IDs        []string `json:"ids"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return config.Result{}, err
		}
		return sv.store.ReorderModels(in.ProviderID, in.IDs)
	}))

	sv.mux.HandleFunc("POST /api/template/save", sv.handleTemplateSave)
	sv.mux.HandleFunc("POST /api/template/delete", sv.handleTemplateDelete)

	sv.mux.HandleFunc("POST /api/model/autoreason", sv.handleAutoReason)
	sv.mux.HandleFunc("POST /api/provider/autoreason-all", sv.handleAutoReasonAll)
	sv.mux.HandleFunc("POST /api/catalog/rescan", sv.handleCatalogRescan)
	sv.mux.HandleFunc("POST /api/heartbeat", sv.handleHeartbeat)
	sv.mux.HandleFunc("POST /api/settings/update", sv.handleSettingsUpdate)
	sv.mux.HandleFunc("POST /api/quit", sv.handleQuit)
}

// ---- 通用响应 ----

type apiState struct {
	config.State
	Snapshots      []snapshot.Info   `json:"snapshots"`
	Templates      []config.Template `json:"templates"`
	CatalogReady   bool              `json:"catalogReady"`
	CatalogCount   int               `json:"catalogCount"`
	CatalogSources []string          `json:"catalogSources"`
	CatalogFromCLI bool              `json:"catalogFromCLI"`
	Settings       config.Settings   `json:"settings"`
}

type envelope struct {
	OK       bool        `json:"ok"`
	Error    string      `json:"error,omitempty"`
	Conflict bool        `json:"conflict,omitempty"`
	State    *apiState   `json:"state,omitempty"`
	Warnings []string    `json:"warnings,omitempty"`
	Extra    interface{} `json:"extra,omitempty"`
}

func (sv *Server) buildState() *apiState {
	st := sv.store.State()
	snaps, err := sv.store.SnapshotList()
	if err != nil {
		snaps = []snapshot.Info{}
	}
	return &apiState{
		State:          st,
		Snapshots:      snaps,
		Templates:      sv.store.Templates(),
		CatalogReady:   sv.catalog() != nil && sv.catalog().Size() > 0,
		CatalogCount:   sv.catalog().Size(),
		CatalogSources: sv.catPaths,
		CatalogFromCLI: sv.catFromCLI,
		Settings:       sv.store.Settings(),
	}
}

func (sv *Server) wrapState(st config.State) *apiState {
	return &apiState{
		State:          st,
		Snapshots:      sv.snapshots(),
		Templates:      sv.store.Templates(),
		CatalogReady:   sv.catalog() != nil && sv.catalog().Size() > 0,
		CatalogCount:   sv.catalog().Size(),
		CatalogSources: sv.catPaths,
		CatalogFromCLI: sv.catFromCLI,
		Settings:       sv.store.Settings(),
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: "读取请求体失败: " + err.Error()})
		return nil, false
	}
	return body, true
}

// mutate 处理"修改后返回完整状态"的端点。
func (sv *Server) mutate(fn func(body []byte) (config.Result, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := readBody(w, r)
		if !ok {
			return
		}
		res, err := fn(body)
		if err != nil {
			code := 400
			env := envelope{OK: false, Error: err.Error()}
			if errors.Is(err, config.ErrConflict) {
				code = 409
				env.Conflict = true
			}
			writeJSON(w, code, env)
			return
		}
		st := res.State
		writeJSON(w, 200, envelope{OK: true, State: sv.wrapState(st), Warnings: res.Warnings})
	}
}

// mutateID 处理以 {id: ...} 定位目标的端点。
func (sv *Server) mutateID(fn func(id string, body []byte) (config.Result, error)) http.HandlerFunc {
	return sv.mutate(func(body []byte) (config.Result, error) {
		var probe struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			return config.Result{}, err
		}
		if probe.ID == "" {
			return config.Result{}, fmt.Errorf("缺少 id")
		}
		return fn(probe.ID, body)
	})
}

func (sv *Server) snapshots() []snapshot.Info {
	snaps, err := sv.store.SnapshotList()
	if err != nil {
		return []snapshot.Info{}
	}
	return snaps
}

// ---- 状态与原始内容 ----

func (sv *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState()})
}

func (sv *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	text, err := sv.store.RawJSON()
	if err != nil {
		writeJSON(w, 500, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, Extra: map[string]string{"text": text}})
}

// ---- 保存 / 重载 ----

func (sv *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Force bool `json:"force"`
	}
	_ = json.Unmarshal(body, &in)

	info, st, err := sv.store.Save(in.Force)
	env := envelope{OK: true, Extra: info, State: sv.wrapState(st)}
	if err != nil {
		if errors.Is(err, config.ErrConflict) {
			writeJSON(w, 409, envelope{OK: false, Conflict: true, Error: err.Error(), State: sv.wrapState(st)})
			return
		}
		writeJSON(w, 500, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, env)
}

func (sv *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := sv.store.Reload(); err != nil {
		writeJSON(w, 500, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState()})
}

// ---- 快照 ----

func (sv *Server) handleSnapshotSave(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Name           string `json:"name"`
		AllowOverwrite bool   `json:"allowOverwrite"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	clean, err := sv.store.SnapshotSave(in.Name, in.AllowOverwrite)
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState(), Extra: map[string]string{"name": clean}})
}

func (sv *Server) handleSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	backupPath, err := sv.store.SnapshotRestore(strings.TrimSpace(in.Name))
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState(), Extra: map[string]string{"backupPath": backupPath}})
}

func (sv *Server) handleSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	if err := sv.store.SnapshotDelete(in.Name); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState()})
}

// ---- 思考档位模板 ----

func (sv *Server) handleTemplateSave(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Name           string   `json:"name"`
		Variants       []string `json:"variants"`
		DefaultVariant string   `json:"defaultVariant"`
		AllowOverwrite bool     `json:"allowOverwrite"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	warnings, err := sv.store.SaveTemplate(config.Template{
		Name:     in.Name,
		Variants: in.Variants,
		Default:  in.DefaultVariant,
	}, in.AllowOverwrite)
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState(), Warnings: warnings})
}

func (sv *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	if err := sv.store.DeleteTemplate(in.Name); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState()})
}

// ---- 官方目录自动匹配 ----

// matchOne 匹配单个模型并应用档位；返回 (来源描述, 匹配方式, 是否匹配)。
func (sv *Server) matchOne(providerID, modelID string) (source, matchLabel string, matched bool, err error) {
	cat := sv.catalog()
	if cat == nil || cat.Size() == 0 {
		return "", "", false, fmt.Errorf("未加载官方模型目录，请用 --catalog 指定 models_catalog*.json 的路径")
	}
	e, _, label := cat.MatchLabel(modelID)
	if e == nil {
		return "", "", false, nil
	}
	// 保留该模型现有的思考开关状态
	enabled := true
	st := sv.store.State()
	for _, p := range st.Providers {
		if p.ID != providerID {
			continue
		}
		for _, m := range p.Models {
			if m.ID == modelID {
				if m.HasReasoning {
					enabled = m.Enabled
				}
			}
		}
	}
	// 目录若声明了上下文/输出，一并写入 limit（保留模型已有的更大值语义由用户手动调）
	update := config.ModelUpdate{
		Reasoning: &config.ReasoningInput{
			Enabled:        enabled,
			Variants:       e.Variants,
			DefaultVariant: e.Default,
		},
	}
	if e.Context > 0 || e.Output > 0 {
		st0 := sv.store.State()
		var curCtx, curOut int64
		for _, pp := range st0.Providers {
			if pp.ID != providerID {
				continue
			}
			for _, mm := range pp.Models {
				if mm.ID == modelID {
					curCtx, curOut = mm.Context, mm.Output
				}
			}
		}
		newCtx, newOut := curCtx, curOut
		if e.Context > curCtx {
			newCtx = e.Context
		}
		if e.Output > curOut {
			newOut = e.Output
		}
		if newCtx > 0 || newOut > 0 {
			update.Limit = &config.LimitInput{Context: newCtx, Output: newOut}
		}
	}
	if _, err := sv.store.UpdateModel(providerID, modelID, update); err != nil {
		return "", "", false, err
	}
	source = "zcode 官方规则"
	return source, label, true, nil
}

func (sv *Server) handleAutoReason(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		ProviderID string `json:"providerId"`
		ID         string `json:"id"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	source, label, matched, err := sv.matchOne(in.ProviderID, in.ID)
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	// 无论是否命中都返回完整 state：前端 apply() 依赖它，缺失会导致界面“冻结”
	extra := map[string]any{"matched": matched, "source": source, "label": label}
	writeJSON(w, 200, envelope{OK: true, State: sv.wrapState(sv.store.State()), Extra: extra})
}

func (sv *Server) handleAutoReasonAll(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		ProviderID string `json:"providerId"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	if sv.catalog() == nil || sv.catalog().Size() == 0 {
		writeJSON(w, 400, envelope{OK: false, Error: "未加载官方模型目录，请用 --catalog 指定 models_catalog*.json 的路径"})
		return
	}
	st := sv.store.State()
	var modelIDs []string
	for _, p := range st.Providers {
		if p.ID == in.ProviderID {
			for _, m := range p.Models {
				modelIDs = append(modelIDs, m.ID)
			}
		}
	}
	matched, hinted := 0, 0
	for _, id := range modelIDs {
		_, label, m, err := sv.matchOne(in.ProviderID, id)
		if err != nil {
			writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
			return
		}
		if m {
			matched++
			if label != "精确" {
				hinted++
			}
		}
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.wrapState(sv.store.State()), Extra: map[string]any{
		"matched": matched, "total": len(modelIDs), "hinted": hinted,
	}})
}

func (sv *Server) handleCatalogRescan(w http.ResponseWriter, r *http.Request) {
	n, err := sv.RescanCatalog()
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState(), Extra: map[string]any{"count": n}})
}

// ---- 页面心跳 / 退出 / 设置 ----

func (sv *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	sv.hbMu.Lock()
	sv.lastHb = time.Now().Unix()
	sv.hbEverSeen = true
	sv.hbMu.Unlock()
	writeJSON(w, 200, envelope{OK: true})
}

func (sv *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, envelope{OK: true})
	if sv.quit != nil {
		go func() {
			time.Sleep(300 * time.Millisecond) // 让响应先送达
			log.Println("收到 Web 退出请求")
			sv.quit()
		}()
	}
}

func (sv *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		AutoQuitOnPageClose *bool     `json:"autoQuitOnPageClose"`
		CatalogPaths        *[]string `json:"catalogPaths"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}

	cur := sv.store.Settings()
	changed := config.Settings{AutoQuitOnPageClose: cur.AutoQuitOnPageClose, CatalogPaths: cur.CatalogPaths}
	var warnings []string
	if in.AutoQuitOnPageClose != nil {
		changed.AutoQuitOnPageClose = *in.AutoQuitOnPageClose
	}
	if in.CatalogPaths != nil {
		if sv.catFromCLI {
			writeJSON(w, 400, envelope{OK: false, Error: "目录来源当前由 --catalog 启动参数锁定；请去掉该参数重启后再在设置中修改"})
			return
		}
		changed.CatalogPaths = *in.CatalogPaths
	}
	if err := sv.store.SaveSettings(changed); err != nil {
		writeJSON(w, 500, envelope{OK: false, Error: "保存设置失败: " + err.Error()})
		return
	}

	// 目录路径变化 → 用新路径重扫
	if in.CatalogPaths != nil && !sv.catFromCLI {
		sv.catMu.Lock()
		sv.catPaths = append([]string{}, changed.CatalogPaths...)
		sv.catMu.Unlock()
		if n, err := sv.RescanCatalog(); err != nil {
			warnings = append(warnings, "目录重扫失败: "+err.Error())
		} else {
			warnings = append(warnings, fmt.Sprintf("官方目录已按新路径重扫：%d 个带档位的模型", n))
		}
	}
	writeJSON(w, 200, envelope{OK: true, State: sv.buildState(), Warnings: warnings})
}

// ---- 从提供商 API 拉取可用模型 ----

// fetchModelItem 是拉取列表中的一行（已按官方目录补全配置）。
type fetchModelItem struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Context   int64    `json:"context,omitempty"`
	Output    int64    `json:"output,omitempty"`
	InputMod  []string `json:"inputModalities,omitempty"`
	OutputMod []string `json:"outputModalities,omitempty"`
	Catalog   string   `json:"catalogMatch,omitempty"`
}

// handleProviderFetchModels 调用提供商的 /models 接口列出可用模型，
// 并用官方模型目录自动补全显示名、上下文窗口、最大输出与模态。
func (sv *Server) handleProviderFetchModels(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var in struct {
		ProviderID string `json:"providerId"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: err.Error()})
		return
	}

	st := sv.store.State()
	var baseURL, apiKey, kind string
	found := false
	for _, p := range st.Providers {
		if p.ID == in.ProviderID {
			baseURL, apiKey, kind, found = p.BaseURL, p.APIKey, p.Kind, true
			break
		}
	}
	if !found {
		writeJSON(w, 400, envelope{OK: false, Error: "提供商不存在"})
		return
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		writeJSON(w, 400, envelope{OK: false, Error: "该提供商未配置 Base URL，无法拉取模型列表"})
		return
	}
	url := baseURL + "/models"
	if kind == "anthropic" {
		url = baseURL + "/v1/models"
	}

	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: "构造请求失败: " + err.Error()})
		return
	}
	if apiKey != "" {
		if kind == "anthropic" {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, 400, envelope{OK: false, Error: "请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		writeJSON(w, 400, envelope{OK: false, Error: fmt.Sprintf("接口返回 HTTP %d：%s", resp.StatusCode, snippet)})
		return
	}

	type item struct{ id, name string }
	var items []item
	var wrapper struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err == nil && len(wrapper.Data) > 0 {
		for _, d := range wrapper.Data {
			name := d.DisplayName
			if name == "" {
				name = d.Name
			}
			items = append(items, item{d.ID, name})
		}
	} else {
		var arr []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(respBody, &arr); err == nil {
			for _, d := range arr {
				items = append(items, item{d.ID, d.Name})
			}
		}
	}
	if len(items) == 0 {
		writeJSON(w, 400, envelope{OK: false, Error: "接口返回内容无法识别为模型列表（OpenAI / Anthropic 格式）"})
		return
	}

	out := make([]fetchModelItem, 0, len(items))
	for _, it := range items {
		m := fetchModelItem{ID: it.id, Name: it.name, InputMod: []string{"text"}, OutputMod: []string{"text"}}
		if e, _, _ := sv.catalog().MatchLabel(it.id); e != nil {
			m.Context, m.Output = e.Context, e.Output
			if len(e.InputMod) > 0 {
				m.InputMod = e.InputMod
			}
			if len(e.OutputMod) > 0 {
				m.OutputMod = e.OutputMod
			}
			m.Catalog = "zcode 官方规则"
		}
		out = append(out, m)
	}
	writeJSON(w, 200, envelope{OK: true, Extra: map[string]any{"models": out, "count": len(out)}})
}
