package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SecurityHeadersMiddleware aplica cabeçalhos padrão de segurança HTTP em todas as respostas.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-XSS-Protection", "0")
		h.Set("Permissions-Policy", "geolocation=(self), microphone=(), camera=()")
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware habilita CORS configurável (fail-closed quando vazio) e responde preflight OPTIONS.
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowAll := false
	allowedMap := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		trimmed := strings.TrimSpace(o)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			allowAll = true
		}
		allowedMap[strings.ToLower(trimmed)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			h := w.Header()

			if allowAll {
				h.Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && allowedMap[strings.ToLower(origin)] {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}

			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Accept")
			h.Set("Access-Control-Expose-Headers", "X-Cache-Status, X-Cache-Cooldown-Remaining, Retry-After")
			h.Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(p []byte) (int, error) {
	n, err := sr.ResponseWriter.Write(p)
	sr.bytes += n
	return n, err
}

func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// LoggingMiddleware registra logs estruturados JSON/texto de cada requisição HTTP.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			durMs := float64(time.Since(start).Microseconds()) / 1000.0
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", durMs,
				"bytes", rec.bytes,
				"ip", clientIP(r),
			}
			if r.URL.RawQuery != "" {
				attrs = append(attrs, "query", r.URL.RawQuery)
			}

			switch {
			case rec.status >= 500:
				logger.Error("http_request", attrs...)
			case rec.status >= 400:
				logger.Warn("http_request", attrs...)
			default:
				logger.Info("http_request", attrs...)
			}
		})
	}
}

// RecoverMiddleware protege o servidor HTTP contra panics inesperados.
func RecoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("http_panic_recovered", "panic", rec, "path", r.URL.Path)
					writeJSON(w, http.StatusInternalServerError, map[string]any{
						"error":   "internal_server_error",
						"message": "Erro interno inesperado ao processar a requisição.",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type ipBucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

// RateLimiter implementa controle de taxa por IP via Token Bucket com limpeza automática
// e integração opcional com verificador distribuído (Upstash Redis).
type RateLimiter struct {
	mu               sync.Mutex
	rpm              int
	ratePerSec       float64
	burst            float64
	buckets          map[string]*ipBucket
	checkCounter     uint64
	distributedCheck func(ip string, rpm int) (allowed bool, checked bool)
}

// NewRateLimiter cria um limitador por IP para rpm (requisições/minuto) e capacidade de rajada (burst).
// Valores <= 0 desabilitam a limitação.
func NewRateLimiter(rpm int, burst int) *RateLimiter {
	if rpm <= 0 || burst <= 0 {
		return &RateLimiter{}
	}
	return &RateLimiter{
		rpm:        rpm,
		ratePerSec: float64(rpm) / 60.0,
		burst:      float64(burst),
		buckets:    make(map[string]*ipBucket),
	}
}

// SetDistributedCheck registra uma função de verificação distribuída (ex: Upstash Redis) para serverless.
func (rl *RateLimiter) SetDistributedCheck(fn func(ip string, rpm int) (bool, bool)) {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	rl.distributedCheck = fn
	rl.mu.Unlock()
}

// Allow verifica se o IP possui tokens disponíveis e devolve o tempo de espera em caso negativo.
func (rl *RateLimiter) Allow(ip string) (bool, time.Duration) {
	if rl == nil || rl.ratePerSec <= 0 || rl.burst <= 0 {
		return true, 0
	}

	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.checkCounter++
	if rl.checkCounter%256 == 0 {
		rl.cleanupLocked(now)
	}

	b, ok := rl.buckets[ip]
	if !ok {
		rl.buckets[ip] = &ipBucket{
			tokens:     rl.burst - 1.0,
			lastRefill: now,
			lastSeen:   now,
		}
		return true, 0
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(rl.burst, b.tokens+elapsed*rl.ratePerSec)
		b.lastRefill = now
	}
	b.lastSeen = now

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, 0
	}

	needed := 1.0 - b.tokens
	waitSec := needed / rl.ratePerSec
	return false, time.Duration(math.Ceil(waitSec*1000)) * time.Millisecond
}

func (rl *RateLimiter) cleanupLocked(now time.Time) {
	for ip, b := range rl.buckets {
		if now.Sub(b.lastSeen) > 10*time.Minute {
			delete(rl.buckets, ip)
		}
	}
	// Proteção contra exaustão de memória (OOM) caso haja spoofing massivo de IPs
	const maxTrackedIPs = 10000
	if len(rl.buckets) > maxTrackedIPs {
		for ip := range rl.buckets {
			delete(rl.buckets, ip)
			if len(rl.buckets) <= maxTrackedIPs/2 {
				break
			}
		}
	}
}

// Middleware aplica rate limiting por IP e retorna HTTP 429 com Retry-After quando excedido.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl == nil || rl.ratePerSec <= 0 || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		allowed, retryAfter := rl.Allow(ip)
		if allowed && rl.rpm > 0 {
			rl.mu.Lock()
			distFn := rl.distributedCheck
			rl.mu.Unlock()
			if distFn != nil {
				if distAllowed, checked := distFn(ip, rl.rpm); checked && !distAllowed {
					allowed = false
					retryAfter = 15 * time.Second
				}
			}
		}
		if !allowed {
			secs := int(math.Ceil(retryAfter.Seconds()))
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":             "rate_limit_exceeded",
				"message":           "Limite de requisições por IP excedido. Aguarde alguns segundos.",
				"retryAfterSeconds": secs,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func trustProxyHeaders() bool {
	if strings.TrimSpace(os.Getenv("VERCEL")) != "" || strings.TrimSpace(os.Getenv("VERCEL_ENV")) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TRUST_PROXY_HEADERS")), "true")
}

func parseValidIP(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 64 {
		return ""
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.String()
	}
	return ""
}

func clientIP(r *http.Request) string {
	if trustProxyHeaders() {
		// Prioriza X-Real-IP injetado pela borda da Vercel antes de X-Forwarded-For, validando formato IP.
		if ip := parseValidIP(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				if ip := parseValidIP(parts[0]); ip != "" {
					return ip
				}
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		if ip := parseValidIP(host); ip != "" {
			return ip
		}
		return host
	}
	if r.RemoteAddr == "" {
		return "unknown"
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
