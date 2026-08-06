package github

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const quotaBufferRatio = 0.02

func computeQuotaBuffer(limit int) int {
	if b := int(math.Ceil(float64(limit) * quotaBufferRatio)); b > 1 {
		return b
	}
	return 1
}

func resetAtFromHeaders(headers map[string][]string) time.Time {
	get := func(key string) string {
		for k, v := range headers {
			if strings.EqualFold(k, key) && len(v) > 0 {
				return v[0]
			}
		}
		return ""
	}
	if ra := get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
	}
	if rr := get("X-RateLimit-Reset"); rr != "" {
		if unix, err := strconv.ParseInt(rr, 10, 64); err == nil {
			return time.Unix(unix, 0)
		}
	}
	return time.Now().Add(60 * time.Second)
}

func classifyForbidden(headers http.Header) (time.Time, bool) {
	if headers.Get("Retry-After") != "" {
		return resetAtFromHeaders(headers), true
	}
	if headers.Get("X-RateLimit-Remaining") == "0" {
		return resetAtFromHeaders(headers), true
	}
	return time.Time{}, false
}