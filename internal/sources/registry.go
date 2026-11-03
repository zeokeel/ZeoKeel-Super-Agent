package sources

import (
	"fmt"
	"sort"
	"sync"
)

// Factory 用一个 Source 实例的已存配置（JSON）构造它。这里用 []byte 而不是
// 泛型参数，是因为本包必须对任何 Source 的配置类型一无所知：配置的 schema
// 属于各自实现（GitHub 的仓库/账号、飞书的 chat id…）。
type Factory func(config []byte) (Kind, error)

// registry 是编译期注册表。用显式类型而不是一堆包级 map，测试可以建一个
// 独立的 registry 验证注册语义，不污染全局。
type registry struct {
	mu sync.RWMutex
	m  map[Name]Factory
}

func newRegistry() *registry { return &registry{m: map[Name]Factory{}} }

// register 拒绝空名和重名：两者都是编程错误，应该在建进程时就炸出来，
// 而不是运行时悄悄覆盖掉一个 Source。
func (r *registry) register(name Name, f Factory) error {
	if name == "" {
		return fmt.Errorf("sources: source kind name is empty")
	}
	if f == nil {
		return fmt.Errorf("sources: source kind %q has a nil factory", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[name]; ok {
		return fmt.Errorf("sources: source kind %q already registered", name)
	}
	r.m[name] = f
	return nil
}

func (r *registry) lookup(name Name) (Factory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.m[name]
	return f, ok
}

func (r *registry) names() []Name {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Name, 0, len(r.m))
	for name := range r.m {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// defaultRegistry 是进程级注册表。Source 实现在自己的 init() 里注册，因此
// "新增一个 Source"在编译期就确定了，运行时没有任何发现/加载逻辑（ADR-6）。
var defaultRegistry = newRegistry()

// Register 把一个 Source kind 的工厂登记进进程级注册表。通常在实现的
// init() 里调用：
//
//	func init() { sources.Register(Name, newFromConfig) }
//
// 空名或重名会 panic：这是编译期约定被违反，属于程序错误，越早暴露越好。
func Register(name Name, f Factory) {
	if err := defaultRegistry.register(name, f); err != nil {
		panic(err)
	}
}

// Lookup returns one kind's factory.
func Lookup(name Name) (Factory, bool) { return defaultRegistry.lookup(name) }

// Names returns every registered kind name, sorted. 面板"来源"页用它列出
// 可以添加的 Source 类型。
func Names() []Name { return defaultRegistry.names() }

// New builds one configured Source instance from its stored configuration.
// Runtime 从 store 取出配置 JSON，交给这里构造；它不需要 import 具体的
// source 包，只需要对某个包做一次 blank import 触发 init 注册。
func New(name Name, config []byte) (Kind, error) {
	f, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("sources: unknown source kind %q", name)
	}
	return f(config)
}
