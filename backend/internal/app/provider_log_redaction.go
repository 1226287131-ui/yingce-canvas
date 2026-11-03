package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"yingce/backend/internal/model"
)

type customProviderLogCredentials struct {
	Secrets []string
	Headers []OutboundHeader
}

// Upstream messages can echo credentials in ordinary text, so field-name based
// payload sanitization alone is insufficient. Apply known-value redaction after
// all response/error enrichment, without changing the actual provider response.
func redactCustomProviderLogCredentials(req *http.Request, callLog *model.ApiCallLog) {
	endpoint, _ := req.Context().Value(customProviderEndpointKey{}).(string)
	if endpoint == "" || !strings.EqualFold(endpoint, req.URL.Host) {
		return
	}
	known, _ := req.Context().Value(customProviderLogCredentialsKey{}).(customProviderLogCredentials)
	credentials := append([]string(nil), known.Secrets...)
	var bounded []string
	for _, header := range known.Headers {
		if sensitiveProviderCredentialName(header.Name) || len(strings.TrimSpace(header.Value)) >= 8 {
			credentials = append(credentials, header.Value)
		} else {
			bounded = append(bounded, header.Value)
		}
	}
	for name, values := range req.Header {
		if sensitiveProviderCredentialName(name) || strings.EqualFold(name, "Cookie") {
			credentials = append(credentials, values...)
		}
		for _, value := range values {
			if scheme, token, ok := strings.Cut(strings.TrimSpace(value), " "); ok && strings.EqualFold(scheme, "Bearer") {
				credentials = append(credentials, strings.TrimSpace(token))
			}
		}
	}
	if username, password, ok := req.BasicAuth(); ok {
		credentials = append(credentials, username, password)
	}
	for _, cookie := range req.Cookies() {
		credentials = append(credentials, cookie.Value)
	}
	for name, values := range req.URL.Query() {
		if sensitiveProviderCredentialName(name) {
			credentials = append(credentials, values...)
		}
	}
	if req.URL.User != nil {
		credentials = append(credentials, req.URL.User.Username())
		if password, ok := req.URL.User.Password(); ok {
			credentials = append(credentials, password)
		}
	}
	values := providerLogCredentialVariants(credentials)
	bounded = providerLogCredentialVariants(bounded)
	if len(values) == 0 && len(bounded) == 0 {
		return
	}
	pairs := make([]string, 0, len(values)*2)
	for _, value := range values {
		pairs = append(pairs, value, "[REDACTED]")
	}
	replacer := strings.NewReplacer(pairs...)
	// These fields contain diagnostics only. Provider IDs/status, paths and error
	// codes are also consumed by task recovery and billing and must stay intact.
	callLog.RequestBody = redactProviderLogPayload(callLog.RequestBody, replacer, bounded)
	callLog.ResponseBody = redactProviderLogPayload(callLog.ResponseBody, replacer, bounded)
	callLog.Error = redactProviderLogText(callLog.Error, replacer, bounded)
}

func providerLogCredentialVariants(credentials []string) []string {
	variants := make(map[string]struct{}, len(credentials)*4)
	for _, credential := range credentials {
		credential = strings.TrimSpace(credential)
		if credential == "" {
			continue
		}
		variants[credential] = struct{}{}
		variants[url.QueryEscape(credential)] = struct{}{}
		variants[url.PathEscape(credential)] = struct{}{}
		encoded, _ := json.Marshal(credential)
		variants[string(encoded[1:len(encoded)-1])] = struct{}{}
	}
	values := make([]string, 0, len(variants))
	for value := range variants {
		values = append(values, value)
	}
	// Longer variants must win when a token is also part of an auth header.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

func redactProviderLogPayload(value string, replacer *strings.Replacer, bounded []string) string {
	if json.Valid([]byte(value)) {
		var payload any
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.UseNumber()
		if decoder.Decode(&payload) == nil {
			redacted := redactProviderLogJSON(payload, replacer, bounded)
			if encoded, err := json.MarshalIndent(redacted, "", "  "); err == nil {
				return string(encoded)
			}
		}
	}
	return redactProviderLogText(value, replacer, bounded)
}

func redactProviderLogJSON(value any, replacer *strings.Replacer, bounded []string) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			redacted[replacer.Replace(key)] = redactProviderLogJSON(child, replacer, bounded)
		}
		return redacted
	case []any:
		for i, child := range typed {
			typed[i] = redactProviderLogJSON(child, replacer, bounded)
		}
		return typed
	case string:
		return redactProviderLogText(typed, replacer, bounded)
	case json.Number:
		// Known secrets have no length exemption; keep JSON valid if a short
		// secret also appears as a number. Ordinary Header values skip numbers.
		if replacer.Replace(typed.String()) != typed.String() {
			return "[REDACTED]"
		}
	}
	return value
}

func redactProviderLogText(value string, replacer *strings.Replacer, bounded []string) string {
	value = replacer.Replace(value)
	for _, credential := range bounded {
		var result strings.Builder
		offset := 0
		for {
			match := strings.Index(value[offset:], credential)
			if match < 0 {
				result.WriteString(value[offset:])
				break
			}
			index := offset + match
			end := index + len(credential)
			before, after := false, false
			if index > 0 {
				r, _ := utf8.DecodeLastRuneInString(value[:index])
				before = providerLogCredentialTokenRune(r)
			}
			if end < len(value) {
				r, _ := utf8.DecodeRuneInString(value[end:])
				after = providerLogCredentialTokenRune(r)
			}
			result.WriteString(value[offset:index])
			if before || after {
				result.WriteString(credential)
			} else {
				result.WriteString("[REDACTED]")
			}
			offset = end
		}
		value = result.String()
	}
	return value
}

func providerLogCredentialTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune("-_./:@+%", r)
}

func sensitiveProviderCredentialName(name string) bool {
	name = strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(name))
	if name == "key" {
		return true
	}
	for _, marker := range []string{"apikey", "accesskey", "token", "secret", "password", "authorization", "signature", "credential"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}
