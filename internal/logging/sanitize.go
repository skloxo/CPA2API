package logging

import "strings"

// SanitizeAPIKey redacts an API key for safe logging.
func SanitizeAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// SanitizeLog redacts sensitive information from a log string.
func SanitizeLog(s string) string {
	s = sanitizeBearer(s)
	s = sanitizeAPIKeyPatterns(s)
	return s
}

func sanitizeBearer(s string) string {
	idx := strings.Index(s, "Bearer ")
	if idx < 0 {
		return s
	}
	start := idx + len("Bearer ")
	if start >= len(s) {
		return s
	}
	rest := s[start:]
	end := strings.IndexAny(rest, " \t\"',;)}\n\r")
	if end < 0 {
		return s[:start+4] + "****" + rest[len(rest)-4:]
	}
	key := rest[:end]
	if len(key) <= 8 {
		return s[:start] + "****" + rest[end:]
	}
	return s[:start] + key[:4] + "****" + key[len(key)-4:] + rest[end:]
}

func sanitizeAPIKeyPatterns(s string) string {
	for _, prefix := range []string{"sk-", "sk_live_", "sk_proj_", "AIza"} {
		idx := strings.Index(s, prefix)
		for idx >= 0 {
			start := idx + len(prefix)
			end := strings.IndexAny(s[start:], " \t\"',;)}\n\r")
			if end < 0 {
				end = len(s) - start
			}
			key := s[start : start+end]
			if len(key) > 4 {
				s = s[:start] + key[:4] + "****" + key[len(key)-4:] + s[start+end:]
			} else {
				s = s[:start] + "****" + s[start+end:]
			}
			idx = strings.Index(s[start+len(prefix)+8:], prefix)
			if idx >= 0 {
				idx += start + len(prefix) + 8
			}
		}
	}
	return s
}
