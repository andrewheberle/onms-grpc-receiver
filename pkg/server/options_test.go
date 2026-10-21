package server

import (
	"reflect"
	"testing"
)

func TestWithAlertmanagerUrlKeepsList(t *testing.T) {
	list := []string{"http://am1:9092", "http://am2:9092"}

	if _, err := NewServiceSyncServer(WithAlertmanagerUrl(list)); err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	if want := []string{"http://am1:9092", "http://am2:9092"}; !reflect.DeepEqual(list, want) {
		t.Errorf("list = %v, want unchanged %v", list, want)
	}
}

func TestAlertmanagerUrlAndSrvExclusive(t *testing.T) {
	tests := []struct {
		name string
		opts []ServiceSyncServerOption
	}{
		{"url then srv", []ServiceSyncServerOption{
			WithAlertmanagerUrl([]string{"http://am:9092"}),
			WithAlertManagerSrv("http", "_alertmanager._tcp.example.com"),
		}},
		{"srv then url", []ServiceSyncServerOption{
			WithAlertManagerSrv("http", "_alertmanager._tcp.example.com"),
			WithAlertmanagerUrl([]string{"http://am:9092"}),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewServiceSyncServer(tt.opts...); err == nil {
				t.Error("NewServiceSyncServer() error = nil, want error for both alertmanager URLs and SRV record")
			}
		})
	}
}
