package kernel

import (
	"fmt"
	"os"
	"sync"
)

// bus —— 内核与外界的唯一接缝。
//
// 面向两类消费者，有两种用法：
//
//  1. 横切插件用两个回调：
//     - Fire：观察。可观测性、计量、审计挂在这里；观察者报错绝不影响主流程。
//     - Check：裁决。审批/权限/预算关卡挂在这里；任一否决即拦截。
//     默认 fail-closed：checker 自身报错计为否决，绝不视为放行。
//
//  2. 流式消费者用"订阅队列 + 背压"：Subscribe 返回有界队列。满时 "block"
//     让生产者等待（把压力推回模型流）；"drop" 永不阻塞，丢帧并计数。

// BlockingResult 是裁决关卡的裁定结果。
type BlockingResult struct {
	Allowed bool
	Reason  string
}

// Observer 观察事件；无返回值，不得让主流失败。
type Observer func(data map[string]any)

// Checker 裁决关卡；返回 error 或 !Allowed 即拦截（fail-closed）。
type Checker func(data map[string]any) (BlockingResult, error)

// Subscription 是带背压策略的有界事件订阅。
type Subscription struct {
	kinds   []string
	maxsize int
	onFull  string

	mu      sync.Mutex
	cond    *sync.Cond
	items   []map[string]any
	dropped int
	closed  bool
	onClose func()
}

func (s *Subscription) wants(event string) bool {
	if len(s.kinds) == 0 {
		return true
	}
	return containsStr(s.kinds, event)
}

// deliver 入队一个事件，遵循溢出策略。
func (s *Subscription) deliver(event string, data map[string]any) {
	s.cond.L.Lock()
	if s.closed || !s.wants(event) {
		s.cond.L.Unlock()
		return
	}
	if s.maxsize > 0 && len(s.items) >= s.maxsize {
		if s.onFull == "drop" {
			s.dropped++ // 永不阻塞，只计数
			s.cond.L.Unlock()
			return
		}
		for s.maxsize > 0 && len(s.items) >= s.maxsize && !s.closed {
			s.cond.Wait() // block：在此等待 → 背压传给上游
		}
		if s.closed {
			s.cond.L.Unlock()
			return
		}
	}
	item := map[string]any{"event": event}
	for k, v := range data {
		item[k] = v
	}
	s.items = append(s.items, item)
	s.cond.Signal()
	s.cond.L.Unlock()
}

// Get 阻塞等待下一个事件。closed 且排空后 ok 为 false。
func (s *Subscription) Get() (item map[string]any, ok bool) {
	s.cond.L.Lock()
	for len(s.items) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.items) == 0 {
		s.cond.L.Unlock()
		return nil, false
	}
	item = s.items[0]
	s.items = s.items[1:]
	s.cond.Signal() // 腾出一个空位 → 唤醒阻塞的生产者
	s.cond.L.Unlock()
	return item, true
}

// Dropped 返回 drop 策略丢弃的帧数。
func (s *Subscription) Dropped() int {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	return s.dropped
}

// Close 结束订阅并从 Bus 上注销。
func (s *Subscription) Close() {
	s.cond.L.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.cond.L.Unlock()
	if s.onClose != nil {
		s.onClose()
		s.onClose = nil
	}
}

// Bus 是唯一的对外接缝：观察、裁决、订阅。
type Bus struct {
	mu            sync.RWMutex
	observers     map[string][]Observer
	checkers      map[string][]Checker
	subscriptions []*Subscription
}

// NewBus 构造一个空 Bus。
func NewBus() *Bus {
	return &Bus{observers: map[string][]Observer{}, checkers: map[string][]Checker{}}
}

// On 为某事件注册一个观察者。
func (b *Bus) On(event string, h Observer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.observers[event] = append(b.observers[event], h)
}

// AddChecker 为某关卡注册一个裁决者。
func (b *Bus) AddChecker(gate string, h Checker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checkers[gate] = append(b.checkers[gate], h)
}

// Subscribe 返回一个有界订阅。onFull 取 "block" 或 "drop"。
func (b *Bus) Subscribe(kinds []string, maxsize int, onFull string) (*Subscription, error) {
	if onFull != "block" && onFull != "drop" {
		return nil, fmt.Errorf("on_full must be 'block' or 'drop'")
	}
	s := &Subscription{kinds: kinds, maxsize: maxsize, onFull: onFull}
	s.cond = sync.NewCond(&s.mu)
	b.mu.Lock()
	b.subscriptions = append(b.subscriptions, s)
	b.mu.Unlock()
	s.onClose = func() {
		b.mu.Lock()
		out := b.subscriptions[:0]
		for _, x := range b.subscriptions {
			if x != s {
				out = append(out, x)
			}
		}
		b.subscriptions = out
		b.mu.Unlock()
	}
	return s, nil
}

// Fire 广播给观察者：通知所有回调并投递给订阅者。回调报错绝不影响主流程。
// block 型订阅会在此被等待，把压力推回事件生产者。
func (b *Bus) Fire(event string, data map[string]any) {
	b.mu.RLock()
	obs := make([]Observer, len(b.observers[event]))
	copy(obs, b.observers[event])
	var subs []*Subscription
	for _, s := range b.subscriptions {
		if s.wants(event) {
			subs = append(subs, s)
		}
	}
	b.mu.RUnlock()

	for _, h := range obs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[bus] observer error ignored: %v\n", r)
				}
			}()
			h(data)
		}()
	}
	for _, s := range subs {
		s.deliver(event, data)
	}
}

// Check 裁决：按顺序询问；任一否决或报错即拦截（fail-closed）。
func (b *Bus) Check(gate string, data map[string]any) BlockingResult {
	b.mu.RLock()
	cs := make([]Checker, len(b.checkers[gate]))
	copy(cs, b.checkers[gate])
	b.mu.RUnlock()

	for _, h := range cs {
		verdict, err := safeCheck(h, data)
		if err != nil { // checker 坏掉绝不等于"放行"
			return BlockingResult{Allowed: false, Reason: "checker error: " + err.Error()}
		}
		if !verdict.Allowed {
			return verdict
		}
	}
	return BlockingResult{Allowed: true}
}

func safeCheck(h Checker, data map[string]any) (verdict BlockingResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			verdict = BlockingResult{Allowed: false}
			err = fmt.Errorf("%v", r)
		}
	}()
	return h(data)
}
