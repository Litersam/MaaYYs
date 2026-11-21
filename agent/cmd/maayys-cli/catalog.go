package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	TaskCategoryDaily      = "daily"
	TaskCategoryStandalone = "standalone"
	TaskCategoryPromotion  = "promotion"
)

var knownTaskCategories = map[string]int{
	TaskCategoryDaily:      0,
	TaskCategoryStandalone: 1,
	TaskCategoryPromotion:  2,
}

// CatalogTask represents a single runnable task discovered under tasks/{daily,standalone,promotion}.
type CatalogTask struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Entry    string   `json:"entry"`
	Category string   `json:"category"`
	Group    []string `json:"group,omitempty"`
	Options  []string `json:"options,omitempty"`
	File     string   `json:"file"`
}

// CatalogOutput is emitted by `maayys-cli catalog` (or `--catalog`).
type CatalogOutput struct {
	OK    bool          `json:"ok"`
	Tasks []CatalogTask `json:"tasks"`
}

type taskFileSchema struct {
	Task   []rawTaskDef            `json:"task"`
	Option map[string]rawOptionDef `json:"option"`
}

type rawTaskDef struct {
	Name             string         `json:"name"`
	Label            string         `json:"label"`
	Entry            string         `json:"entry"`
	Group            []string       `json:"group"`
	Option           []string       `json:"option"`
	PipelineOverride map[string]any `json:"pipeline_override"`
}

type rawOptionDef struct {
	Type             string          `json:"type"`
	DefaultCase      json.RawMessage `json:"default_case"`
	DefaultSelection json.RawMessage `json:"default"`
	Cases            []rawCaseDef    `json:"cases"`
	Inputs           []rawInputDef   `json:"inputs"`
	Option           []string        `json:"option"`
	SubOptions       []string        `json:"sub_options"`
	PipelineOverride map[string]any  `json:"pipeline_override"`
}

type rawCaseDef struct {
	Name             string         `json:"name"`
	Option           []string       `json:"option"`
	SubOptions       []string       `json:"sub_options"`
	PipelineOverride map[string]any `json:"pipeline_override"`
}

type rawInputDef struct {
	Name         string `json:"name"`
	Default      string `json:"default"`
	PipelineType string `json:"pipeline_type"`
}

type typedInputValue struct {
	raw          string
	pipelineType string
}

type resolvedTask struct {
	CatalogTask
	RawTask      rawTaskDef
	BaseOverride map[string]any
	OptionsMap   map[string]rawOptionDef
}

func loadTaskDefinitions(repoRoot string) ([]resolvedTask, map[string]resolvedTask, error) {
	tasksDir := filepath.Join(repoRoot, "tasks")
	if _, err := os.Stat(tasksDir); err != nil {
		return nil, nil, fmt.Errorf("stat tasks directory %s: %w", tasksDir, err)
	}

	var list []resolvedTask
	byName := make(map[string]resolvedTask)

	err := filepath.WalkDir(tasksDir, func(fullPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", fullPath, err)
		}
		var parsed taskFileSchema
		if err := json.Unmarshal(data, &parsed); err != nil {
			return fmt.Errorf("parse %s: %w", fullPath, err)
		}

		relFromTasks, err := filepath.Rel(tasksDir, fullPath)
		if err != nil {
			return fmt.Errorf("rel path %s: %w", fullPath, err)
		}
		relSlash := filepath.ToSlash(relFromTasks)
		relFile := filepath.ToSlash(filepath.Join("tasks", relFromTasks))

		for _, t := range parsed.Task {
			if strings.TrimSpace(t.Name) == "" || strings.TrimSpace(t.Entry) == "" {
				continue
			}
			cat := inferTaskCategory(relSlash, t.Group)
			rt := resolvedTask{
				CatalogTask: CatalogTask{
					Name:     t.Name,
					Label:    t.Label,
					Entry:    t.Entry,
					Category: cat,
					Group:    t.Group,
					Options:  catalogOptionIDs(t.Name, collectReachableOptions(t.Option, parsed.Option)),
					File:     relFile,
				},
				RawTask:    t,
				OptionsMap: parsed.Option,
			}
			rt.BaseOverride = resolveTaskOverride(rt, nil)
			list = append(list, rt)
			byName[t.Name] = rt
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sort.Slice(list, func(i, j int) bool {
		oi, oki := knownTaskCategories[list[i].Category]
		oj, okj := knownTaskCategories[list[j].Category]
		if oki && okj && oi != oj {
			return oi < oj
		}
		if list[i].Category != list[j].Category {
			return list[i].Category < list[j].Category
		}
		return list[i].Name < list[j].Name
	})
	return list, byName, nil
}

func inferTaskCategory(relSlash string, groups []string) string {
	if idx := strings.IndexByte(relSlash, '/'); idx > 0 {
		dir := strings.ToLower(strings.TrimSpace(relSlash[:idx]))
		if _, ok := knownTaskCategories[dir]; ok {
			return dir
		}
	}
	for _, g := range groups {
		norm := strings.ToLower(strings.TrimSpace(g))
		if _, ok := knownTaskCategories[norm]; ok {
			return norm
		}
	}
	return TaskCategoryStandalone
}

func lookupTask(tasks map[string]resolvedTask, query string) (resolvedTask, bool) {
	query = strings.TrimSpace(query)
	if def, ok := tasks[query]; ok {
		return def, true
	}
	if slash := strings.IndexByte(query, '/'); slash > 0 && slash+1 < len(query) {
		cat := strings.ToLower(strings.TrimSpace(query[:slash]))
		name := strings.TrimSpace(query[slash+1:])
		if def, ok := tasks[name]; ok && strings.EqualFold(def.Category, cat) {
			return def, true
		}
	}
	return resolvedTask{}, false
}

func collectReachableOptions(rootOpts []string, opts map[string]rawOptionDef) []string {
	if len(rootOpts) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]bool, len(rootOpts))
	var visit func(names []string)
	visit = func(names []string) {
		for _, name := range names {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
			opt, ok := opts[name]
			if !ok {
				continue
			}
			visit(opt.Option)
			visit(opt.SubOptions)
			for _, c := range opt.Cases {
				visit(c.Option)
				visit(c.SubOptions)
			}
		}
	}
	visit(rootOpts)
	return out
}

