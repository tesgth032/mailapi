package domainutil

import (
	"strings"

	"mailapi/internal/model"
)

// Normalize 将域名统一为小写并去除首尾空白。
func Normalize(domain string) string {
	return strings.ToLower(strings.TrimSpace(domain))
}

// Candidates 返回“精确域名 -> 逐级父域”的候选序列。
// 例如 "a.b.example.com" => ["a.b.example.com", "b.example.com", "example.com", "com"]。
func Candidates(domain string) []string {
	domain = Normalize(domain)
	if domain == "" {
		return nil
	}

	parts := strings.Split(domain, ".")
	out := make([]string, 0, len(parts))
	for i := range parts {
		candidate := strings.Join(parts[i:], ".")
		if candidate == "" {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// MatchSet 在允许域名集合中查找最具体的匹配（精确优先，其次父域）。
func MatchSet(domain string, allowed map[string]struct{}) (string, bool) {
	if len(allowed) == 0 {
		return "", false
	}

	for _, candidate := range Candidates(domain) {
		if _, ok := allowed[candidate]; ok {
			return candidate, true
		}
	}
	return "", false
}

// MatchMapKey 在字符串 map 中查找最具体的匹配 key（精确优先，其次父域）。
func MatchMapKey[T any](domain string, values map[string]T) (string, bool) {
	if len(values) == 0 {
		return "", false
	}

	for _, candidate := range Candidates(domain) {
		if _, ok := values[candidate]; ok {
			return candidate, true
		}
	}
	return "", false
}

// MatchSlice 在允许域名切片中查找最具体的匹配（精确优先，其次父域）。
func MatchSlice(domain string, allowed []string) (string, bool) {
	if len(allowed) == 0 {
		return "", false
	}

	set := make(map[string]struct{}, len(allowed))
	for _, item := range allowed {
		item = Normalize(item)
		if item == "" || item == "*" {
			continue
		}
		set[item] = struct{}{}
	}
	return MatchSet(domain, set)
}

// ResolveDomain 在活动域名列表中查找最具体的匹配域名（精确优先，其次父域）。
func ResolveDomain(domain string, domains []model.Domain) (*model.Domain, string, bool) {
	if len(domains) == 0 {
		return nil, "", false
	}

	indexByDomain := make(map[string]int, len(domains))
	for i := range domains {
		if !domains[i].IsActive {
			continue
		}
		name := Normalize(domains[i].Domain)
		if name == "" {
			continue
		}
		indexByDomain[name] = i
	}

	for _, candidate := range Candidates(domain) {
		if idx, ok := indexByDomain[candidate]; ok {
			return &domains[idx], candidate, true
		}
	}
	return nil, "", false
}
