package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func registerUpstreamProviderRoutes(apiRouter *gin.RouterGroup) {
	upstreamProviderRoute := apiRouter.Group("/upstream-providers")
	upstreamProviderRoute.Use(middleware.AdminAuth())

	upstreamProviderRoute.GET("", middleware.RequirePermission(authz.ChannelRead), controller.ListManagedUpstreamProviders)
	upstreamProviderRoute.GET("/groups/compare", middleware.RequirePermission(authz.ChannelRead), controller.GetManagedUpstreamGroupComparison)
	upstreamProviderRoute.GET("/:id", middleware.RequirePermission(authz.ChannelRead), controller.GetManagedUpstreamProvider)
	upstreamProviderRoute.GET("/:id/groups", middleware.RequirePermission(authz.ChannelRead), controller.GetManagedUpstreamProviderGroups)

	upstreamProviderRoute.POST("",
		middleware.RequirePermission(authz.ChannelSensitiveWrite),
		middleware.CriticalRateLimit(),
		middleware.DisableCache(),
		controller.CreateManagedUpstreamProvider,
	)
	upstreamProviderRoute.PUT("/:id",
		middleware.RequirePermission(authz.ChannelSensitiveWrite),
		middleware.CriticalRateLimit(),
		middleware.DisableCache(),
		controller.UpdateManagedUpstreamProvider,
	)
	upstreamProviderRoute.DELETE("/:id",
		middleware.RequirePermission(authz.ChannelSensitiveWrite),
		middleware.CriticalRateLimit(),
		middleware.DisableCache(),
		controller.DeleteManagedUpstreamProvider,
	)
	upstreamProviderRoute.POST("/sync-all",
		middleware.RequirePermission(authz.ChannelOperate),
		middleware.CriticalRateLimit(),
		middleware.DisableCache(),
		controller.SyncAllManagedUpstreamProviders,
	)
	upstreamProviderRoute.POST("/:id/test",
		middleware.RequirePermission(authz.ChannelOperate),
		middleware.CriticalRateLimit(),
		middleware.DisableCache(),
		controller.TestManagedUpstreamProvider,
	)
	upstreamProviderRoute.POST("/:id/sync",
		middleware.RequirePermission(authz.ChannelOperate),
		middleware.CriticalRateLimit(),
		middleware.DisableCache(),
		controller.SyncManagedUpstreamProvider,
	)
	upstreamProviderRoute.POST("/:id/provision",
		middleware.RequirePermission(authz.ChannelSensitiveWrite),
		middleware.CriticalRateLimit(),
		middleware.DisableCache(),
		controller.ProvisionManagedUpstreamChannels,
	)
}
