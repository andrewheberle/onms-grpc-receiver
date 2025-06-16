package server

import (
	"reflect"
	"testing"
)

func TestWithAlertmanagerUrl(t *testing.T) {

	tests := []struct {
		name       string
		list       []string
		want       []string
		wantOptErr bool
		wantErr    bool
	}{
		{"none", []string{}, []string{}, false, false},
		{"one", []string{"http://am:9092"}, []string{"http://am:9092/api/v2/alerts"}, false, false},
		{"two", []string{"http://am1:9092", "http://am2:9092"}, []string{"http://am1:9092/api/v2/alerts", "http://am2:9092/api/v2/alerts"}, false, false},
		{"invalid", []string{"http://am1 :9092"}, []string{}, true, false},
		{"second invalid", []string{"http://am1:9092", "http://am2 :9092"}, []string{}, true, false},
		{"prefix", []string{"http://am:9092/prefix"}, []string{"http://am:9092/prefix/api/v2/alerts"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &ServiceSyncServer{}
			o := WithAlertmanagerUrl(tt.list)
			gotOptErr := o(srv)
			if tt.wantOptErr {
				if gotOptErr == nil {
					t.Errorf("WithAlertmanagerUrl() = %v, wantOptErr %v", gotOptErr, tt.wantOptErr)
				}
				return
			}

			got, gotErr := srv.alertmanagers()
			if tt.wantErr && gotErr == nil {
				t.Errorf("WithAlertmanagerUrl() = %v, wantErr %v", gotErr, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("WithAlertmanagerUrl() = %v, want %v", got, tt.want)
			}
		})
	}
}
