package server

import (
	"errors"
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

// errAlertmanagerExclusive is returned when Alertmanagers are set both as
// URLs and as an SRV record
var errAlertmanagerExclusive = errors.New("alertmanager URLs and SRV record cannot both be set")

func WithAlertmanagerUrl(list []string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		if s.deliveryConfig.discovery == discoverySRV {
			return errAlertmanagerExclusive
		}
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
		if s.deliveryConfig.discovery == discoveryStatic {
			return errAlertmanagerExclusive
		}
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

func WithURLMapping(m map[string]string) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.translationConfig.urlMap = m

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
// batch may hold more alarms than this.
func WithFlushThreshold(n int) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.flushThreshold = n

		return nil
	}
}

// WithFlushInterval sets how often a batch of alarms is flushed, whatever its
// size. The interval restarts after every flush.
func WithFlushInterval(d time.Duration) ServiceSyncServerOption {
	return func(s *ServiceSyncServer) error {
		s.flushInterval = d

		return nil
	}
}
