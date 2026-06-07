// Package profiler provides phase-level startup/runtime profiling and memory
// monitoring for the MCUB kernel.
// Ported from core/lib/utils/profiler.py.
// SPDX-License-Identifier: MIT
package profiler

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Phase profiler (mirrors Python Profiler class)
// ---------------------------------------------------------------------------

type phaseData struct {
	wall   float64 // accumulated wall seconds
	calls  int
	start  float64 // monotonic start; 0 = not running
	paused float64 // monotonic time when paused; 0 = not paused
}

// Profiler is a simple phase-level profiler for MCUB kernel startup & runtime.
//
// Usage:
//
//	p := profiler.New(true)
//	p.Begin("init_db")
//	// ... do work ...
//	p.End("init_db")
//	p.Dump()
type Profiler struct {
	mu        sync.Mutex
	Enabled   bool
	phases    map[string]*phaseData
	order     []string // insertion order
	stack     []string
	startTime time.Time
}

// New creates a new Profiler. Pass enabled=false to make all calls no-ops.
func New(enabled bool) *Profiler {
	return &Profiler{
		Enabled:   enabled,
		phases:    make(map[string]*phaseData),
		startTime: time.Now(),
	}
}

func monoNow() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// Begin starts timing name. Pauses the previous phase if nested.
func (p *Profiler) Begin(name string) {
	if !p.Enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := monoNow()
	if len(p.stack) > 0 {
		parent := p.phases[p.stack[len(p.stack)-1]]
		if parent != nil && parent.paused == 0 {
			parent.paused = now
		}
	}
	p.stack = append(p.stack, name)
	if _, ok := p.phases[name]; !ok {
		p.phases[name] = &phaseData{}
		p.order = append(p.order, name)
	}
	p.phases[name].start = now
}

// End stops timing name and accumulates wall time.
func (p *Profiler) End(name string) {
	if !p.Enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.stack) == 0 || p.stack[len(p.stack)-1] != name {
		return // mismatched end
	}
	now := monoNow()
	phase := p.phases[name]
	if phase.start != 0 {
		elapsed := now - phase.start
		if phase.paused != 0 {
			elapsed -= now - phase.paused
			phase.paused = 0
		}
		phase.wall += elapsed
		phase.calls++
	}
	phase.start = 0
	p.stack = p.stack[:len(p.stack)-1]
	if len(p.stack) > 0 {
		parent := p.phases[p.stack[len(p.stack)-1]]
		if parent != nil {
			parent.start = now
		}
	}
}

// Measure is a helper that calls Begin/End around fn.
func (p *Profiler) Measure(name string, fn func()) {
	p.Begin(name)
	defer p.End(name)
	fn()
}

// Results returns a snapshot of all completed phases.
func (p *Profiler) Results() map[string]map[string]interface{} {
	if !p.Enabled {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]map[string]interface{})
	for _, name := range p.order {
		ph := p.phases[name]
		if ph.calls > 0 {
			out[name] = map[string]interface{}{
				"wall":  ph.wall,
				"calls": ph.calls,
			}
		}
	}
	return out
}

// Dump prints all phase timings sorted by wall time descending.
func (p *Profiler) Dump(prefix string) {
	if !p.Enabled {
		return
	}
	total := time.Since(p.startTime).Seconds()
	res := p.Results()
	type row struct {
		name  string
		wall  float64
		calls int
	}
	var rows []row
	for name, info := range res {
		rows = append(rows, row{name, info["wall"].(float64), info["calls"].(int)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].wall > rows[j].wall })
	fmt.Printf("%s %-50s %-10s %-6s\n", prefix, "Phase", "Wall(s)", "Calls")
	fmt.Printf("%s %s\n", prefix, strings.Repeat("-", 66))
	for _, r := range rows {
		pct := 0.0
		if total > 0 {
			pct = r.wall / total * 100
		}
		fmt.Printf("%s %-50s %-10.4f %-6d (%.1f%%)\n", prefix, r.name, r.wall, r.calls, pct)
	}
	fmt.Printf("%s %s\n", prefix, strings.Repeat("-", 66))
	fmt.Printf("%s %-50s %-10.4f 1\n", prefix, "TOTAL", total)
}

// GetProfileJSON returns JSON-serialisable profile data.
func (p *Profiler) GetProfileJSON() map[string]interface{} {
	return map[string]interface{}{
		"total_wall": time.Since(p.startTime).Seconds(),
		"phases":     p.Results(),
	}
}

// ---------------------------------------------------------------------------
// Global singleton
// ---------------------------------------------------------------------------

var (
	globalOnce     sync.Once
	globalProfiler *Profiler
)

// Get returns the process-wide Profiler singleton. The first call reads the
// MCUB_PROFILE environment variable to decide whether to enable it.
func Get() *Profiler {
	globalOnce.Do(func() {
		enabled := os.Getenv("MCUB_PROFILE") == "1"
		globalProfiler = New(enabled)
	})
	return globalProfiler
}

// Enable force-enables the global profiler (e.g. via --profile flag).
func Enable() {
	Get().Enabled = true
}

// ---------------------------------------------------------------------------
// Memory statistics
// ---------------------------------------------------------------------------

