package server

import (
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ServiceSyncServerOption func(*ServiceSyncServer) error

// WithLogger sets the logger, which must not be nil
func WithLogger(logger *slog.Logger) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		if logger == nil {
			return errors.New("invalid logger: must not be nil")
		}
		s.logger = logger

		return nil
	}
}

// WithHeaders sets headers added to every request to Alertmanager. An error
// is returned by NewServiceSyncServer if any is not a valid HTTP header, or is
// Host, Content-Type or Content-Length.
func WithHeaders(headers map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.deliveryConfig.headers = headers

		return nil
	}
}

// WithAlertmanagerUrl sets the base URLs of the Alertmanagers to send alerts
// to. An error is returned by NewServiceSyncServer if the list is empty, or
// any URL is not an http or https URL with a host, has a fragment or is listed
// more than once, or if WithAlertManagerSrv is also used.
func WithAlertmanagerUrl(list []string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.deliveryConfig.urls = list
		s.deliveryConfig.urlsSet = true

		return nil
	}
}

// WithSRVCacheTTL sets how long SRV lookups are cached, where zero disables
// caching. An error is returned by NewServiceSyncServer if it is negative.
func WithSRVCacheTTL(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.deliveryConfig.srvCacheTTL = d

		return nil
	}
}

// WithAlertManagerSrv sets the SRV record the Alertmanagers to send alerts to
// are looked up from, and the scheme of their URLs. An error is returned by
// NewServiceSyncServer if the scheme is not http or https, the record is
// empty, or if WithAlertmanagerUrl is also used.
func WithAlertManagerSrv(scheme, srv string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.deliveryConfig.srvScheme = scheme
		s.deliveryConfig.srvName = srv
		s.deliveryConfig.srvSet = true

		return nil
	}
}

// WithAlertLabels sets labels that are added to all alerts. An error is
// returned by NewServiceSyncServer if any label clashes with a built-in label,
// has an invalid name or has an empty value.
func WithAlertLabels(labels map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translationConfig.extraLabels = labels

		return nil
	}
}

// WithAlertAnnotations sets annotations that are added to all alerts. An
// error is returned by NewServiceSyncServer if any annotation clashes with a
// built-in annotation, has an invalid name or has an empty value.
func WithAlertAnnotations(annotations map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translationConfig.extraAnnotations = annotations

		return nil
	}
}

// WithRunbookURLTemplate sets a Go text/template used for the runbook_url
// annotation of alerts when an alarm's operator instructions contain no URL.
// An error is returned by NewServiceSyncServer if the template is invalid.
func WithRunbookURLTemplate(tmpl string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translationConfig.runbookURLTemplate = tmpl

		return nil
	}
}

// WithSeverityMap sets the alert severity used in place of the name of an
// OpenNMS severity, keyed by severity name (case-insensitive). Severities not
// in the map keep their lowercased name. An error is returned by
// NewServiceSyncServer if a key is not a firing OpenNMS severity or a value is
// empty.
func WithSeverityMap(m map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translationConfig.severityMap = m

		return nil
	}
}

// WithURLMapping sets the base URL of each Horizon instance, keyed by instance
// ID, used to link alerts to their alarm. An error is returned by
// NewServiceSyncServer if any instance ID is empty, or any URL is not an http
// or https URL with a host, or has a query or fragment.
func WithURLMapping(m map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translationConfig.urlMap = m

		return nil
	}
}

// WithRegistry sets the registry metrics are registered with, which must not
// be nil. An error is returned by NewServiceSyncServer if any metric cannot be
// registered, such as when the registry already has the Go and process
// collectors.
func WithRegistry(reg *prometheus.Registry) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		if reg == nil {
			return errors.New("invalid registry: must not be nil")
		}
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
		s.translationConfig.resolveTimeout = d

		return nil
	}
}

// WithResolveHold sets extra time that firing alarm alerts stay active after
// the resolve timeout, so they are not resolved while OpenNMS restarts.
// Heartbeat alerts and cleared alarms are not held.
func WithResolveHold(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translationConfig.resolveHold = d

		return nil
	}
}

// WithFlushThreshold sets the number of alarms at which a batch is flushed.
// The threshold is checked once all alarms of an update are added, so a
// batch may hold more alarms than this. An error is returned by
// NewServiceSyncServer if it is less than one.
func WithFlushThreshold(n int) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.flushThreshold = n

		return nil
	}
}

// WithFlushInterval sets how often a batch of alarms is flushed, whatever its
// size. The interval restarts after every flush. An error is returned by
// NewServiceSyncServer if it is not greater than zero.
func WithFlushInterval(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.flushInterval = d

		return nil
	}
}
