package server

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

func TestCheckHTTPURL(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		allowQuery bool
		wantErr    string
	}{
		{"http", "http://am:9093", false, ""},
		{"https with path", "https://am:9093/prefix/", false, ""},
		{"credentials", "https://noc:secret@am:9093", false, ""},
		{"upper case scheme", "HTTP://am:9093", false, ""},
		{"query allowed", "http://am:9093/?token=x", true, ""},
		{"query not allowed", "http://am:9093/?token=x", false, "must not have a query"},
		{"empty query not allowed", "http://am:9093/?", false, "must not have a query"},
		{"no scheme", "am:9093", false, "must be http or https"},
		{"host as scheme", "localhost:9093", false, "must be http or https"},
		{"empty", "", false, "must be http or https"},
		{"other scheme", "ftp://am:9093", false, "must be http or https"},
		{"no host", "http://", false, "must have a host"},
		{"no scheme with host", "//am:9093", false, "must be http or https"},
		{"fragment", "http://am:9093/#x", true, "must not have a fragment"},
		{"empty fragment", "http://am:9093/#", true, "must not have a fragment"},
		{"unparseable", "http://am 1:9093", false, "invalid character"},
		{"password redacted", "ftp://noc:secret@am:9093", false, `"ftp://noc:xxxxx@am:9093"`},
		{"unparseable password redacted", "http://noc:secret@am 1:9093", false, `"http://noc:xxxxx@am 1:9093"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkHTTPURL("test URL", tt.url, tt.allowQuery)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("checkHTTPURL() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("checkHTTPURL() error = %v, want containing %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("checkHTTPURL() error = %v, want the password redacted", err)
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
