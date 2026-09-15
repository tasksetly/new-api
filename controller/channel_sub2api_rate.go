package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// sub2APIRateSyncDefaultIntervalMinutes mirrors the upstream model update
// cadence: frequent enough to follow an upstream price change the same day,
// rare enough that a large channel fleet stays well inside upstream rate limits.
const sub2APIRateSyncDefaultIntervalMinutes = 30

type sub2APIRateSyncToggleRequest struct {
	ID      int  `json:"id"`
	Enabled bool `json:"enabled"`
}

// GetChannelSub2APIRate returns the upstream billing multiplier declared by a
// Sub2API channel's upstream, without persisting anything. It backs the manual
// "sync now" button, which shows the freshly fetched value before the value is
// written into the channel's cost fields.
func GetChannelSub2APIRate(c *gin.Context) {
	var req struct {
		ID int `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := service.GetSub2APIRateSyncChannel(req.ID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channel.Type != constant.ChannelTypeSub2API {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Only Sub2API channels expose an upstream billing rate"})
		return
	}
	rate, costRate, peakFactor, err := service.ProbeChannelSub2APIRate(c.Request.Context(), channel)
	if err != nil {
		if errors.Is(err, service.ErrSub2APIRateSyncUnsupported) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "The upstream does not expose /v1/sub2api/billing"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":          channel.Id,
			"rate":        rate,
			"cost_rate":   costRate,
			"peak_factor": peakFactor,
		},
	})
}

// SyncChannelSub2APIRate fetches the upstream billing declaration for one
// channel and writes it into the channel cost fields.
func SyncChannelSub2APIRate(c *gin.Context) {
	var req struct {
		ID int `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := service.GetSub2APIRateSyncChannel(req.ID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := service.SyncChannelSub2APIRate(c.Request.Context(), channel)
	if err != nil {
		if errors.Is(err, service.ErrSub2APIRateSyncUnsupported) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "The upstream does not expose /v1/sub2api/billing"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "channel.sub2api_rate_sync", map[string]any{"id": channel.Id})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    result,
	})
}

// SetChannelSub2APIRateSyncEnabled toggles the automatic upstream rate sync for
// one Sub2API channel.
func SetChannelSub2APIRateSyncEnabled(c *gin.Context) {
	var req sub2APIRateSyncToggleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := service.GetSub2APIRateSyncChannel(req.ID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.SetChannelSub2APIRateSyncEnabled(channel, req.Enabled); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "channel.sub2api_rate_sync_toggle", map[string]any{
		"id":      channel.Id,
		"enabled": req.Enabled,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":      channel.Id,
			"enabled": req.Enabled,
		},
	})
}
