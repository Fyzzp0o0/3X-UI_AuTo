package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestChainRateLimiterBurstRefillAndIsolation(t *testing.T) {
	l := newChainRateLimiter()
	first := l.allow("k", 60, 2)
	second := l.allow("k", 60, 2)
	if !first || !second {
		t.Fatal("burst of 2 must be allowed")
	}
	if l.allow("k", 60, 2) {
		t.Fatal("third request inside the same second must be denied")
	}
	if !l.allow("other", 60, 2) {
		t.Fatal("a different key must have its own bucket")
	}
	time.Sleep(1100 * time.Millisecond)
	if !l.allow("k", 60, 2) {
		t.Fatal("bucket must refill over time")
	}
}

func TestChainLimitMiddlewareReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/limited", chainLimit("test-"+time.Now().Format("150405.000000"), 1, 1), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/limited", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", first.Code)
	}
	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/limited", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
}
