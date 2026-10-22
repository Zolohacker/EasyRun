package portal

import (
	"errors"
	"sync"
)

// Pause is the platform updater's compound barrier: it excludes both running
// jobs and wallet work. It never cancels an energy rental or transaction halfway.
func (s *Service) Pause(reason string) (func(), error) {
	s.backgroundMu.Lock()
	if s.backgroundPaused || s.backgroundRunning || s.collectionPending || len(s.scanRequested) > 0 || s.ClientVersions == nil {
		s.backgroundMu.Unlock()
		return nil, errors.New("仍有后台事务或平台维护正在进行，请在空闲时重试")
	}
	s.backgroundPaused = true
	s.backgroundMu.Unlock()
	unpause, err := s.ClientVersions.Pause(reason)
	if err != nil {
		s.backgroundMu.Lock()
		s.backgroundPaused = false
		s.backgroundMu.Unlock()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { unpause(); s.backgroundMu.Lock(); s.backgroundPaused = false; s.backgroundMu.Unlock() })
	}, nil
}

func (s *Service) beginBackground() (bool, func(), bool) {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.backgroundPaused || s.backgroundRunning {
		return false, func() {}, false
	}
	s.backgroundRunning = true
	collect := s.collectionPending
	s.collectionPending = false
	return collect, func() { s.backgroundMu.Lock(); s.backgroundRunning = false; s.backgroundMu.Unlock() }, true
}
func (s *Service) queueCollection() error {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.backgroundPaused {
		return errors.New("平台更新维护中，请稍后再发起归集")
	}
	s.collectionPending = true
	select {
	case s.collectRequested <- struct{}{}:
	default:
	}
	return nil
}