// MemStats holds a snapshot of the process memory usage.
type MemStats struct {
	// HeapAlloc is the bytes of allocated heap objects (from runtime).
	HeapAlloc float64
	// HeapSys is the total heap memory obtained from the OS in MB.
	HeapSys float64
	// RSS is the resident set size in MB read from /proc/self/status (Linux).
	// Zero on platforms where /proc is unavailable.
	RSS float64
	// VMS is the virtual memory size in MB (Linux only).
	VMS float64
	// Percent is an estimate of RSS as a percentage of total system RAM.
	// Zero when the data is unavailable.
	Percent float64
}

// GetMemStats returns a MemStats snapshot. It always succeeds; individual
// fields may be zero when the OS does not expose the information.
func GetMemStats() (*MemStats, error) {
	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)

	ms := &MemStats{
		HeapAlloc: float64(rtm.HeapAlloc) / 1024 / 1024,
		HeapSys:   float64(rtm.HeapSys) / 1024 / 1024,
	}

	// Linux: read /proc/self/status for RSS / VMS and /proc/meminfo for total RAM.
	rss, vms := readProcStatus()
	ms.RSS = rss
	ms.VMS = vms

	total := readTotalRAMMB()
	if total > 0 && ms.RSS > 0 {
		ms.Percent = ms.RSS / total * 100
	}
	return ms, nil
}

func readProcStatus() (rssMB, vmsMB float64) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			rssMB = parseKBLine(line)
		} else if strings.HasPrefix(line, "VmSize:") {
			vmsMB = parseKBLine(line)
		}
	}
	return
}

func parseKBLine(line string) float64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	kb, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0
	}
	return kb / 1024
}

func readTotalRAMMB() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			return parseKBLine(line)
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// GC & cache utilities
// ---------------------------------------------------------------------------

// GCForce triggers three generations of garbage collection.
func GCForce() {
	runtime.GC()
	runtime.GC()
	runtime.GC()
}

// ProfileFunc measures the wall-clock execution time of fn, optionally
// recording it in the global profiler under name.
func ProfileFunc(name string, fn func()) time.Duration {
	start := time.Now()
	fn()
	d := time.Since(start)
	p := Get()
	if p.Enabled && name != "" {
		p.mu.Lock()
		if _, ok := p.phases[name]; !ok {
			p.phases[name] = &phaseData{}
			p.order = append(p.order, name)
		}
		p.phases[name].wall += d.Seconds()
		p.phases[name].calls++
		p.mu.Unlock()
	}
	return d
}

// PurgeCaches asks the kernel to purge its caches at the given level (1-3).
// It uses the same interface as cache_purger.PurgeCaches so that the two
// packages can coexist; this thin wrapper simply delegates to the kernel if it
// exposes a PurgeCaches method.
func PurgeCaches(kernel interface{}, level int) map[string][]string {
	type purger interface {
		PurgeCaches(level int) map[string][]string
	}
	if p, ok := kernel.(purger); ok {
		return p.PurgeCaches(level)
	}
	return map[string][]string{"cleared": {}}
}

// ---------------------------------------------------------------------------
// MemoryGuard
// ---------------------------------------------------------------------------

// MemoryGuard monitors memory usage at regular intervals and calls PurgeCaches
// on the kernel when configurable thresholds are exceeded.
type MemoryGuard struct {
	kernel   interface{}
	// Percentage thresholds.
	L1Pct float64 // default 55 → level-1 purge
	L2Pct float64 // default 70 → level-2 purge
	L3Pct float64 // default 85 → level-3 purge
	// Absolute RSS thresholds in MB.
	L1RSS float64 // default 600 MB
	L2RSS float64 // default 1200 MB
	L3RSS float64 // default 2000 MB
	// How often to sample memory.
	Interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewMemoryGuard creates a MemoryGuard with sensible defaults.
func NewMemoryGuard(kernel interface{}) *MemoryGuard {
	return &MemoryGuard{
		kernel:   kernel,
		L1Pct:    55,
		L2Pct:    70,
		L3Pct:    85,
		L1RSS:    600,
		L2RSS:    1200,
		L3RSS:    2000,
		Interval: 30 * time.Second,
	}
}

// Start begins the background monitoring loop. Cancel the supplied context or
// call Stop() to terminate it.
func (mg *MemoryGuard) Start(ctx context.Context) {
	mg.mu.Lock()
	if mg.cancel != nil {
		mg.mu.Unlock()
		return // already running
	}
	ctx, cancel := context.WithCancel(ctx)
	mg.cancel = cancel
	mg.mu.Unlock()

	go func() {
		ticker := time.NewTicker(mg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ms, err := GetMemStats()
				if err != nil {
					continue
				}
				level := 0
				switch {
				case ms.RSS >= mg.L3RSS || ms.Percent >= mg.L3Pct:
					level = 3
				case ms.RSS >= mg.L2RSS || ms.Percent >= mg.L2Pct:
					level = 2
				case ms.RSS >= mg.L1RSS || ms.Percent >= mg.L1Pct:
					level = 1
				}
				if level > 0 {
					PurgeCaches(mg.kernel, level)
				}
			}
		}
	}()
}

// Stop terminates the background monitoring loop.
func (mg *MemoryGuard) Stop() {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	if mg.cancel != nil {
		mg.cancel()
		mg.cancel = nil
	}
}
