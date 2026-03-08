package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ServiceSyncServerOption func(*ServiceSyncServer) error

func WithLogger(logger *slog.Logger) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.logger = logger

		return nil
	}
}

func WithHeaders(headers map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.httpClient.Transport = &customTransport{
			Headers: headers,
		}

		return nil
	}
}

func WithAlertmanagerUrl(list []string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		for n, v := range list {
			// add path to url
			joined, err := url.JoinPath(v, "/api/v2/alerts")
			if err != nil {
				return err
			}

			// validate
			if _, err := url.Parse(joined); err != nil {
				return err
			}
			list[n] = joined
		}

		s.alertmanagers = func() ([]string, error) {
			return list, nil
		}

		return nil
	}
}

func WithSRVCacheTTL(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.srvCacheTTL = d

		return nil
	}
}

func WithAlertManagerSrv(scheme, srv string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		cache := &srvCache{}

		resolve := func() ([]string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
			defer cancel()

			_, ams, err := s.dnsClient.LookupSRV(ctx, "", "", srv)
			if err != nil {
				return nil, err
			}

			list := make([]string, 0, len(ams))
			for _, am := range ams {
				list = append(list, fmt.Sprintf("%s://%s/api/v2/alerts", scheme, net.JoinHostPort(am.Target, fmt.Sprint(am.Port))))
			}

			return list, nil
		}

		s.alertmanagers = func() ([]string, error) {
			if s.srvCacheTTL == 0 {
				return resolve()
			}

			if urls, fresh, stale := cache.get(); fresh {
				return urls, nil
			} else if stale {
				if cache.markResolving() {
					go func() {
						urls, err := resolve()
						if err != nil {
							s.logger.Warn("background SRV re-resolve failed", "srv", srv, "error", err)
							cache.clearResolving()
							return
						}
						cache.set(urls, s.srvCacheTTL)
					}()
				}
				return urls, nil
			}

			// Cache expired beyond stale window — resolve synchronously
			urls, err := resolve()
			if err != nil {
				return nil, err
			}
			cache.set(urls, s.srvCacheTTL)
			return urls, nil
		}

		return nil
	}
}

func WithURLMapping(m map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.urlMap = m

		return nil
	}
}

func WithRegistry(reg *prometheus.Registry) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.registry = reg

		return nil
	}
}

func WithVerbose() ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.verbose = true

		return nil
	}
}

func WithResolveTimeout(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.resolveTimeout = d

		return nil
	}
}

func WithBatchMaxSize(n int) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.batchMaxSize = n

		return nil
	}
}

func WithBatchMaxWait(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.batchMaxWait = d

		return nil
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
