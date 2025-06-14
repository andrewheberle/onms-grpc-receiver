package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

type ServiceSyncServerOption func(*ServiceSyncServer)

func WithLogger(logger *slog.Logger) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) {
		s.logger = logger
	}
}

func WithHeaders(headers map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) {
		s.httpClient.Transport = &customTransport{
			Headers: headers,
		}
	}
}

func WithAlertmanagerUrl(u *url.URL) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) {
		u.Path = "/api/v2/alerts"
		s.alertmanagers = func() ([]string, error) {
			return []string{u.String()}, nil
		}
	}
}

func WithAlertManagerSrv(scheme, srv string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) {
		s.alertmanagers = func() ([]string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
			defer cancel()

			_, ams, err := s.dnsClient.LookupSRV(ctx, "", "", srv)
			if err != nil {
				return nil, err
			}

			list := make([]string, 0)
			for _, am := range ams {
				list = append(list, fmt.Sprintf("%s://%s:%d/api/v2/alerts", scheme, am.Target, am.Port))
			}

			return list, nil
		}
	}
}

func WithSiteMap(m map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) {
		s.siteMap = m
	}
}

func WithURLMap(m map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) {
		s.urlMap = m
	}
}

type customTransport struct {
	Transport http.RoundTripper
	Headers   map[string]string
}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	newReq := req.Clone(req.Context())
	if newReq.Header == nil {
		newReq.Header = make(http.Header)
	}
	for key, value := range t.Headers {
		newReq.Header.Set(key, value)
	}

	// Use the underlying transport to execute the request
	return t.transport().RoundTrip(newReq)
}

func (t *customTransport) transport() http.RoundTripper {
	if t.Transport != nil {
		return t.Transport
	}
	return http.DefaultTransport
}
