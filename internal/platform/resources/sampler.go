// Package resources samples the worker host's process-level CPU and memory usage.
//
// The Go worker owns the process tree that runs the Python runtime. Phase 06 reports the Go host's usage; the
// runtime phase may extend this sampler with sandbox/cgroup metrics.
package resources

import (
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Usage is one sample of process resource consumption.
type Usage struct {
	CPUPercent float64 `json:"cpu_usage_percent"`
	MemoryMB   int64   `json:"memory_used_mb"`
}

// Sampler computes CPU usage as the delta of process CPU time over wall time. It is safe for concurrent use.
type Sampler struct {
	mu      sync.Mutex
	lastCPU float64
	lastAt  time.Time
}

// New builds a sampler. The first sample has zero CPU usage because there is no previous interval.
func New() *Sampler {
	return &Sampler{}
}

// Sample returns the usage since the previous call.
func (s *Sampler) Sample() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	var usage syscall.Rusage
	now := time.Now()
	cpuPercent := s.lastCPU
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err == nil {
		cpuSeconds := float64(usage.Utime.Sec+usage.Stime.Sec) +
			float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
		if !s.lastAt.IsZero() && now.After(s.lastAt) {
			elapsed := now.Sub(s.lastAt).Seconds()
			cpuPercent = (cpuSeconds - s.lastCPU) / elapsed * 100
		} else {
			cpuPercent = 0
		}
		s.lastCPU = cpuSeconds
		s.lastAt = now
	}
	if cpuPercent < 0 {
		cpuPercent = 0
	}
	if cpuPercent > 100 {
		cpuPercent = 100
	}
	return Usage{
		CPUPercent: cpuPercent,
		MemoryMB:   int64(memory.Sys / (1024 * 1024)), //nolint:gosec // process memory is far below int64.
	}
}
