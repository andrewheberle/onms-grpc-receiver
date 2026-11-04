package server

import (
	"fmt"
	"net/http"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// readiness is how far a ServiceSyncServer is through its life. It is only
// ready to receive alarm updates and heartbeats while serving.
//
// Readiness does not depend on whether Alertmanager can be reached: alerts
// are retried and repaired by the next snapshot, while refusing Horizon
// instances would only lose more, and would make every receiver unready at
// once.
type readiness int

const (
	// starting is from when the server is created until Serve serves gRPC
	starting readiness = iota

	// serving is while gRPC is serving
	serving

	// stopping is from when Serve is stopped or fails until it returns,
	// while gRPC stops and the alarms held are flushed
	stopping

	// stopped is once Serve has returned
	stopped
)

func (r readiness) String() string {
	switch r {
	case starting:
		return "starting"
	case serving:
		return "serving"
	case stopping:
		return "stopping"
	case stopped:
		return "stopped"
	}

	return fmt.Sprintf("readiness(%d)", int(r))
}

// healthServices are the services reported by the gRPC health service: the
// server as a whole, and the service Horizon instances use
var healthServices = []string{"", pb.NmsInventoryServiceSync_ServiceDesc.ServiceName}

// setReadiness moves the server to r, unless it is already further along,
// and updates the gRPC health service
func (s *ServiceSyncServer) setReadiness(r readiness) {
	s.readinessMu.Lock()
	defer s.readinessMu.Unlock()

	if r < s.readiness {
		return
	}
	s.readiness = r
	s.updateHealthLocked()
}

// setHealthServer reports readiness with h, the caller must not hold
// readinessMu
func (s *ServiceSyncServer) setHealthServer(h *health.Server) {
	s.readinessMu.Lock()
	defer s.readinessMu.Unlock()

	s.health = h
	s.updateHealthLocked()
}

// updateHealthLocked sets the status of every health service from the
// readiness, the caller must hold readinessMu
func (s *ServiceSyncServer) updateHealthLocked() {
	if s.health == nil {
		return
	}

	status := healthpb.HealthCheckResponse_NOT_SERVING
	if s.readiness == serving {
		status = healthpb.HealthCheckResponse_SERVING
	}
	for _, service := range healthServices {
		s.health.SetServingStatus(service, status)
	}
}

// currentReadiness returns how far the server is through its life
func (s *ServiceSyncServer) currentReadiness() readiness {
	s.readinessMu.Lock()
	defer s.readinessMu.Unlock()

	return s.readiness
}

// HealthHandler returns a handler for a readiness check, which responds with
// 200 OK while the server is serving, and 503 Service Unavailable while it
// is starting, stopping or has stopped. The same readiness is reported by the
// gRPC health service registered by Serve.
func (s *ServiceSyncServer) HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r := s.currentReadiness(); r != serving {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "Not ready: %s", r)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ready"))
	})
}
