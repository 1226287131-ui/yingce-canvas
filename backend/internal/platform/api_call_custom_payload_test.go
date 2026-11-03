package platform

import (
	"strings"
	"testing"
)

func TestCustomAPIPayloadRedactsConnectionCredentials(t *testing.T) {
	got := SanitizeAPICallPayload([]byte(`{"customModels":[{"config":{"baseUrl":"https://name:private-url-password@provider.example/v1?api_key=private-query-key&secret=private-query-secret&region=cn","apiKey":"private-api-key","secretKey":"private-secret-key","headers":[{"name":"X-Gateway-Key","value":"private-gateway-header"}]}}],"prompt":"keep this prompt"}`), "application/json")
	for _, secret := range []string{"private-url-password", "private-query-key", "private-query-secret", "private-api-key", "private-secret-key", "private-gateway-header"} {
		if strings.Contains(got, secret) {
			t.Fatalf("custom connection credential leaked into API log: %s", secret)
		}
	}
	if !strings.Contains(got, "keep this prompt") || !strings.Contains(got, "region=cn") {
		t.Fatal("sanitizer discarded ordinary debugging fields")
	}
}
