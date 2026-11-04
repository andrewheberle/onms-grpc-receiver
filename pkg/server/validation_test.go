package server

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

func TestCheckHTTPURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		link    bool
		wantErr string
	}{
		{"http", "http://am:9093", false, ""},
		{"https with path", "https://am:9093/prefix/", false, ""},
		{"link", "https://horizon:8980/opennms/", true, ""},
		{"credentials", "https://noc:secret@am:9093", false, ""},
		{"credentials in link", "https://noc:secret@horizon:8980/opennms/", true, `"https://horizon:8980/opennms/": must not have credentials`},
		{"user name in link", "https://noc@horizon:8980/opennms/", true, "must not have credentials"},
		{"empty user name in link", "https://@horizon:8980/opennms/", true, "must not have credentials"},
		{"upper case scheme", "HTTP://am:9093", false, ""},
		{"query", "http://am:9093/?token=x", false, ""},
		{"query in link", "http://horizon:8980/opennms/?token=x", true, "must not have a query"},
		{"empty query in link", "http://horizon:8980/opennms/?", true, "must not have a query"},
		{"no scheme", "am:9093", false, "must be http or https"},
		{"host as scheme", "localhost:9093", false, "must be http or https"},
		{"empty", "", false, "must be http or https"},
		{"other scheme", "ftp://am:9093", false, "must be http or https"},
		{"no host", "http://", false, "must have a host"},
		{"no scheme with host", "//am:9093", false, "must be http or https"},
		{"fragment", "http://am:9093/#x", false, "must not have a fragment"},
		{"empty fragment", "http://am:9093/#", false, "must not have a fragment"},
		{"fragment in link", "http://horizon:8980/opennms/#x", true, "must not have a fragment"},
		{"unparseable", "http://am 1:9093", false, "invalid character"},
		{"credentials hidden", "ftp://noc:secret@am:9093", false, `"ftp://am:9093"`},
		{"unparseable credentials hidden", "http://noc:secret@am 1:9093", false, `"http://am 1:9093"`},
		{"query hidden", "http://am:9093/?token=secret#x", false, `"http://am:9093/": must not have a fragment`},
		{"opaque credentials hidden", "noc:secret@am:9093", false, `"noc:am:9093": must be http or https`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkHTTPURL("test URL", tt.url, tt.link)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("checkHTTPURL() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("checkHTTPURL() error = %v, want containing %q", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Errorf("checkHTTPURL() error = %v, want the password redacted", err)
			}
		})
	}
}

func TestSafeURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"plain", "http://am:9093/api/v2/alerts", "http://am:9093/api/v2/alerts"},
		{"no port", "https://am.example.com/prefix/api/v2/alerts", "https://am.example.com/prefix/api/v2/alerts"},
		{"credentials", "https://noc:secret@am:9093/api/v2/alerts", "https://am:9093/api/v2/alerts"},
		{"user name only", "https://noc@am:9093/api/v2/alerts", "https://am:9093/api/v2/alerts"},
		{"query", "http://am:9093/api/v2/alerts?token=secret&org=noc", "http://am:9093/api/v2/alerts"},
		{"empty query", "http://am:9093/api/v2/alerts?", "http://am:9093/api/v2/alerts"},
		{"fragment", "http://am:9093/api/v2/alerts#secret", "http://am:9093/api/v2/alerts"},
		{"everything", "https://noc:secret@am:9093/prefix/api/v2/alerts?token=secret#secret", "https://am:9093/prefix/api/v2/alerts"},
		{"escaped path", "http://am:9093/a%2Fb/api/v2/alerts", "http://am:9093/a%2Fb/api/v2/alerts"},
		{"ipv6", "http://noc:secret@[::1]:9093/api/v2/alerts", "http://[::1]:9093/api/v2/alerts"},
		{"no scheme", "am:9093", "am:9093"},
		{"opaque credentials", "noc:secret@am:9093", "noc:am:9093"},
		{"empty", "", ""},
		{"unparseable", "http://noc:secret@am 1:9093/prefix?token=secret", "http://am 1:9093/prefix"},
		{"unparseable password with at", "http://noc:p@ss@am 1:9093", "http://am 1:9093"},
		{"opaque with space", "noc:secret@am 1:9093?token=secret", "noc:am 1:9093"},
		{"unparseable without scheme", "%zz:secret@am:9093?token=secret", "am:9093"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeURL(tt.url); got != tt.want {
				t.Errorf("safeURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestCheckHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		wantErr string
	}{
		{"none", nil, ""},
		{"valid", map[string]string{"X-Scope-OrgID": "noc", "Authorization": "Bearer secret\tvalue"}, ""},
		{"space in name", map[string]string{"X Scope": "noc"}, `invalid header "X Scope": not a valid header name`},
		{"empty name", map[string]string{"": "noc"}, `invalid header "": not a valid header name`},
		{"newline in value", map[string]string{"Authorization": "Bearer secret\nX-Other: 1"}, `invalid header "Authorization": value has a control character`},
		{"host", map[string]string{"host": "am"}, `invalid header "host": Host cannot be set`},
		{"content type", map[string]string{"Content-Type": "text/plain"}, `invalid header "Content-Type": Content-Type cannot be set`},
		{"content length", map[string]string{"content-length": "1"}, `invalid header "content-length": Content-Length cannot be set`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := checkHeaders(tt.headers)
			if tt.wantErr == "" {
				if len(problems) != 0 {
					t.Errorf("checkHeaders() = %v, want no problems", problems)
				}
				return
			}
			if len(problems) != 1 || problems[0].Error() != tt.wantErr {
				t.Errorf("checkHeaders() = %v, want %q", problems, tt.wantErr)
			}
			if strings.Contains(problems[0].Error(), "secret") {
				t.Errorf("checkHeaders() = %v, want no header values", problems)
			}
		})
	}
}

func TestRegister(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())

	if err := register(reg, collectors.NewGoCollector()); err == nil {
		t.Error("register() error = nil, want error for a collector already registered")
	}
	if err := register(reg, prometheus.NewCounter(prometheus.CounterOpts{Name: "test_total", Help: "Test."})); err != nil {
		t.Errorf("register() error = %v", err)
	}
}
