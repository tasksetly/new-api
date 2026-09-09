package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamProviderRoutesRegisterExpectedPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api")

	require.NotPanics(t, func() {
		registerUpstreamProviderRoutes(api)
	})

	registered := make(map[string]bool)
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		http.MethodGet + " /api/upstream-providers",
		http.MethodPost + " /api/upstream-providers",
		http.MethodGet + " /api/upstream-providers/groups/compare",
		http.MethodGet + " /api/upstream-providers/:id",
		http.MethodPut + " /api/upstream-providers/:id",
		http.MethodDelete + " /api/upstream-providers/:id",
		http.MethodPost + " /api/upstream-providers/:id/test",
		http.MethodPost + " /api/upstream-providers/:id/sync",
		http.MethodGet + " /api/upstream-providers/:id/groups",
		http.MethodPost + " /api/upstream-providers/:id/provision",
		http.MethodPost + " /api/upstream-providers/sync-all",
	} {
		assert.Truef(t, registered[route], "missing route %s", route)
	}
}
