package testutil

import "testing"

// TestMockStrictMode verifies opt-in strict mode: an unconfigured method
// panics when Strict is set, and is silent (zero/default) when it isn't.
func TestMockStrictMode(t *testing.T) {
	t.Run("ArrClient strict panics on unconfigured call", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from strict unconfigured DeleteFile, got none")
			}
		}()
		m := &MockArrClient{Strict: true}
		_, _ = m.DeleteFile(1, "/x") // no DeleteFileFunc → should panic
		t.Error("DeleteFile returned instead of panicking in strict mode")
	})

	t.Run("ArrClient non-strict returns zero silently", func(t *testing.T) {
		m := &MockArrClient{} // Strict defaults false
		id, err := m.FindMediaByPath("/x")
		if id != 0 || err != nil {
			t.Errorf("non-strict default = (%d, %v), want (0, nil)", id, err)
		}
	})

	t.Run("ArrClient strict allows configured call", func(t *testing.T) {
		m := &MockArrClient{
			Strict:              true,
			FindMediaByPathFunc: func(string) (int64, error) { return 42, nil },
		}
		if id, _ := m.FindMediaByPath("/x"); id != 42 {
			t.Errorf("configured func not used: got %d", id)
		}
	})

	t.Run("PathMapper strict panics; non-strict identity", func(t *testing.T) {
		if got, _ := (&MockPathMapper{}).ToArrPath("/p"); got != "/p" {
			t.Errorf("non-strict identity default broken: %q", got)
		}
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from strict unconfigured ToArrPath")
			}
		}()
		_, _ = (&MockPathMapper{Strict: true}).ToArrPath("/p")
	})

	t.Run("HealthChecker strict panics; non-strict healthy", func(t *testing.T) {
		if ok, _ := (&MockHealthChecker{}).Check("/p", "quick"); !ok {
			t.Error("non-strict healthy default broken")
		}
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from strict unconfigured Check")
			}
		}()
		_, _ = (&MockHealthChecker{Strict: true}).Check("/p", "quick")
	})
}
