package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CatalogTask represents a single runnable task discovered in tasks/*.json.
type CatalogTask struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Entry   string   `json:"entry"`
	Group   []string `json:"group,omitempty"`
	Options []string `json:"options,omitempty"`
	File    string   `json:"file"`
}

// CatalogOutput is emitted by `maayys-cli catalog`.
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
	Type             string         `json:"type"`
	DefaultCase      string         `json:"default_case"`
	Cases            []rawCaseDef   `json:"cases"`
	Inputs           []rawInputDef  `json:"inputs"`
	PipelineOverride map[string]any `json:"pipeline_override"`
}

type rawCaseDef struct {
	Name             string         `json:"name"`
	PipelineOverride map[string]any `json:"pipeline_override"`
}

type rawInputDef struct {
	Name    string `json:"name"`
	Default string `json:"default"`
}

type resolvedTask struct {
	CatalogTask
	BaseOverride map[string]any
	OptionsMap   map[string]rawOptionDef
}

func loadTaskDefinitions(repoRoot string) ([]resolvedTask, map[string]resolvedTask, error) {
	tasksDir := filepath.Join(repoRoot, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read tasks directory %s: %w", tasksDir, err)
	}

	var list []resolvedTask
	byName := make(map[string]resolvedTask)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		fullPath := filepath.Join(tasksDir, entry.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", fullPath, err)
		}
		var parsed taskFileSchema
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", fullPath, err)
		}
		relFile := filepath.ToSlash(filepath.Join("tasks", entry.Name()))
		for _, t := range parsed.Task {
			if strings.TrimSpace(t.Name) == "" || strings.TrimSpace(t.Entry) == "" {
				continue
			}
			rt := resolvedTask{
				CatalogTask: CatalogTask{
					Name:    t.Name,
					Label:   t.Label,
					Entry:   t.Entry,
					Group:   t.Group,
					Options: t.Option,
					File:    relFile,
				},
				BaseOverride: buildDefaultTaskOverride(t, parsed.Option),
				OptionsMap:   parsed.Option,
			}
			list = append(list, rt)
			byName[t.Name] = rt
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list, byName, nil
}

func buildDefaultTaskOverride(t rawTaskDef, opts map[string]rawOptionDef) map[string]any {
	merged := make(map[string]any)
	for _, optName := range t.Option {
		opt, ok := opts[optName]
		if !ok {
			continue
		}
		switch strings.ToLower(opt.Type) {
		case "switch", "select":
			if opt.DefaultCase == "" {
				continue
			}
			for _, c := range opt.Cases {
				if c.Name == opt.DefaultCase && c.PipelineOverride != nil {
					mergeMap(merged, c.PipelineOverride)
					break
				}
			}
		case "input":
			if opt.PipelineOverride == nil || t.Name == "账号登录" {
				continue
			}
			vals := make(map[string]string, len(opt.Inputs))
			for _, in := range opt.Inputs {
				vals[in.Name] = in.Default
			}
			if expanded, ok := substitutePlaceholders(opt.PipelineOverride, vals).(map[string]any); ok {
				mergeMap(merged, expanded)
			}
		}
	}
	if t.PipelineOverride != nil {
		mergeMap(merged, t.PipelineOverride)
	}
	return merged
}

func applyOptionParams(dst map[string]any, task resolvedTask, params map[string]any) {
	if len(params) == 0 || task.Name == "账号登录" {
		return
	}
	for _, optName := range task.Options {
		opt, ok := task.OptionsMap[optName]
		if !ok {
			continue
		}
		if strings.EqualFold(opt.Type, "input") && opt.PipelineOverride != nil {
			vals := make(map[string]string, len(opt.Inputs))
			touched := false
			for _, in := range opt.Inputs {
				vals[in.Name] = in.Default
				if v, ok := params[in.Name]; ok {
					vals[in.Name] = fmt.Sprint(v)
					touched = true
				}
			}
			if touched {
				if expanded, ok := substitutePlaceholders(opt.PipelineOverride, vals).(map[string]any); ok {
					mergeMap(dst, expanded)
				}
			}
			continue
		}
		rawVal, provided := params[optName]
		if !provided {
			continue
		}
		strVal := fmt.Sprint(rawVal)
		for _, c := range opt.Cases {
			if strings.EqualFold(c.Name, strVal) && c.PipelineOverride != nil {
				mergeMap(dst, c.PipelineOverride)
				break
			}
		}
	}
}

func substitutePlaceholders(v any, vals map[string]string) any {
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
		res := node
		for k, val := range vals {
			res = strings.ReplaceAll(res, "{"+k+"}", val)
		}
		return res
	default:
		return v
	}
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
