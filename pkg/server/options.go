package server

import (
	"log/slog"
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
		s.deliveryConfig.headers = headers

		return nil
	}
}

func WithAlertmanagerUrl(list []string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.deliveryConfig.discovery = discoveryStatic
		s.deliveryConfig.urls = list

		return nil
	}
}

func WithSRVCacheTTL(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.deliveryConfig.srvCacheTTL = d

		return nil
	}
}

func WithAlertManagerSrv(scheme, srv string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.deliveryConfig.discovery = discoverySRV
		s.deliveryConfig.srvScheme = scheme
		s.deliveryConfig.srvName = srv

		return nil
	}
}

// WithAlertLabels sets labels that are added to all alerts. An error is
// returned by NewServiceSyncServer if any label clashes with a built-in label,
// has an invalid name or has an empty value.
func WithAlertLabels(labels map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translator.extraLabels = labels

		return nil
	}
}

// WithAlertAnnotations sets annotations that are added to all alerts. An
// error is returned by NewServiceSyncServer if any annotation clashes with a
// built-in annotation, has an invalid name or has an empty value.
func WithAlertAnnotations(annotations map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translator.extraAnnotations = annotations

		return nil
	}
}

func WithURLMapping(m map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translator.urlMap = m

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
		s.translator.resolveTimeout = d

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