// resolveTaskOverride evaluates a task's conditional ProjectInterface v2 option tree
// (including multi-select cases, nested options and typed input placeholders).
func resolveTaskOverride(task resolvedTask, params map[string]any) map[string]any {
	params = withLegacyDokanPreset(task.Name, params)
	merged := make(map[string]any)
	visited := make(map[string]bool)

	var evalList func(optNames []string)
	evalList = func(optNames []string) {
		for _, optName := range optNames {
			if optName == "" || visited[optName] {
				continue
			}
			visited[optName] = true
			opt, ok := task.OptionsMap[optName]
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(opt.Type)) {
			case "switch", "select":
				active, found := selectOptionCase(opt, publicOptionID(task.Name, optName), params)
				if found {
					if active.PipelineOverride != nil {
						mergeMap(merged, active.PipelineOverride)
					}

					evalList(opt.Option)
					evalList(opt.SubOptions)
					evalList(active.Option)
					evalList(active.SubOptions)
				}
			case "checkbox":
				for _, active := range selectedCheckboxCases(opt, params[optName]) {
					mergeMap(merged, active.PipelineOverride)
					evalList(opt.Option)
					evalList(opt.SubOptions)
					evalList(active.Option)
					evalList(active.SubOptions)
				}
			case "input":
				if opt.PipelineOverride != nil && task.Name != "账号登录" {
					vals := resolveInputValues(opt, optName, params)
					if task.Name == "契灵结契" && optName == "契灵结契-设置挑战次数" {
						resolveBondlingLimit(vals, params, optName)
					}
					if expanded, ok := substitutePlaceholders(opt.PipelineOverride, vals).(map[string]any); ok {
						mergeMap(merged, expanded)
					}
				}
				evalList(opt.Option)
				evalList(opt.SubOptions)
			default:
				if opt.PipelineOverride != nil {
					mergeMap(merged, opt.PipelineOverride)
				}
				evalList(opt.Option)
				evalList(opt.SubOptions)
			}
		}
	}

	evalList(task.RawTask.Option)
	if task.RawTask.PipelineOverride != nil {
		mergeMap(merged, task.RawTask.PipelineOverride)
	}
	return merged
}

func resolveBondlingLimit(vals map[string]typedInputValue, params map[string]any, optName string) {
	const legacy = "挑战次数"
	nested, _ := params[optName].(map[string]any)
	limit, valid, provided := 0, false, false
	for _, input := range []struct {
		source map[string]any
		key    string
	}{{params, "limit_count"}, {params, legacy}, {nested, legacy}} {
		raw, exists := input.source[input.key]
		if !exists {
			continue
		}
		provided = true
		text := strings.TrimSpace(fmt.Sprint(raw))
		if number, ok := raw.(float64); ok {
			text = strconv.FormatFloat(number, 'f', -1, 64)
		}
		n, err := strconv.Atoi(text)
		if err == nil && n >= 0 && (!valid || n < limit) {
			limit, valid = n, true
		}
	}
	if provided {
		// Ignore malformed siblings of a valid limit; never fall back to the
		// default when the caller supplied only invalid limits.
		if !valid {
			limit = -1
		}
		vals[legacy] = typedInputValue{strconv.Itoa(limit), "int"}
	}
}

// PI v2 imports merge options in a shared namespace. Keep distinct MXU IDs
// for options whose choices differ by task, without changing the CLI contract.
func publicOptionID(task, id string) string {
	if task == "组队刷本" || task == "觉醒" || task == "同心之兰" {
		switch id {
		case task + "-party_role":
			return "party_role"
		case task + "-dungeon_layer":
			return "dungeon_layer"
		}
	}
	return id
}

