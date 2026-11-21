package bonus_toggle

import (
	"errors"
	"sync"
	"sync/atomic"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const ownedAnchor = "通用加成-本轮已开启-"

func OwnsAny(ctx *maa.Context) bool {
	o := ownership{anchors: ctx}
	for name := range labels {
		if o.contains(name) {
			return true
		}
	}
	return false
}

type taskAnchors interface {
	GetAnchor(string) (string, error)
	SetAnchor(string, string) error
}

type ownership struct {
	anchors taskAnchors
	scope   *Scope
	cleanup bool
}

func ownershipFor(ctx *maa.Context, node string) ownership {
	value, _ := scopes.Load(*ctx.GetTasker())
	scope, _ := value.(*Scope)
	return ownership{ctx, scope, scope != nil && node == cleanupNode && scope.cleaning.Load()}
}

func (o ownership) contains(name string) bool {
	if o.cleanup {
		return o.scope.contains(name)
	}
	// Native anchors belong to the current task context, including in MXU.
	value, err := o.anchors.GetAnchor(ownedAnchor + name)
	return err == nil && value == activeNode
}

func (o ownership) set(name string, owned bool) error {
	if o.scope != nil {
		o.scope.set(name, owned)
	}
	if o.cleanup {
		return nil
	}
	marker := inactiveNode
	if owned {
		marker = activeNode
	}
	return o.anchors.SetAnchor(ownedAnchor+name, marker)
}

// The CLI mirrors ownership only to carry it into a separate, bounded
// cleanup job after the original task has stopped. MXU needs no global map.
var scopes sync.Map // map[maa.Tasker]*Scope

type Scope struct {
	mu       sync.Mutex
	owned    map[string]bool
	cleaning atomic.Bool
}

func Track(tasker *maa.Tasker) (*Scope, func()) {
	scope := &Scope{owned: make(map[string]bool)}
	scopes.Store(*tasker, scope)
	return scope, func() { scopes.CompareAndDelete(*tasker, scope) }
}

func (s *Scope) set(name string, owned bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owned {
		s.owned[name] = true
	} else {
		delete(s.owned, name)
	}
}

func (s *Scope) contains(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owned[name]
}

func (s *Scope) clearParams() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	params := make(map[string]bool, len(s.owned))
	for k := range s.owned {
		params[k] = false
	}
	return params
}

func (s *Scope) Cleanup(tasker *maa.Tasker, run func(*maa.Tasker, string, map[string]any) bool) error {
	params := s.clearParams()
	if len(params) == 0 {
		return nil
	}
	s.cleaning.Store(true)
	defer s.cleaning.Store(false)
	if !run(tasker, cleanupEntry, map[string]any{
		cleanupNode: map[string]any{"custom_action_param": params},
	}) || len(s.clearParams()) != 0 {
		return errors.New("无法确认本轮开启的加成已关闭")
	}
	return nil
}
