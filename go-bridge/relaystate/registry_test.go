package relaystate

import (
	"testing"
	"time"
)

func TestRegistry_PutIfAbsent_DedupAndReuse(t *testing.T) {
	r := NewCorrelatedRegistry(8, 8)
	// 首次登记
	ok, reason := r.PutIfAbsent("c1")
	if !ok || reason != "admitted" {
		t.Fatalf("first put: ok=%v reason=%s", ok, reason)
	}
	// duplicate（active 中）-> already_active
	ok, reason = r.PutIfAbsent("c1")
	if ok || reason != "already_active" {
		t.Errorf("duplicate active: ok=%v reason=%s (want already_active)", ok, reason)
	}
	// retire 后 reuse 窗口内 -> reuse（禁止复用）
	if !r.Retire("c1") {
		t.Fatal("retire c1 failed")
	}
	ok, reason = r.PutIfAbsent("c1")
	if ok || reason != "reuse" {
		t.Errorf("reuse after retire: ok=%v reason=%s (want reuse)", ok, reason)
	}
}

func TestRegistry_QuotaBusy(t *testing.T) {
	r := NewCorrelatedRegistry(2, 8)
	r.PutIfAbsent("c1")
	r.PutIfAbsent("c2")
	// active 满 -> busy
	ok, reason := r.PutIfAbsent("c3")
	if ok || reason != "busy" {
		t.Errorf("active full: ok=%v reason=%s (want busy)", ok, reason)
	}
	// retire 一个后腾出 quota
	r.Retire("c1")
	ok, _ = r.PutIfAbsent("c3")
	if !ok {
		t.Error("after retire, new put should admit")
	}
}

func TestRegistry_RetiredOverflow(t *testing.T) {
	r := NewCorrelatedRegistry(8, 1)
	r.PutIfAbsent("c1")
	r.Retire("c1") // retired 满
	r.PutIfAbsent("c2")
	if r.Retire("c2") {
		t.Error("retired overflow should return false (caller closes generation, no silent LRU)")
	}
}

// deadline 不变式：120 > 90 > 30 > 15。
func TestDeadlineInvariants(t *testing.T) {
	if err := CheckDeadlineInvariants(); err != nil {
		t.Fatal(err)
	}
}

// fake-clock：各 deadline 在边界前/后判定正确。
func TestDeadlines_FakeClock(t *testing.T) {
	start := time.Unix(1750000000, 0)
	clock := NewFakeClock(start)

	// pre-first：commit 后 29s 未到；30s 到。
	if PreFirstDeadline(start, clock.Now()) {
		t.Error("pre-first should not fire before 30s")
	}
	clock.Advance(PreFirstChunkIdle) // = 30s
	if !PreFirstDeadline(start, clock.Now()) {
		t.Error("pre-first should fire at 30s")
	}

	// client total cap：90s
	clock2 := NewFakeClock(start)
	clock2.Advance(89 * time.Second)
	if ClientTotalExceeded(start, clock2.Now()) {
		t.Error("client total should not fire before 90s")
	}
	clock2.Advance(time.Second)
	if !ClientTotalExceeded(start, clock2.Now()) {
		t.Error("client total should fire at 90s")
	}

	// server group max age 120s > client 90s（串联：server 窗口晚于 client）
	if !(ServerGroupMaxAge > ClientTotalCap) {
		t.Fatal("server max age must exceed client total cap")
	}

	// inter-chunk idle 15s
	last := start
	clock3 := NewFakeClock(start)
	clock3.Advance(14 * time.Second)
	if InterChunkExceeded(last, clock3.Now()) {
		t.Error("inter-chunk should not fire before 15s")
	}
	clock3.Advance(time.Second)
	if !InterChunkExceeded(last, clock3.Now()) {
		t.Error("inter-chunk should fire at 15s")
	}
}
