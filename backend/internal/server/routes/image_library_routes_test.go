package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImagePlazaBatchPublicationRouteIsRegisteredBeforeParameterizedActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		ImageLibrary: handler.NewImageLibraryHandler(nil, nil),
	}}
	registerImageLibraryRoutes(router.Group("/api/v1/admin"), handlers)

	routes := router.Routes()
	batchIndex, actionIndex := -1, -1
	for index, route := range routes {
		switch {
		case route.Method == "POST" && route.Path == "/api/v1/admin/image-plaza/publications/batch":
			batchIndex = index
		case route.Method == "POST" && route.Path == "/api/v1/admin/image-plaza/publications/:publication_id/:action":
			actionIndex = index
		}
	}
	require.NotEqual(t, -1, batchIndex)
	require.NotEqual(t, -1, actionIndex)
	require.Less(t, batchIndex, actionIndex, "the static batch endpoint must be registered before parameterized publication actions")
}
