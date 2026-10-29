package server

import "time"

// postableAlert is an alert posted to the Alertmanager v2 API. It matches the
// PostableAlert model in the Alertmanager OpenAPI spec, which is checked by
// the tests, so the Alertmanager module is not a dependency of the service.
type postableAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
	StartsAt     time.Time         `json:"startsAt,omitzero"`
	EndsAt       time.Time         `json:"endsAt,omitzero"`
}
