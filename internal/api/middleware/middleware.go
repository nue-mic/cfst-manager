// Package middleware 提供 HTTP 中间件：跨域、访问日志、panic 恢复、Bearer 鉴权。
package middleware

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// CORS 按允许来源设置跨域响应头。origins 含 "*" 时放行任意来源。
func CORS(origins []string) func(http.Handler) http.Handler {
	wildcard := IsWildcard(origins)
	allow := map[string]struct{}{}
	for _, o := range origins {
		allow[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if wildcard {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if _, ok := allow[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IsWildcard 判断是否含通配来源。
func IsWildcard(origins []string) bool {
	for _, o := range origins {
		if o == "*" {
			return true
		}
	}
	return false
}

// AccessLog 记录每个请求的方法/路径/状态/耗时。
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(sw, r)
			if log != nil {
				log.Debug("http",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", sw.status),
					slog.Duration("dur", time.Since(start)),
				)
			}
		})
	}
}

// Recover 捕获处理器 panic，返回 500。
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if log != nil {
						log.Error("panic", slog.Any("err", rec), slog.String("path", r.URL.Path))
					}
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"ok":false,"error":"内部错误"}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Bearer 校验管理端令牌。支持 Authorization: Bearer <token>，
// 也支持 SSE 等无法设置头的场景用 ?access_token= 查询参数。
func Bearer(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 失败关闭：令牌为空绝不放行（防止任何误配置导致管理端无鉴权）。
			if token == "" {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"ok":false,"error":"服务未配置管理令牌"}`))
				return
			}
			provided := ""
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				provided = strings.TrimPrefix(h, "Bearer ")
			} else if q := r.URL.Query().Get("access_token"); q != "" {
				provided = q
			}
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"ok":false,"error":"未授权：请提供有效的管理令牌"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush 透传，使 SSE 可用。
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
