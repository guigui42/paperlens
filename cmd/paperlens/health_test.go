package main

import "testing"

func TestHealthTargetPreservesExplicitListenHost(t *testing.T) {
	for _, tc := range []struct{ listen, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:8080/healthz"},
		{"192.0.2.20:8080", "http://192.0.2.20:8080/healthz"},
		{"[::1]:8080", "http://[::1]:8080/healthz"},
		{"0.0.0.0:8080", "http://127.0.0.1:8080/healthz"},
		{"[::]:8080", "http://[::1]:8080/healthz"},
		{":8080", "http://127.0.0.1:8080/healthz"},
	} {
		t.Run(tc.listen, func(t *testing.T) {
			got, err := healthTarget(tc.listen)
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}
