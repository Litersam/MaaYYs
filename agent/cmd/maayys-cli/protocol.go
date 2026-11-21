package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"maa-yys-agent/internal/redact"
	"maa-yys-agent/internal/runstate"
)

type RunRequest struct {
	AdbPath               string     `json:"adb_path"`
	AdbAddress            string     `json:"adb_address"`
	MumuIndex             *int       `json:"mumu_index,omitempty"`
	ResourcePaths         []string   `json:"resource_paths"`
	Steps                 []StepSpec `json:"steps"`
	FailureScreenshotPath string     `json:"failure_screenshot_path,omitempty"`
}

type StepSpec struct {
	ID               string         `json:"id"`
	Task             string         `json:"task"`
	Params           map[string]any `json:"params,omitempty"`
	PipelineOverride map[string]any `json:"pipeline_override,omitempty"`
}

type jsonlWriter struct {
	mu      sync.Mutex
	out     io.Writer
	secrets []string
}

func (w *jsonlWriter) addSecret(s string) {
	if s != "" {
		w.mu.Lock()
		w.secrets = append(w.secrets, s)
		w.mu.Unlock()
	}
}

func (w *jsonlWriter) redact(s string) string {
	w.mu.Lock()
	secrets := append([]string(nil), w.secrets...)
	w.mu.Unlock()
	return redact.Secrets(s, secrets...)
}

func (w *jsonlWriter) emit(v any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if data, err := json.Marshal(v); err == nil {
		_, _ = w.out.Write(append(data, '\n'))
		if f, ok := w.out.(*os.File); ok {
			_ = f.Sync()
		}
	}
}

func (w *jsonlWriter) Log(level, msg string) {
	w.emit(struct {
		Type    string `json:"type"`
		Time    string `json:"time"`
		Level   string `json:"level"`
		Message string `json:"message"`
	}{"log", time.Now().Format("15:04:05"), level, w.redact(msg)})
}

func (w *jsonlWriter) Step(id, status, detail string, metrics map[string]any) {
	w.emit(struct {
		Type    string         `json:"type"`
		ID      string         `json:"id"`
		Status  string         `json:"status"`
		Detail  string         `json:"detail"`
		Metrics map[string]any `json:"metrics,omitempty"`
	}{"step", id, status, w.redact(detail), w.safeMetrics(metrics)})
}

func (w *jsonlWriter) EndStep(id, detail string, outcome runstate.Outcome, err error) {
	status := "complete"
	switch {
	case err != nil:
		status, detail = "error", err.Error()
	case outcome.Status == runstate.Failed:
		status = "error"
	case outcome.Status == runstate.Skipped:
		status, detail = "skipped", "已跳过"
	case outcome.Status == runstate.Deferred:
		status, detail = "deferred", "待重试"
	}
	w.Step(id, status, detail, outcome.Metrics)
}

func (w *jsonlWriter) Result(outcome runstate.Outcome, displaced bool, errMsg string, screenshotSaved bool) {
	status := outcome.Status
	if status == "" {
		status = runstate.Completed
	}
	ok := status != runstate.Failed
	var errPtr *string
	if !ok && errMsg != "" {
		clean := w.redact(errMsg)
		errPtr = &clean
	}
	w.emit(struct {
		Type            string          `json:"type"`
		OK              bool            `json:"ok"`
		Status          runstate.Status `json:"status"`
		Displaced       bool            `json:"displaced"`
		Error           *string         `json:"error"`
		ScreenshotSaved bool            `json:"screenshot_saved"`
		Metrics         map[string]any  `json:"metrics"`
	}{"result", ok, status, displaced, errPtr, screenshotSaved, w.safeMetrics(outcome.Metrics)})
}

func (w *jsonlWriter) safeMetrics(input map[string]any) map[string]any {
	metrics := make(map[string]any, len(input))
	for key, value := range input {
		data, err := json.Marshal(value)
		if err != nil {
			continue
		}
		var normalized any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if dec.Decode(&normalized) == nil {
			metrics[w.redact(key)] = w.redactMetric(normalized)
		}
	}
	return metrics
}

func (w *jsonlWriter) redactMetric(value any) any {
	switch v := value.(type) {
	case string:
		return w.redact(v)
	case []any:
		for i, item := range v {
			v[i] = w.redactMetric(item)
		}
	case map[string]any:
		safe := make(map[string]any, len(v))
		for key, item := range v {
			safe[w.redact(key)] = w.redactMetric(item)
		}
		return safe
	}
	return value
}
