package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAsyncImageTaskRead(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{name: "legacy v1 task", method: http.MethodGet, path: "/v1/images/tasks/imgtask_123", want: true},
		{name: "legacy unversioned task", method: http.MethodGet, path: "/images/tasks/imgtask_123", want: true},
		{name: "durable BB task", method: http.MethodGet, path: "/v1/images/tasks_async/asyncimg_123", want: true},
		{name: "durable SC task", method: http.MethodGet, path: "/v1/tasks_sc/asyncimg_123", want: true},
		{name: "legacy task write", method: http.MethodPost, path: "/v1/images/tasks/imgtask_123", want: false},
		{name: "durable BB task write", method: http.MethodPost, path: "/v1/images/tasks_async/asyncimg_123", want: false},
		{name: "durable SC task write", method: http.MethodPost, path: "/v1/tasks_sc/asyncimg_123", want: false},
		{name: "generation endpoint", method: http.MethodGet, path: "/v1/images/generations", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isAsyncImageTaskRead(tt.method, tt.path))
		})
	}
}
