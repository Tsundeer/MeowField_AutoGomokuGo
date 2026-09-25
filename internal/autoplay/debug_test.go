package autoplay

import (
	"testing"
	"time"

	"MeowField_AutoGomokuGo/internal/domain"
	"MeowField_AutoGomokuGo/internal/engine"
)

func TestDebugScenario1(t *testing.T) {
	logf := func(f string, a ...any) { t.Logf("LOG: "+f, a...) }
	s := NewService(Settings{OurColor: "2", EngineKind: "simple",
		MoveDelay: 0, EngineThreads: 0, ThinkLimit: 1}, logf,
		func(kind string, payload any) { t.Logf("EVT: %v %v", kind, payload) })
	s.MkEngine(func(kind string, think float64) engine.Engine {
		t.Logf("factory kind=%s", kind)
		return &fakeAI{}
	})
	s.active = true
	s.confirmed = nil
	feed3(s, g(sOnly([][2]int{{7, 7}, {7, 8}}, 1)))
	feed3(s, g(sOnly([][2]int{{7, 7}, {7, 8}, {6, 6}}, 2)))
	t.Logf("our=%d expected=%d", s.ourColor, s.expected)
}

func feed3(s *Service, b *domain.BoardGrid) {
	s.sameCount = 99
	s.processChange(b)
	dl := time.Now().Add(8 * time.Second)
	for s.thinking && time.Now().Before(dl) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.thinkPending() {
		s.finishThink()
	}
}
