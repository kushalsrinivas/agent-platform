package resources

import "testing"

func TestSamplerReportsNonNegativeUsage(t *testing.T) {
	sampler := New()
	first := sampler.Sample()
	if first.CPUPercent < 0 || first.CPUPercent > 100 {
		t.Fatalf("cpu percent = %v", first.CPUPercent)
	}
	if first.MemoryMB < 0 {
		t.Fatalf("memory = %d", first.MemoryMB)
	}
	second := sampler.Sample()
	if second.CPUPercent < 0 || second.CPUPercent > 100 {
		t.Fatalf("second cpu percent = %v", second.CPUPercent)
	}
	if second.MemoryMB <= 0 {
		t.Fatalf("expected a positive process memory estimate, got %d", second.MemoryMB)
	}
}
