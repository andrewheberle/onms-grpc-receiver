package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// checkHTTPURL returns a problem with u as the URL of what, or nil if it is
// an http or https URL with a host and no fragment. A query is only allowed
// if allowQuery is set. Any password in u is redacted in the problem.
func checkHTTPURL(what, u string, allowQuery bool) error {
	parsed, err := url.Parse(u)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}

		return fmt.Errorf("invalid %s %q: %w", what, redactURL(u), err)
	}

	var problem string
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		problem = "must be http or https"
	case parsed.Host == "":
		problem = "must have a host"
	case strings.Contains(u, "#"):
		problem = "must not have a fragment"
	case !allowQuery && (parsed.RawQuery != "" || parsed.ForceQuery):
		problem = "must not have a query"
	default:
		return nil
	}

	return fmt.Errorf("invalid %s %q: %s", what, parsed.Redacted(), problem)
}

// redactURL hides any password in a URL that could not be parsed
func redactURL(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return u
	}

	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return u
	}

	user, _, hasPassword := strings.Cut(rest[:at], ":")
	if !hasPassword {
		return u
	}

	return scheme + "://" + user + ":xxxxx" + rest[at:]
}

// reservedHeaders are headers that cannot be added to requests to
// Alertmanager, as they are ignored or would break the request
var reservedHeaders = []string{"Content-Length", "Content-Type", "Host"}

// checkHeaders returns a problem for each header that is not a valid HTTP
// header or is reserved. Values are not included in problems, as they may
// hold credentials.
func checkHeaders(headers map[string]string) []error {
	var problems []error
	for name, value := range headers {
		switch {
		case !validHeaderName(name):
			problems = append(problems, fmt.Errorf("invalid header %q: not a valid header name", name))
		case !validHeaderValue(value):
			problems = append(problems, fmt.Errorf("invalid header %q: value has a control character", name))
		default:
			for _, reserved := range reservedHeaders {
				if http.CanonicalHeaderKey(name) == reserved {
					problems = append(problems, fmt.Errorf("invalid header %q: %s cannot be set", name, reserved))
				}
			}
		}
	}

	return problems
}

// validHeaderName reports whether name is an HTTP field name, a token of
// RFC 9110
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}

	for _, c := range []byte(name) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}

	return true
}

// validHeaderValue reports whether value is an HTTP field value, which may
// not have control characters other than tab
func validHeaderValue(value string) bool {
	for _, c := range []byte(value) {
		if (c < ' ' && c != '\t') || c == 0x7f {
			return false
		}
	}

	return true
}

// register registers every collector with reg, returning an error for each
// that could not be registered
func register(reg prometheus.Registerer, collectors ...prometheus.Collector) error {
	var errs []error
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			errs = append(errs, fmt.Errorf("registering metrics: %w", err))
		}
	}

	return errors.Join(errs...)
}
