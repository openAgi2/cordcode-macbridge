package codexremote

import (
	"testing"
	"time"
)

// 对齐官方 remote-control 策略（codex-rs websocket.rs next_reconnect_delay）：
// 1s 起步倍增、30s 封顶、±10% 抖动、封顶后归零重数、成功 Reset 回到基准。
func TestReconnectBackoffSequence(t *testing.T) {
	b := newReconnectBackoff()
	wantBase := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second,
	}
	for i, want := range wantBase {
		got := b.Next()
		lo := time.Duration(float64(want) * 0.89)
		hi := time.Duration(float64(want) * 1.11)
		if got < lo || got > hi {
			t.Fatalf("step %d: delay = %s, want %s ±10%%", i, got, want)
		}
	}
	// 封顶那次返回后 attempt 归零：下一次回到 ~1s 而不是继续 30s。
	if got := b.Next(); got > 1200*time.Millisecond {
		t.Fatalf("after cap, delay = %s, want ~1s (reset)", got)
	}
}

func TestReconnectBackoffResetOnSuccess(t *testing.T) {
	b := newReconnectBackoff()
	for i := 0; i < 4; i++ {
		_ = b.Next()
	}
	b.Reset()
	if got := b.Next(); got > 1200*time.Millisecond {
		t.Fatalf("after Reset, delay = %s, want ~1s", got)
	}
}

func TestSleepInterruptibleReturnsEarlyOnStop(t *testing.T) {
	a := New(nil)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = a.Stop()
	}()
	start := time.Now()
	a.sleepInterruptible(30 * time.Second)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("sleep took %s, want interruptible within ~2s of Stop", elapsed)
	}
}
