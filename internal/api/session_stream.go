package api

import (
	"context"
	"time"
)

// monitorSession 在长连接存续期间复核会话，停用、重置或注销后关闭已建立的事件流和日志流。
func (s *Server) monitorSession(parent context.Context, token string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		defer cancel()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 后台复核不续期，避免只打开页面也能一直保持会话。
				if _, err := s.svc.Authenticate(ctx, token, false); err != nil {
					return
				}
			}
		}
	}()
	return ctx, cancel
}
