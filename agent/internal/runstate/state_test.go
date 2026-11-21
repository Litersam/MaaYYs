package runstate

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentCollectorAndPrecedence(t *testing.T) {
	var c Collector
	if c.Outcome().Status != Completed {
		t.Fatal("legacy task without reports should complete")
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Record(Skipped)
			c.Metric(fmt.Sprint(i), i)
		}()
	}
	wg.Wait()
	if got := c.Outcome(); got.Status != Skipped || len(got.Metrics) != 16 {
		t.Fatalf("lost concurrent reports: %+v", got)
	}
	for _, report := range []Status{Completed, Deferred, Failed, Completed, "invalid"} {
		c.Record(report)
	}
	if c.Outcome().Status != Failed {
		t.Fatal("later success masked a failure")
	}
}

func TestMetricIsDetachedAndJSONSafe(t *testing.T) {
	var c Collector
	input := []int{42}
	if !c.Metric("timer", input) || c.Metric("bad", func() {}) {
		t.Fatal("collector must only accept JSON values")
	}
	input[0] = 8
	snapshot := c.Outcome()
	snapshot.Metrics["timer"].(json.RawMessage)[1] = '0'
	if got := string(c.Outcome().Metrics["timer"].(json.RawMessage)); got != "[42]" {
		t.Fatalf("stored metric changed to %s", got)
	}
}