func catalogOptionIDs(task string, ids []string) []string {
	for i, id := range ids {
		ids[i] = publicOptionID(task, id)
	}
	return ids
}

func withLegacyDokanPreset(task string, params map[string]any) map[string]any {
	const toggle = "道馆馆主战换御魂"
	if task != "自动道馆突破" {
		return params
	}
	if _, provided := params[toggle]; provided {
		return params
	}
	legacy, _ := params["道馆阵容预设"].(map[string]any)
	for _, key := range []string{"group_name", "team_name"} {
		raw, flat := params[key]
		if !flat || raw == nil {
			raw = legacy[key]
		}
		if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
			compat := maps.Clone(params)
			compat[toggle] = "Yes"
			return compat
		}
	}
	return params
}

func selectedCheckboxCases(opt rawOptionDef, selected any) []rawCaseDef {
	var names []string
	switch values := selected.(type) {
	case nil:
		defaults := opt.DefaultCase
		if len(defaults) == 0 || string(defaults) == "null" {
			defaults = opt.DefaultSelection
		}
		if len(defaults) != 0 && json.Unmarshal(defaults, &names) != nil {
			return nil
		}
	case []string:
		names = values
	case []any:
		for _, item := range values {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
	}
	selectedNames := make(map[string]bool, len(names))
	for _, name := range names {
		selectedNames[strings.ToLower(name)] = true
	}
	var cases []rawCaseDef
	for _, c := range opt.Cases {
		if selectedNames[strings.ToLower(c.Name)] {
			cases = append(cases, c)
		}
	}
	return cases
}

func selectOptionCase(opt rawOptionDef, optName string, params map[string]any) (rawCaseDef, bool) {
	var target string
	if len(opt.DefaultCase) > 0 && json.Unmarshal(opt.DefaultCase, &target) != nil {
		return rawCaseDef{}, false
	}
	var boolVal *bool
	if params != nil {
		if rawVal, provided := params[optName]; provided && rawVal != nil {
			if b, ok := rawVal.(bool); ok {
				boolVal = &b
				if b {
					target = "Yes"
				} else {
					target = "No"
				}
			} else {
				target = fmt.Sprint(rawVal)
			}
		}
	}
	if target == "" {
		return rawCaseDef{}, false
	}
	for _, c := range opt.Cases {
		if strings.EqualFold(c.Name, target) {
			return c, true
		}
	}
	if boolVal != nil {
		alt := strconv.FormatBool(*boolVal)
		for _, c := range opt.Cases {
			if strings.EqualFold(c.Name, alt) {
				return c, true
			}
		}
	}
	return rawCaseDef{}, false
}

func resolveInputValues(opt rawOptionDef, optName string, params map[string]any) map[string]typedInputValue {
	vals := make(map[string]typedInputValue, len(opt.Inputs))
	var nestedMap map[string]any
	if params != nil {
		if sub, ok := params[optName].(map[string]any); ok {
			nestedMap = sub
		}
	}
	for _, in := range opt.Inputs {
		raw := in.Default
		if nestedMap != nil {
			if v, ok := nestedMap[in.Name]; ok && v != nil {
				raw = fmt.Sprint(v)
			}
		}
		if params != nil {
			if v, ok := params[in.Name]; ok && v != nil {
				raw = fmt.Sprint(v)
			}
		}
		vals[in.Name] = typedInputValue{
			raw:          raw,
			pipelineType: strings.ToLower(strings.TrimSpace(in.PipelineType)),
		}
	}
	return vals
}

func substitutePlaceholders(v any, vals map[string]typedInputValue) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			out[k] = substitutePlaceholders(child, vals)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, child := range node {
			out[i] = substitutePlaceholders(child, vals)
		}
		return out
	case string:
		for k, iv := range vals {
			if node == "{"+k+"}" {
				return coerceInputPlaceholder(iv)
			}
		}
		res := node
		for k, iv := range vals {
			res = strings.ReplaceAll(res, "{"+k+"}", iv.raw)
		}
		return res
	default:
		return v
	}
}

func coerceInputPlaceholder(iv typedInputValue) any {
	trimmed := strings.TrimSpace(iv.raw)
	switch iv.pipelineType {
	case "int", "integer", "uint":
		if n, err := strconv.Atoi(trimmed); err == nil {
			return n
		}
	case "float", "number", "double":
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return f
		}
	case "bool", "boolean", "boole":
		if b, err := strconv.ParseBool(trimmed); err == nil {
			return b
		}
	}
	return iv.raw
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstMap, exists := dst[k].(map[string]any); exists {
				mergeMap(dstMap, srcMap)
				continue
			}
			copied := make(map[string]any, len(srcMap))
			mergeMap(copied, srcMap)
			dst[k] = copied
			continue
		}
		dst[k] = v
	}
}
