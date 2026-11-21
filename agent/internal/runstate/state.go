// Package runstate collects reports from Maa custom runners during one CLI step.
package runstate

import (
	"encoding/json"
	"maps"
	"sync"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type Status string

const (
	Completed Status = "completed"
	Skipped   Status = "skipped"
	Deferred  Status = "deferred"
	Failed    Status = "failed"
)

type Outcome struct {
	Status   Status
	Metrics  map[string]any
	Reported bool
}

type Collector struct {
	mu      sync.Mutex
	status  Status
	metrics map[string]json.RawMessage
}

// Maa returns a fresh Go wrapper for the same native Tasker on each callback.
// Its value, unlike the wrapper pointer, identifies the bound executor.
var bindings sync.Map // map[maa.Tasker]*Collector

// Bind collects reports until release is called, after the posted job has finished.
func Bind(tasker *maa.Tasker) (*Collector, func()) {
	c := new(Collector)
	bindings.Store(*tasker, c)
	return c, func() { bindings.CompareAndDelete(*tasker, c) }
}

func Report(ctx *maa.Context, status Status) {
	if c := current(ctx); c != nil {
		c.Record(status)
	}
}

func SetMetric(ctx *maa.Context, key string, value any) {
	if c := current(ctx); c != nil {
		c.Metric(key, value)
	}
}

func current(ctx *maa.Context) *Collector {
	if ctx != nil {
		if c, ok := bindings.Load(*ctx.GetTasker()); ok {
			return c.(*Collector)
		}
	}
	return nil
}

func priority(status Status) int {
	switch status {
	case Failed:
		return 4
	case Deferred:
		return 3
	case Completed:
		return 2
	case Skipped:
		return 1
	default:
		return 0
	}
}

func (c *Collector) Record(status Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if priority(status) > priority(c.status) {
		c.status = status
	}
}

// Metric snapshots JSON values at the call boundary so nested maps and slices
// cannot be mutated by the caller after reporting. Invalid values are rejected.
func (c *Collector) Metric(key string, value any) bool {
	data, err := json.Marshal(value)
	if key == "" || err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.metrics == nil {
		c.metrics = make(map[string]json.RawMessage)
	}
	c.metrics[key] = data
	return true
}

func (c *Collector) Outcome() Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := c.status
	if status == "" {
		status = Completed
	}
	// Clone each JSON buffer before exposing it to external callers.
	metrics := make(map[string]any, len(c.metrics))
	for k, v := range c.metrics {
		metrics[k] = append(json.RawMessage(nil), v...)
	}
	return Outcome{Status: status, Metrics: metrics, Reported: c.status != ""}
}

// Merge combines only the steps relevant to the plan's outcome. Later metric
// observations replace earlier observations with the same name.
func (o *Outcome) Merge(step Outcome) {
	if priority(step.Status) > priority(o.Status) {
		o.Status = step.Status
	}
	if o.Metrics == nil {
		o.Metrics = make(map[string]any)
	}
	maps.Copy(o.Metrics, step.Metrics)
}
