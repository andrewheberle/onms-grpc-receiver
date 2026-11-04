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
// an http or https URL with a host and no fragment. If link is set, u is a
// link that people open from an alert, so it must not have credentials, and
// it must not have a query, as the receiver sets the query of the link. The
// problem shows u as safeURL does.
func checkHTTPURL(what, u string, link bool) error {
	parsed, err := url.Parse(u)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}

		return fmt.Errorf("invalid %s %q: %w", what, safeURL(u), err)
	}

	var problem string
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		problem = "must be http or https"
	case parsed.Host == "":
		problem = "must have a host"
	case strings.Contains(u, "#"):
		problem = "must not have a fragment"
	case link && parsed.User != nil:
		problem = "must not have credentials"
	case link && (parsed.RawQuery != "" || parsed.ForceQuery):
		problem = "must not have a query"
	default:
		return nil
	}

	return fmt.Errorf("invalid %s %q: %s", what, safeURL(u), problem)
}

// safeURL returns u as scheme://host[:port][/path], leaving out any user
// name, password, query or fragment, as they may hold credentials. It is used
// wherever a URL is shown: in metric labels, logs and errors. A URL that
// cannot be parsed has the same parts removed as far as they can be found.
func safeURL(u string) string {
	if parsed, err := url.Parse(u); err == nil {
		// a URL with no "//", such as "noc:secret@am:9093", is parsed as an
		// opaque URL with "noc" as its scheme
		opaque := parsed.Opaque
		if i := strings.LastIndex(opaque, "@"); i >= 0 {
			opaque = opaque[i+1:]
		}

		safe := url.URL{
			Scheme:  parsed.Scheme,
			Opaque:  opaque,
			Host:    parsed.Host,
			Path:    parsed.Path,
			RawPath: parsed.RawPath,
		}

		return safe.String()
	}

	// remove the query and fragment
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}

	// remove any user name and password from the authority, or from
	// everything if there is no scheme
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		if i := strings.LastIndex(u, "@"); i >= 0 {
			return u[i+1:]
		}

		return u
	}
	authority, path := rest, ""
	if i := strings.Index(rest, "/"); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		authority = authority[i+1:]
	}

	return scheme + "://" + authority + path
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
